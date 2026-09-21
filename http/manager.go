package http

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	nethttp "net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/heavycaffeiner/hanami/bootstrap"
	"github.com/heavycaffeiner/hanami/process"
	"go.uber.org/fx"
)

// Protocol identifies the wire protocol used by a managed generation.
type Protocol string

const (
	ProtocolHTTP  Protocol = "http"
	ProtocolHTTPS Protocol = "https"
)

// ReplaceState is the result of a generation transition.
type ReplaceState string

const (
	ReplaceRejected                ReplaceState = "rejected"
	ReplaceApplied                 ReplaceState = "applied"
	ReplaceAppliedWithDrainFailure ReplaceState = "applied_with_drain_failure"

	// Short names are kept for callers that use the state names directly.
	Rejected                = ReplaceRejected
	Applied                 = ReplaceApplied
	AppliedWithDrainFailure = ReplaceAppliedWithDrainFailure
)

// Runtime is the small lifecycle seam for native net/http or an application
// server that can serve the manager-owned listener without framework types.
type Runtime interface {
	Serve(net.Listener) error
	Shutdown(context.Context) error
	Close() error
}

// ProbeResponse is the authenticated management response returned by a generation gate.
type ProbeResponse struct {
	StatusCode int
	Identity   string
	Body       string
}

// RuntimeControl is supplied to custom runtimes so they can preserve
// generation-local admission without importing a web framework.
type RuntimeControl interface {
	Admitted() bool
	Probe(method, path, token string) (ProbeResponse, bool)
	Wrap(nethttp.Handler) nethttp.Handler
}

// GenerationGate controls one candidate or active generation. Probe accepts
// only the manager's private token and configured probe path.
type GenerationGate struct {
	path      string
	identity  string
	token     string
	admission *bootstrap.Admission
	admitted  atomic.Bool
}

func newGenerationGate(path, identity, token string, admission ...*bootstrap.Admission) *GenerationGate {
	gate := &GenerationGate{path: path, identity: identity, token: token}
	if len(admission) > 0 {
		gate.admission = admission[0]
	}
	return gate
}

func (g *GenerationGate) Admitted() bool {
	if g == nil || !g.admitted.Load() {
		return false
	}
	return g.admission == nil || g.admission.IsOpen()
}
func (g *GenerationGate) setAdmitted(value bool) { g.admitted.Store(value) }

func (g *GenerationGate) Probe(method, path, token string) (ProbeResponse, bool) {
	if g == nil || method != nethttp.MethodGet || path != g.path || token != g.token {
		return ProbeResponse{}, false
	}
	return ProbeResponse{StatusCode: nethttp.StatusOK, Identity: g.identity, Body: g.identity}, true
}

