package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/heavycaffeiner/hanami"
	"github.com/heavycaffeiner/hanami/bootstrap"
	hanamihttp "github.com/heavycaffeiner/hanami/http"
	"github.com/heavycaffeiner/hanami/process"
	"go.uber.org/fx"
)

type applicationConfig struct {
	Address string
}

func main() {
	hanami.Main(hanami.Spec[applicationConfig]{
		Name:    "hanami-reconfigurable-starter",
		Load:    loadConfig,
		Modules: applicationModules,
	}, hanami.WithTimeouts[applicationConfig](hanami.Timeouts{Startup: 15 * time.Second, Shutdown: 10 * time.Second}))
}

func loadConfig(context.Context) (applicationConfig, error) {
	address := os.Getenv("HANAMI_RECONFIGURABLE_ADDRESS")
	if address == "" {
		address = "127.0.0.1:8082"
	}
	config := hanamihttp.ServerConfig{Address: address}
	if err := config.Validate(); err != nil {
		return applicationConfig{}, err
	}
	return applicationConfig{Address: address}, nil
}

func applicationModules(config applicationConfig) fx.Option {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/generation", func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]string{"status": "active"})
	})
	return fx.Options(
		hanamihttp.ManagedModule(hanamihttp.ServerConfig{
			Address:                config.Address,
			Protocol:               hanamihttp.ProtocolHTTP,
			Handler:                mux,
			ProbePath:              "/.hanami/ready",
			ProbeIdentity:          "hanami-reconfigurable-starter",
			ProbeTimeout:           3 * time.Second,
			DrainTimeout:           10 * time.Second,
			MaxDrainingGenerations: 2,
		}),
		fx.Invoke(registerReplacementExample),
	)
}

func registerReplacementExample(lifecycle fx.Lifecycle, manager *hanamihttp.Manager, admission *bootstrap.Admission, controller *process.Controller, logger *slog.Logger) {
	lifecycle.Append(fx.Hook{OnStart: func(context.Context) error {
		go func() {
			for !admission.IsOpen() {
				time.Sleep(10 * time.Millisecond)
			}
			if next := os.Getenv("HANAMI_RECONFIGURABLE_NEXT_ADDRESS"); next != "" {
				result, err := manager.Replace(context.Background(), hanamihttp.ReplaceRequest{Server: hanamihttp.ServerConfig{
					Address:                next,
					Protocol:               hanamihttp.ProtocolHTTP,
					Handler:                http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { writer.WriteHeader(http.StatusNoContent) }),
					ProbePath:              "/.hanami/ready",
					ProbeIdentity:          "hanami-reconfigurable-starter",
					ProbeTimeout:           3 * time.Second,
					DrainTimeout:           10 * time.Second,
					MaxDrainingGenerations: 2,
				}})
				if err != nil {
					logger.Error("HTTP generation replacement failed", slog.Any("error", err), slog.String("state", string(result.State)))
					controller.RequestStop(process.Request{Reason: process.StopReasonRuntimeFailure, Err: err})
					return
				}
				logger.Info("HTTP generation replaced", slog.String("generation", result.GenerationID), slog.String("state", string(result.State)))
			}
		}()
		return nil
	}})
}
