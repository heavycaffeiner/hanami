package gin

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"

	gingonic "github.com/gin-gonic/gin"
	"github.com/heavycaffeiner/hanami/health"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/fx"
)

type Config struct {
	Mode       string
	Middleware []gingonic.HandlerFunc
	Preset     *PresetConfig
}

func Module(config Config) fx.Option {
	middleware := append([]gingonic.HandlerFunc(nil), config.Middleware...)
	var preset *PresetConfig
	if config.Preset != nil {
		value := *config.Preset
		preset = &value
	}
	return fx.Module(
		"hanami-gin",
		fx.Provide(func(params engineParams) (*gingonic.Engine, error) {
			if config.Mode != "" {
				gingonic.SetMode(config.Mode)
			}
			engine := gingonic.New()
			if preset != nil {
				engine.Use(MiddlewarePreset(PresetConfig{
					ServiceName:     preset.ServiceName,
					Logger:          params.Logger,
					TracerProvider:  params.TracerProvider,
					SecurityHeaders: preset.SecurityHeaders,
				})...)
			}
			engine.Use(middleware...)
			return engine, nil
		}),
		fx.Provide(func(engine *gingonic.Engine) http.Handler { return engine }),
	)
}

type engineParams struct {
	fx.In

	Logger         *slog.Logger
	TracerProvider trace.TracerProvider `optional:"true"`
}

type PresetConfig struct {
	ServiceName     string
	Logger          *slog.Logger
	TracerProvider  trace.TracerProvider
	SecurityHeaders bool
}

func MiddlewarePreset(config PresetConfig) []gingonic.HandlerFunc {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	middleware := []gingonic.HandlerFunc{
		gingonic.Recovery(),
		requestIdentity(),
	}
	if config.TracerProvider != nil {
		middleware = append(middleware, otelgin.Middleware(config.ServiceName, otelgin.WithTracerProvider(config.TracerProvider)))
	}
	middleware = append(middleware, accessLog(logger))
	if config.SecurityHeaders {
		middleware = append(middleware, securityHeaders())
	}
	return middleware
}

func MountHealth(engine *gingonic.Engine, registry *health.Registry) {
	engine.GET("/health/live", gingonic.WrapF(registry.Handler(health.Liveness, false)))
	engine.GET("/health/ready", gingonic.WrapF(registry.Handler(health.Readiness, false)))
}

func requestIdentity() gingonic.HandlerFunc {
	return func(ctx *gingonic.Context) {
		requestID := ctx.GetHeader("X-Request-ID")
		if requestID == "" {
			var value [16]byte
			if _, err := rand.Read(value[:]); err == nil {
				requestID = hex.EncodeToString(value[:])
			}
		}
		if requestID != "" {
			ctx.Header("X-Request-ID", requestID)
			ctx.Set("request_id", requestID)
		}
		ctx.Next()
	}
}

func accessLog(logger *slog.Logger) gingonic.HandlerFunc {
	return func(ctx *gingonic.Context) {
		started := time.Now()
		ctx.Next()
		logger.InfoContext(ctx.Request.Context(), "HTTP request",
			slog.String("request_id", ctx.GetString("request_id")),
			slog.String("route", ctx.FullPath()),
			slog.String("method", ctx.Request.Method),
			slog.Int("status", ctx.Writer.Status()),
			slog.Duration("duration", time.Since(started)),
		)
	}
}

func securityHeaders() gingonic.HandlerFunc {
	return func(ctx *gingonic.Context) {
		ctx.Header("X-Content-Type-Options", "nosniff")
		ctx.Header("X-Frame-Options", "DENY")
		ctx.Header("Referrer-Policy", "no-referrer")
		ctx.Next()
	}
}

var _ http.Handler = (*gingonic.Engine)(nil)
