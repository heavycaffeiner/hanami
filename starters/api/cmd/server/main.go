package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/heavycaffeiner/hanami"
	"github.com/heavycaffeiner/hanami/config"
	hanamigin "github.com/heavycaffeiner/hanami/gin"
	"github.com/heavycaffeiner/hanami/health"
	hanamihttp "github.com/heavycaffeiner/hanami/http"
	"github.com/heavycaffeiner/hanami/observability"
	"go.uber.org/fx"
)

type applicationConfig struct {
	HTTP struct {
		Address           string        `mapstructure:"address"`
		ReadHeaderTimeout time.Duration `mapstructure:"read_header_timeout"`
		IdleTimeout       time.Duration `mapstructure:"idle_timeout"`
		MaxHeaderBytes    int           `mapstructure:"max_header_bytes"`
	} `mapstructure:"http"`
	OTel struct {
		Enabled        bool          `mapstructure:"enabled"`
		Endpoint       string        `mapstructure:"endpoint"`
		Insecure       bool          `mapstructure:"insecure"`
		ExportTimeout  time.Duration `mapstructure:"export_timeout"`
		MetricInterval time.Duration `mapstructure:"metric_interval"`
	} `mapstructure:"otel"`
}

func (cfg applicationConfig) Validate() error {
	server := hanamihttp.ServerConfig{
		Address:           cfg.HTTP.Address,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
		MaxHeaderBytes:    cfg.HTTP.MaxHeaderBytes,
	}
	if err := server.Validate(); err != nil {
		return err
	}
	if cfg.OTel.Enabled && cfg.OTel.Endpoint == "" {
		return errors.New("OTel endpoint is required when telemetry is enabled")
	}
	return nil
}

func main() {
	hanami.Main(hanami.Spec[applicationConfig]{
		Name:    "hanami-api-starter",
		Load:    loadConfig,
		Modules: applicationModules,
	}, hanami.WithTimeouts[applicationConfig](hanami.Timeouts{
		Startup:  15 * time.Second,
		Shutdown: 30 * time.Second,
	}))
}

func loadConfig(ctx context.Context) (applicationConfig, error) {
	return config.Load[applicationConfig](ctx, config.Source{
		File:      os.Getenv("HANAMI_API_CONFIG"),
		EnvPrefix: "HANAMI_API",
		Defaults: map[string]any{
			"http.address":             "127.0.0.1:8080",
			"http.read_header_timeout": "5s",
			"http.idle_timeout":        "60s",
			"http.max_header_bytes":    1048576,
			"otel.enabled":             false,
			"otel.export_timeout":      "5s",
			"otel.metric_interval":     "30s",
		},
	})
}

func applicationModules(cfg applicationConfig) fx.Option {
	options := []fx.Option{
		health.Module(),
		hanamigin.Module(hanamigin.Config{
			Mode: gin.ReleaseMode,
			Preset: &hanamigin.PresetConfig{
				ServiceName:     "hanami-api-starter",
				SecurityHeaders: true,
			},
		}),
		hanamihttp.Module(hanamihttp.ServerConfig{
			Address:           cfg.HTTP.Address,
			ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
			IdleTimeout:       cfg.HTTP.IdleTimeout,
			MaxHeaderBytes:    cfg.HTTP.MaxHeaderBytes,
		}),
		fx.Invoke(registerRoutes),
	}
	if cfg.OTel.Enabled {
		options = append(options, observability.Module(observability.Config{
			Enabled:        true,
			ServiceName:    "hanami-api-starter",
			Endpoint:       cfg.OTel.Endpoint,
			Insecure:       cfg.OTel.Insecure,
			ExportTimeout:  cfg.OTel.ExportTimeout,
			MetricInterval: cfg.OTel.MetricInterval,
		}))
	}
	return fx.Options(options...)
}

func registerRoutes(engine *gin.Engine, registry *health.Registry, logger *slog.Logger) {
	hanamigin.MountHealth(engine, registry)
	engine.GET("/v1/greeting", func(ctx *gin.Context) {
		logger.InfoContext(ctx.Request.Context(), "Greeting served")
		ctx.JSON(200, gin.H{"message": "hello from Hanami"})
	})
}
