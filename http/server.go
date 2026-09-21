package http

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	nethttp "net/http"
	"strings"
	"sync"
	"time"

	"github.com/heavycaffeiner/hanami/bootstrap"
	"github.com/heavycaffeiner/hanami/process"
	"go.uber.org/fx"
)

type ServerConfig struct {
	Address           string
	ReadHeaderTimeout time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int

	// Protocol and TLS fields are used by the managed generation manager.
	// Their zero values retain the fixed HTTP server behavior.
	Protocol               Protocol
	Handler                nethttp.Handler
	TLSConfig              *tls.Config
	CertificatePEM         []byte
	PrivateKeyPEM          []byte
	Certificate            *tls.Certificate
	ProbeServerName        string
	ProbePath              string
	ProbeIdentity          string
	ProbeTimeout           time.Duration
	DrainTimeout           time.Duration
	MaxDrainingGenerations int
	ForceCloseHijacked     bool
	RuntimeFactory         RuntimeFactory
}

func (config ServerConfig) Validate() error {
	if err := validateBasicServerConfig(config); err != nil {
		return err
	}
	protocol := config.Protocol
	if protocol == "" {
		protocol = ProtocolHTTP
	}
	if protocol != ProtocolHTTP && protocol != ProtocolHTTPS {
		return fmt.Errorf("unsupported HTTP protocol %q", protocol)
	}
	if config.ProbePath != "" && (!strings.HasPrefix(config.ProbePath, "/") || strings.ContainsAny(config.ProbePath, "?#")) {
		return errors.New("HTTP probe path must be an absolute path")
	}
	if config.ProbeTimeout < 0 || config.DrainTimeout < 0 || config.MaxDrainingGenerations < 0 {
		return errors.New("HTTP transition limits must not be negative")
	}
	if protocol == ProtocolHTTPS {
		if config.Certificate != nil && (len(config.Certificate.Certificate) == 0 || config.Certificate.PrivateKey == nil) {
			return errors.New("HTTPS certificate is incomplete")
		}
		if config.Certificate == nil && config.TLSConfig == nil && (len(config.CertificatePEM) == 0 || len(config.PrivateKeyPEM) == 0) {
			return errors.New("HTTPS requires certificate material")
		}
	} else if config.Certificate != nil || config.TLSConfig != nil || len(config.CertificatePEM) != 0 || len(config.PrivateKeyPEM) != 0 {
		return errors.New("TLS material supplied for HTTP protocol")
	}
	return nil
}

func validateBasicServerConfig(config ServerConfig) error {
	if config.Address == "" {
		return errors.New("HTTP address is empty")
	}
	if _, _, err := net.SplitHostPort(config.Address); err != nil {
		return fmt.Errorf("invalid HTTP address: %w", err)
	}
	if config.ReadHeaderTimeout < 0 || config.IdleTimeout < 0 || config.MaxHeaderBytes < 0 {
		return errors.New("HTTP limits must not be negative")
	}
	return nil
}

type FixedServer struct {
	server     *nethttp.Server
	admission  *bootstrap.Admission
	controller *process.Controller

	mu       sync.Mutex
	listener net.Listener
	done     chan error
}

func NewFixedServer(config ServerConfig, handler nethttp.Handler, admission *bootstrap.Admission, controller *process.Controller) (*FixedServer, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if handler == nil {
		return nil, errors.New("HTTP handler is nil")
	}
	server := &nethttp.Server{
		Addr:              config.Address,
		Handler:           admissionHandler(admission, handler),
		ReadHeaderTimeout: config.ReadHeaderTimeout,
		IdleTimeout:       config.IdleTimeout,
		MaxHeaderBytes:    config.MaxHeaderBytes,
	}
	return &FixedServer{
		server:     server,
		admission:  admission,
		controller: controller,
		done:       make(chan error, 1),
	}, nil
}

func Module(config ServerConfig) fx.Option {
	return fx.Module(
		"hanami-http",
		fx.Provide(func(handler nethttp.Handler, admission *bootstrap.Admission, controller *process.Controller) (*FixedServer, error) {
			return NewFixedServer(config, handler, admission, controller)
		}),
		fx.Provide(func(server *FixedServer) *nethttp.Server { return server.server }),
		fx.Invoke(func(lifecycle fx.Lifecycle, server *FixedServer) {
			lifecycle.Append(fx.Hook{OnStart: server.Start, OnStop: server.Stop})
		}),
	)
}

func (server *FixedServer) Start(ctx context.Context) error {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", server.server.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", server.server.Addr, err)
	}
	server.mu.Lock()
	server.listener = listener
	server.mu.Unlock()
	go func() {
		err := server.server.Serve(listener)
		if errors.Is(err, nethttp.ErrServerClosed) {
			err = nil
		}
		server.done <- err
		close(server.done)
		if err != nil {
			server.admission.Close()
			server.controller.RequestStop(process.Request{Reason: process.StopReasonRuntimeFailure, Err: fmt.Errorf("HTTP serve loop: %w", err)})
		}
	}()
	return nil
}

func (server *FixedServer) Stop(ctx context.Context) error {
	server.admission.Close()
	shutdownErr := server.server.Shutdown(ctx)
	if shutdownErr != nil {
		closeErr := server.server.Close()
		shutdownErr = errors.Join(shutdownErr, closeErr)
	}
	select {
	case serveErr := <-server.done:
		return errors.Join(shutdownErr, serveErr)
	case <-ctx.Done():
		return errors.Join(shutdownErr, ctx.Err())
	}
}

func (server *FixedServer) Address() string {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.listener == nil {
		return server.server.Addr
	}
	return server.listener.Addr().String()
}

func admissionHandler(admission *bootstrap.Admission, next nethttp.Handler) nethttp.Handler {
	return nethttp.HandlerFunc(func(writer nethttp.ResponseWriter, request *nethttp.Request) {
		if admission.IsOpen() || request.URL.Path == "/health/live" || request.URL.Path == "/health/ready" {
			next.ServeHTTP(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(nethttp.StatusServiceUnavailable)
		_, _ = writer.Write([]byte(`{"status":"unavailable"}`))
	})
}
