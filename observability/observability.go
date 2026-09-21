package observability

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	apitrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/fx"
)

type Config struct {
	Enabled        bool
	ServiceName    string
	ServiceVersion string
	Environment    string
	Endpoint       string
	Insecure       bool
	ExportTimeout  time.Duration
	MetricInterval time.Duration
}

type Providers struct {
	Tracer *trace.TracerProvider
	Meter  *metric.MeterProvider
}

type tracerProviderHolder struct {
	provider apitrace.TracerProvider
}

type dynamicTracerProvider struct {
	apitrace.TracerProvider
	current atomic.Pointer[tracerProviderHolder]
}

func newDynamicTracerProvider() *dynamicTracerProvider {
	provider := noop.NewTracerProvider()
	dynamic := &dynamicTracerProvider{TracerProvider: provider}
	dynamic.set(provider)
	return dynamic
}

func (provider *dynamicTracerProvider) set(current apitrace.TracerProvider) {
	provider.current.Store(&tracerProviderHolder{provider: current})
}

func (provider *dynamicTracerProvider) Tracer(name string, options ...apitrace.TracerOption) apitrace.Tracer {
	return provider.current.Load().provider.Tracer(name, options...)
}

type runtimeProviders struct {
	config Config
	tracer *dynamicTracerProvider
	active Providers
}

func Module(config Config) fx.Option {
	return fx.Module(
		"hanami-observability",
		fx.Provide(func(lifecycle fx.Lifecycle) (*runtimeProviders, error) {
			if err := validateConfig(config); err != nil {
				return nil, err
			}
			runtime := &runtimeProviders{config: config, tracer: newDynamicTracerProvider()}
			lifecycle.Append(fx.Hook{OnStart: runtime.Start, OnStop: runtime.Stop})
			return runtime, nil
		}),
		fx.Provide(func(runtime *runtimeProviders) apitrace.TracerProvider { return runtime.tracer }),
	)
}

func (runtime *runtimeProviders) Start(ctx context.Context) error {
	providers, err := New(ctx, runtime.config)
	if err != nil {
		return err
	}
	runtime.active = providers
	runtime.tracer.set(providers.Tracer)
	return nil
}

func (runtime *runtimeProviders) Stop(ctx context.Context) error {
	runtime.tracer.set(noop.NewTracerProvider())
	return runtime.active.Shutdown(ctx)
}

func New(ctx context.Context, config Config) (Providers, error) {
	if err := validateConfig(config); err != nil {
		return Providers{}, err
	}
	if config.ExportTimeout <= 0 {
		config.ExportTimeout = 5 * time.Second
	}
	if config.MetricInterval <= 0 {
		config.MetricInterval = 30 * time.Second
	}

	attributes := []resource.Option{resource.WithAttributes(
		semconv.ServiceName(config.ServiceName),
		semconv.ServiceVersion(config.ServiceVersion),
		semconv.DeploymentEnvironmentName(config.Environment),
	)}
	telemetryResource, err := resource.New(ctx, attributes...)
	if err != nil {
		return Providers{}, fmt.Errorf("create telemetry resource: %w", err)
	}

	traceOptions := []otlptracegrpc.Option{otlptracegrpc.WithTimeout(config.ExportTimeout)}
	metricOptions := []otlpmetricgrpc.Option{otlpmetricgrpc.WithTimeout(config.ExportTimeout)}
	if config.Endpoint != "" {
		traceOptions = append(traceOptions, otlptracegrpc.WithEndpoint(config.Endpoint))
		metricOptions = append(metricOptions, otlpmetricgrpc.WithEndpoint(config.Endpoint))
	}
	if config.Insecure {
		traceOptions = append(traceOptions, otlptracegrpc.WithInsecure())
		metricOptions = append(metricOptions, otlpmetricgrpc.WithInsecure())
	}

	traceExporter, err := otlptracegrpc.New(ctx, traceOptions...)
	if err != nil {
		return Providers{}, fmt.Errorf("create trace exporter: %w", err)
	}
	metricExporter, err := otlpmetricgrpc.New(ctx, metricOptions...)
	if err != nil {
		_ = traceExporter.Shutdown(ctx)
		return Providers{}, fmt.Errorf("create metric exporter: %w", err)
	}

	tracerProvider := trace.NewTracerProvider(
		trace.WithResource(telemetryResource),
		trace.WithBatcher(traceExporter),
	)
	meterProvider := metric.NewMeterProvider(
		metric.WithResource(telemetryResource),
		metric.WithReader(metric.NewPeriodicReader(metricExporter, metric.WithInterval(config.MetricInterval))),
	)
	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return Providers{Tracer: tracerProvider, Meter: meterProvider}, nil
}

func validateConfig(config Config) error {
	if !config.Enabled {
		return errors.New("observability module selected with telemetry disabled")
	}
	if config.ServiceName == "" {
		return errors.New("telemetry service name is empty")
	}
	return nil
}

func (providers Providers) Shutdown(ctx context.Context) error {
	var result error
	if providers.Meter != nil {
		result = errors.Join(result, providers.Meter.ForceFlush(ctx), providers.Meter.Shutdown(ctx))
	}
	if providers.Tracer != nil {
		result = errors.Join(result, providers.Tracer.ForceFlush(ctx), providers.Tracer.Shutdown(ctx))
	}
	return result
}