func (g *GenerationGate) Wrap(next nethttp.Handler) nethttp.Handler {
	if next == nil {
		next = nethttp.NotFoundHandler()
	}
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if response, ok := g.Probe(r.Method, r.URL.Path, r.Header.Get("X-Hanami-Probe")); ok {
			w.Header().Set("X-Hanami-Probe-Identity", response.Identity)
			w.WriteHeader(response.StatusCode)
			_, _ = io.WriteString(w, response.Body)
			return
		}
		if !g.Admitted() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(nethttp.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"status":"unavailable"}`)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RuntimeFactory constructs a runtime for one immutable generation.
type RuntimeFactory func(ServerConfig, RuntimeControl) (Runtime, error)

// ManagedServerConfig is retained as a descriptive alias. ServerConfig is
// the public configuration type used by ReplaceRequest.
type ManagedServerConfig = ServerConfig

// ManagerConfig configures manager defaults without hiding native HTTP types.
type ManagerConfig struct {
	Server     ServerConfig
	Admission  *bootstrap.Admission
	Controller *process.Controller
}

// ReplaceRequest describes one complete desired HTTP generation.
type ReplaceRequest struct {
	Server ServerConfig
	// Config is accepted for compatibility with early managed-manager users.
	// When non-zero it is merged with Server.
	Config  ServerConfig
	Handler nethttp.Handler
}

// ReplaceResult reports the observed outcome. An error is possible with an
// applied state when retiring the previous generation fails.
type ReplaceResult struct {
	GenerationID string
	State        ReplaceState
}

// ServerState is a sanitized snapshot of manager-owned runtime state.
type ServerState struct {
	GenerationID           string
	Address                string
	Protocol               Protocol
	Active                 bool
	Serving                bool
	TLS                    bool
	CertificateFingerprint string
	Candidates             int
	Draining               int
}

// Manager owns native servers and listeners for a logical HTTP endpoint.
type Manager struct {
	mu         sync.Mutex
	config     ManagedServerConfig
	handler    nethttp.Handler
	admission  *bootstrap.Admission
	controller *process.Controller
	probeToken string
	generation uint64
	active     *generation
	candidates map[*generation]struct{}
	draining   map[*generation]struct{}
	closed     bool
}

type generation struct {
	id          string
	config      ManagedServerConfig
	runtime     Runtime
	server      *nethttp.Server
	listener    net.Listener
	serveDone   chan error
	gate        *GenerationGate
	promoted    atomic.Bool
	certificate atomic.Pointer[tls.Certificate]
	hijackedMu  sync.Mutex
	hijacked    map[net.Conn]struct{}
	closed      atomic.Bool
}

// NewManager accepts a ServerConfig or ManagerConfig and optional native
// handler, admission and process controller values.
func NewManager(args ...any) (*Manager, error) {
	var cfg ManagedServerConfig
	var handler nethttp.Handler
	var admission *bootstrap.Admission
	var controller *process.Controller
	for _, arg := range args {
		switch value := arg.(type) {
		case ManagerConfig:
			cfg, admission, controller = value.Server, value.Admission, value.Controller
		case ManagedServerConfig:
			cfg = value
		case *ManagedServerConfig:
			if value == nil {
				return nil, errors.New("managed HTTP config is nil")
			}
			cfg = *value
		case nethttp.Handler:
			handler = value
		case *bootstrap.Admission:
			admission = value
		case *process.Controller:
			controller = value
		case Protocol:
			cfg.Protocol = value
		case string:
			if cfg.Address == "" {
				cfg.Address = value
			}
		default:
			return nil, fmt.Errorf("unsupported HTTP manager argument %T", arg)
		}
	}
	if cfg.Handler == nil {
		cfg.Handler = handler
	}
	if cfg.Handler == nil {
		cfg.Handler = nethttp.NotFoundHandler()
	}
	cfg, err := cfg.normalized()
	if err != nil {
		return nil, err
	}
	if admission == nil {
		admission = bootstrap.NewAdmission()
		admission.Open()
	}
	if controller == nil {
		controller = process.NewController()
	}
	token, err := newProbeToken()
	if err != nil {
		return nil, fmt.Errorf("create probe identity: %w", err)
	}
	return &Manager{
		config: cfg, handler: cfg.Handler, admission: admission, controller: controller,
		probeToken: token, candidates: make(map[*generation]struct{}), draining: make(map[*generation]struct{}),
	}, nil
}

// NewManagedManager is the typed constructor for callers that prefer not to
// use the compatibility constructor.
func NewManagedManager(config ManagedServerConfig) (*Manager, error) { return NewManager(config) }

func newProbeToken() (string, error) {
	var b [24]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (config ManagedServerConfig) normalized() (ManagedServerConfig, error) {
	if config.Protocol == "" {
		config.Protocol = ProtocolHTTP
	}
	if config.ProbePath == "" {
		config.ProbePath = "/health/ready"
	}
	if config.ProbeIdentity == "" {
		config.ProbeIdentity = "hanami"
	}
	if config.ProbeTimeout == 0 {
		config.ProbeTimeout = 3 * time.Second
	}
	if config.DrainTimeout == 0 {
		config.DrainTimeout = 10 * time.Second
	}
	if config.MaxDrainingGenerations == 0 {
		config.MaxDrainingGenerations = 2
	}
	if config.ProbeServerName == "" {
		host, _, _ := net.SplitHostPort(config.Address)
		if host == "" || host == "0.0.0.0" || host == "::" || host == "[::" {
			host = "localhost"
		}
		config.ProbeServerName = strings.Trim(host, "[]")
	}
	if config.Handler == nil {
		config.Handler = nethttp.NotFoundHandler()
	}
	if config.CertificatePEM != nil || config.PrivateKeyPEM != nil {
		cert, err := tls.X509KeyPair(config.CertificatePEM, config.PrivateKeyPEM)
		if err != nil {
			return ManagedServerConfig{}, fmt.Errorf("load TLS certificate: %w", err)
		}
		config.Certificate = &cert
	}
	if err := config.Validate(); err != nil {
		return ManagedServerConfig{}, err
	}
	if config.Protocol == ProtocolHTTPS {
		if config.Certificate == nil && config.TLSConfig != nil {
			config.TLSConfig = config.TLSConfig.Clone()
			if len(config.TLSConfig.Certificates) == 0 && config.TLSConfig.GetCertificate == nil {
				return ManagedServerConfig{}, errors.New("TLS config has no certificate")
			}
		} else if config.Certificate != nil {
			cert := *config.Certificate
			config.Certificate = &cert
		}
	}
	return config, nil
}

func (m *Manager) configFor(req ReplaceRequest) (ManagedServerConfig, error) {
	cfg := req.Config
	if cfg.Address == "" {
		cfg = m.config
	}
	if req.Server.Address != "" {
		cfg = req.Server
	}
	if req.Handler != nil {
		cfg.Handler = req.Handler
	}
	if cfg.Handler == nil {
		cfg.Handler = m.handler
	}
	return cfg.normalized()
}
func (m *Manager) Replace(ctx context.Context, req ReplaceRequest) (ReplaceResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ReplaceResult{State: ReplaceRejected}, errors.New("HTTP manager is closed")
	}
	cfg, err := m.configFor(req)
	if err != nil {
		m.mu.Unlock()
		return ReplaceResult{State: ReplaceRejected}, err
	}
	if m.active != nil && equivalentConfig(m.active.config, cfg) {
		id := m.active.id
		m.mu.Unlock()
		return ReplaceResult{GenerationID: id, State: ReplaceApplied}, nil
	}
	if len(m.draining) >= cfg.MaxDrainingGenerations && m.active != nil {
		m.mu.Unlock()
		return ReplaceResult{State: ReplaceRejected}, fmt.Errorf("draining generation limit reached")
	}
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		return ReplaceResult{State: ReplaceRejected}, err
	}
	candidate, err := m.prepareGeneration(cfg)
	if err != nil {
		m.mu.Unlock()
		return ReplaceResult{State: ReplaceRejected}, err
	}
	m.candidates[candidate] = struct{}{}
	if err := m.startGeneration(candidate); err != nil {
		delete(m.candidates, candidate)
		m.mu.Unlock()
		_ = candidate.close(context.Background(), cfg.ForceCloseHijacked)
		return ReplaceResult{State: ReplaceRejected}, err
	}
	if err := m.probe(ctx, candidate); err != nil {
		delete(m.candidates, candidate)
		m.mu.Unlock()
		_ = candidate.close(context.Background(), cfg.ForceCloseHijacked)
		return ReplaceResult{State: ReplaceRejected}, err
	}
	if err := ctx.Err(); err != nil {
		delete(m.candidates, candidate)
		m.mu.Unlock()
		_ = candidate.close(context.Background(), cfg.ForceCloseHijacked)
		return ReplaceResult{State: ReplaceRejected}, err
	}
	old := m.active
	if old != nil && old.gate != nil {
		old.gate.setAdmitted(false)
	}
	candidate.promoted.Store(true)
	candidate.gate.setAdmitted(true)
	m.active = candidate
	delete(m.candidates, candidate)
	m.config = cfg
	m.handler = cfg.Handler
	if old == nil {
		m.mu.Unlock()
		return ReplaceResult{GenerationID: candidate.id, State: ReplaceApplied}, nil
	}
	m.draining[old] = struct{}{}
	m.mu.Unlock()
	// The old generation is now independent of the caller's cancellation.
	drainErr := m.drainGeneration(old, cfg.DrainTimeout, cfg.ForceCloseHijacked)
	m.mu.Lock()
	delete(m.draining, old)
	m.mu.Unlock()
	if drainErr != nil {
		return ReplaceResult{GenerationID: candidate.id, State: ReplaceAppliedWithDrainFailure}, drainErr
	}
	return ReplaceResult{GenerationID: candidate.id, State: ReplaceApplied}, nil
}

func equivalentConfig(a, b ManagedServerConfig) bool {
	if a.Address != b.Address || a.Protocol != b.Protocol || a.ReadHeaderTimeout != b.ReadHeaderTimeout || a.IdleTimeout != b.IdleTimeout || a.MaxHeaderBytes != b.MaxHeaderBytes || a.ProbePath != b.ProbePath || a.ProbeIdentity != b.ProbeIdentity || a.ProbeServerName != b.ProbeServerName {
		return false
	}
	return certificateFingerprint(a) == certificateFingerprint(b)
}

func certificateFingerprint(c ManagedServerConfig) string {
	if c.Certificate == nil || len(c.Certificate.Certificate) == 0 {
		return ""
	}
	return hex.EncodeToString(c.Certificate.Certificate[0])
}
func (m *Manager) prepareGeneration(cfg ManagedServerConfig) (*generation, error) {
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", cfg.Address)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", cfg.Address, err)
	}
	m.generation++
	id := strconv.FormatUint(m.generation, 10)
	g := &generation{id: id, config: cfg, listener: listener, serveDone: make(chan error, 1), hijacked: make(map[net.Conn]struct{})}
	g.gate = newGenerationGate(cfg.ProbePath, cfg.ProbeIdentity, m.probeToken, m.admission)
	if cfg.Protocol == ProtocolHTTPS {
		tlsConfig, err := makeTLSConfig(cfg, g)
		if err != nil {
			_ = listener.Close()
			return nil, err
		}
		g.listener = tls.NewListener(listener, tlsConfig)
	}
	handler := g.gate.Wrap(m.handler)
	if cfg.RuntimeFactory != nil {
		runtime, err := cfg.RuntimeFactory(cfg, g.gate)
		if err != nil {
			_ = listener.Close()
			return nil, fmt.Errorf("create HTTP runtime: %w", err)
		}
		if runtime == nil {
			_ = listener.Close()
			return nil, errors.New("HTTP runtime factory returned nil runtime")
		}
		g.runtime = runtime
	} else {
		g.server = &nethttp.Server{Addr: cfg.Address, Handler: handler, ReadHeaderTimeout: cfg.ReadHeaderTimeout, IdleTimeout: cfg.IdleTimeout, MaxHeaderBytes: cfg.MaxHeaderBytes}
		g.server.ConnState = func(conn net.Conn, state nethttp.ConnState) {
			if state == nethttp.StateHijacked {
				g.hijackedMu.Lock()
				g.hijacked[conn] = struct{}{}
				g.hijackedMu.Unlock()
			}
		}
		g.runtime = nativeRuntime{server: g.server}
	}
	return g, nil
}
func makeTLSConfig(cfg ManagedServerConfig, g *generation) (*tls.Config, error) {
	base := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.TLSConfig != nil {
		base = cfg.TLSConfig.Clone()
		if base.MinVersion < tls.VersionTLS12 {
			base.MinVersion = tls.VersionTLS12
		}
	}
	if cfg.Certificate != nil {
		cert := *cfg.Certificate
		if len(cert.Certificate) == 0 {
			return nil, errors.New("TLS certificate is empty")
		}
		if _, err := x509.ParseCertificate(cert.Certificate[0]); err != nil {
			return nil, fmt.Errorf("parse TLS certificate: %w", err)
		}
		g.certificate.Store(&cert)
		base.Certificates = nil
		base.GetCertificate = func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			cert := g.certificate.Load()
			if cert == nil {
				return nil, errors.New("TLS certificate unavailable")
			}
			return cert, nil
		}
	} else if len(base.Certificates) == 0 && base.GetCertificate == nil {
		return nil, errors.New("TLS config has no certificate")
	}
	return base, nil
}

type nativeRuntime struct{ server *nethttp.Server }

func (r nativeRuntime) Serve(listener net.Listener) error  { return r.server.Serve(listener) }
func (r nativeRuntime) Shutdown(ctx context.Context) error { return r.server.Shutdown(ctx) }
func (r nativeRuntime) Close() error                       { return r.server.Close() }

func (m *Manager) generationHandler(g *generation) nethttp.Handler {
	return g.gate.Wrap(m.handler)
}

func (m *Manager) startGeneration(g *generation) error {
	go func() {
		err := g.runtime.Serve(g.listener)
		if errors.Is(err, nethttp.ErrServerClosed) {
			err = nil
		}
		g.serveDone <- err
		if err != nil && g.promoted.Load() {
			if m.admission != nil {
				m.admission.Close()
			}
			m.controller.RequestStop(process.Request{Reason: process.StopReasonRuntimeFailure, Err: fmt.Errorf("HTTP serve loop: %w", err)})
		}
	}()
	return nil
}

func (m *Manager) probe(ctx context.Context, g *generation) error {
	probeCtx, cancel := context.WithTimeout(ctx, g.config.ProbeTimeout)
	defer cancel()
	address := g.listener.Addr().String()
	scheme := string(g.config.Protocol)
	path := g.config.ProbePath
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u := &url.URL{Scheme: scheme, Host: address, Path: path}
	transport := &nethttp.Transport{}
	if g.config.Protocol == ProtocolHTTPS {
		pool := x509.NewCertPool()
		if g.config.Certificate != nil {
			cert, err := x509.ParseCertificate(g.config.Certificate.Certificate[0])
			if err != nil {
				return err
			}
			pool.AddCert(cert)
		}
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, ServerName: g.config.ProbeServerName}
	}
	client := &nethttp.Client{Transport: transport, CheckRedirect: func(*nethttp.Request, []*nethttp.Request) error { return nethttp.ErrUseLastResponse }}
	req, err := nethttp.NewRequestWithContext(probeCtx, nethttp.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Hanami-Probe", m.probeToken)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("probe candidate: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != nethttp.StatusOK {
		return fmt.Errorf("candidate probe returned HTTP %s", resp.Status)
	}
	if resp.Header.Get("X-Hanami-Probe-Identity") != g.config.ProbeIdentity || string(body) != g.config.ProbeIdentity {
		return errors.New("candidate probe identity mismatch")
	}
	return nil
}

func (m *Manager) drainGeneration(g *generation, timeout time.Duration, forceHijacked bool) error {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	err := g.runtime.Shutdown(ctx)
	if err != nil {
		err = errors.Join(err, g.runtime.Close())
		if forceHijacked {
			g.closeHijacked()
		}
	}
	select {
	case serveErr := <-g.serveDone:
		err = errors.Join(err, serveErr)
	case <-ctx.Done():
		if forceHijacked {
			g.closeHijacked()
		}
		err = errors.Join(err, ctx.Err())
	}
	g.closed.Store(true)
	return err
}

func (g *generation) close(ctx context.Context, forceHijacked bool) error {
	if g.closed.Swap(true) {
		return nil
	}
	err := g.runtime.Shutdown(ctx)
	if err != nil {
		err = errors.Join(err, g.runtime.Close())
	}
	if forceHijacked {
		g.closeHijacked()
	}
	select {
	case serveErr := <-g.serveDone:
		err = errors.Join(err, serveErr)
	case <-ctx.Done():
		err = errors.Join(err, ctx.Err())
	}
	return err
}

func (g *generation) closeHijacked() {
	g.hijackedMu.Lock()
	defer g.hijackedMu.Unlock()
	for conn := range g.hijacked {
		_ = conn.Close()
		delete(g.hijacked, conn)
	}
}

// RotateCertificate atomically publishes a validated certificate for future
// TLS handshakes without replacing the listener or closing current connections.
func (m *Manager) RotateCertificate(cert tls.Certificate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil || m.active.config.Protocol != ProtocolHTTPS {
		return errors.New("no active TLS generation")
	}
	if len(cert.Certificate) == 0 || cert.PrivateKey == nil {
		return errors.New("TLS certificate is incomplete")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return fmt.Errorf("parse TLS certificate: %w", err)
	}
	privateKey, ok := cert.PrivateKey.(interface{ Public() crypto.PublicKey })
	if !ok {
		return errors.New("TLS private key has no public key")
	}
	if !publicKeysEqual(leaf.PublicKey, privateKey.Public()) {
		return errors.New("TLS certificate and private key do not match")
	}
	copyCert := cert
	m.active.certificate.Store(&copyCert)
	m.active.config.Certificate = &copyCert
	m.config.Certificate = &copyCert
	return nil
}

func publicKeysEqual(certKey, privateKey crypto.PublicKey) bool {
	certDER, err := x509.MarshalPKIXPublicKey(certKey)
	if err != nil {
		return false
	}
	keyDER, err := x509.MarshalPKIXPublicKey(privateKey)
	if err != nil {
		return false
	}
	return bytes.Equal(certDER, keyDER)
}

// RotateTLSCertificate validates PEM material before atomically publishing it.
func (m *Manager) RotateTLSCertificate(certPEM, keyPEM []byte) error {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return fmt.Errorf("load TLS certificate: %w", err)
	}
	return m.RotateCertificate(cert)
}

// Current returns only observed, non-secret runtime state.
func (m *Manager) Current() ServerState {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := ServerState{Candidates: len(m.candidates), Draining: len(m.draining)}
	if m.active == nil {
		return state
	}
	state.GenerationID, state.Protocol, state.TLS = m.active.id, m.active.config.Protocol, m.active.config.Protocol == ProtocolHTTPS
	state.Active, state.Serving = true, !m.active.closed.Load()
	state.Address = m.active.listener.Addr().String()
	state.CertificateFingerprint = certificateFingerprint(m.active.config)
	return state
}

// Stop closes public admission and joins every owned generation.
func (m *Manager) Stop(ctx context.Context) error { return m.Shutdown(ctx) }
func (m *Manager) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	if m.admission != nil {
		m.admission.Close()
	}
	all := make([]*generation, 0, len(m.candidates)+len(m.draining)+1)
	for g := range m.candidates {
		all = append(all, g)
	}
	for g := range m.draining {
		all = append(all, g)
	}
	if m.active != nil {
		all = append(all, m.active)
	}
	m.active = nil
	m.candidates = make(map[*generation]struct{})
	m.draining = make(map[*generation]struct{})
	m.mu.Unlock()
	var result error
	for _, g := range all {
		if err := g.close(ctx, g.config.ForceCloseHijacked); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

// Start serves the manager's initial configuration. It is equivalent to an
// initial replacement and is useful with lifecycle frameworks.
func (m *Manager) Start(ctx context.Context) error {
	_, err := m.Replace(ctx, ReplaceRequest{Config: m.config})
	return err
}

// Address returns the active listener address, including an ephemeral port.
func (m *Manager) Address() string {
	state := m.Current()
	if state.Address != "" {
		return state.Address
	}
	return m.config.Address
}

// ModuleManaged is an explicit alias for the managed-generation Fx module.
func ModuleManaged(config ServerConfig) fx.Option { return ManagedModule(config) }

// Server returns the active native net/http server when the built-in runtime is used.
func (m *Manager) Server() *nethttp.Server {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		return nil
	}
	return m.active.server
}

// ManagedModule supplies the manager and joins all owned generations through
// the standard Fx lifecycle.
func ManagedModule(config ServerConfig) fx.Option {
	return fx.Module("hanami-http-managed",
		fx.Provide(func(admission *bootstrap.Admission, controller *process.Controller) (*Manager, error) {
			return NewManager(ManagerConfig{Server: config, Admission: admission, Controller: controller})
		}),
		fx.Provide(func(manager *Manager) ServerState { return manager.Current() }),
		fx.Invoke(func(lifecycle fx.Lifecycle, manager *Manager) {
			lifecycle.Append(fx.Hook{OnStart: manager.Start, OnStop: manager.Stop})
		}),
	)
}

var _ = nethttp.ErrServerClosed
