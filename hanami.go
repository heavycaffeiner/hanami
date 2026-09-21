package hanami

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"github.com/heavycaffeiner/hanami/bootstrap"
	hanamilog "github.com/heavycaffeiner/hanami/log"
	"github.com/heavycaffeiner/hanami/process"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
)

type Spec[C any] struct {
	Name    string
	Load    func(context.Context) (C, error)
	Modules func(C) fx.Option
}

type Prepared struct {
	Cleanup func(context.Context) error
	Values  []any
}

type Integration[C any] interface {
	Name() string
	Prepare(context.Context, C) (Prepared, error)
}

type Option[C any] interface {
	apply(*runSettings[C]) error
}

type optionFunc[C any] func(*runSettings[C]) error

func (option optionFunc[C]) apply(settings *runSettings[C]) error {
	return option(settings)
}

type Timeouts struct {
	Startup  time.Duration
	Shutdown time.Duration
}

type runSettings[C any] struct {
	integrations []Integration[C]
	timeouts     Timeouts
	logger       *slog.Logger
}

func WithIntegration[C any](integration Integration[C]) Option[C] {
	return optionFunc[C](func(settings *runSettings[C]) error {
		if integration == nil {
			return errors.New("integration is nil")
		}
		settings.integrations = append(settings.integrations, integration)
		return nil
	})
}

func WithTimeouts[C any](timeouts Timeouts) Option[C] {
	return optionFunc[C](func(settings *runSettings[C]) error {
		if timeouts.Startup <= 0 {
			return errors.New("startup timeout must be positive")
		}
		if timeouts.Shutdown <= 0 {
			return errors.New("shutdown timeout must be positive")
		}
		settings.timeouts = timeouts
		return nil
	})
}

func WithLogger[C any](logger *slog.Logger) Option[C] {
	return optionFunc[C](func(settings *runSettings[C]) error {
		if logger == nil {
			return errors.New("logger is nil")
		}
		settings.logger = logger
		return nil
	})
}

type Result struct {
	Reason                  process.StopReason
	ExternalRestartRequired bool
}

func Run[C any](ctx context.Context, spec Spec[C], options ...Option[C]) (Result, error) {
	settings, err := resolveSettings(spec, options)
	if err != nil {
		return Result{Reason: process.StopReasonStartupFailure}, err
	}

	startupContext, cancelStartup := context.WithTimeout(ctx, settings.timeouts.Startup)
	defer cancelStartup()

	config, err := spec.Load(startupContext)
	if err != nil {
		return Result{Reason: process.StopReasonStartupFailure}, fmt.Errorf("load configuration: %w", err)
	}

	prepared, err := prepareIntegrations(startupContext, config, settings.integrations)
	if err != nil {
		cleanupErr := runCleanups(settings.timeouts.Shutdown, prepared.cleanups)
		return Result{Reason: process.StopReasonStartupFailure}, errors.Join(err, cleanupErr)
	}

	admission := bootstrap.NewAdmission()
	controller := process.NewController()
	var coordinator *bootstrap.Coordinator
	modules, err := buildModules(spec.Modules, config)
	if err != nil {
		cleanupErr := runCleanups(settings.timeouts.Shutdown, prepared.cleanups)
		return Result{Reason: process.StopReasonStartupFailure}, errors.Join(err, cleanupErr)
	}
	application := fx.New(
		fx.RecoverFromPanics(),
		fx.Supply(config, admission, controller, settings.logger),
		fx.Provide(func() context.Context { return startupContext }),
		fx.Supply(prepared.values...),
		fx.WithLogger(func(logger *slog.Logger) fxevent.Logger {
			return hanamilog.FxEventLogger(logger)
		}),
		bootstrap.Module(),
		modules,
		fx.Populate(&coordinator),
	)
	if err := application.Err(); err != nil {
		cleanupErr := runCleanups(settings.timeouts.Shutdown, prepared.cleanups)
		return Result{Reason: process.StopReasonStartupFailure}, errors.Join(fmt.Errorf("compose application: %w", err), cleanupErr)
	}

	if err := application.Start(startupContext); err != nil {
		stopErr := stopApplication(application, settings.timeouts.Shutdown)
		cleanupErr := runCleanups(settings.timeouts.Shutdown, prepared.cleanups)
		return Result{Reason: process.StopReasonStartupFailure}, errors.Join(fmt.Errorf("start application: %w", err), stopErr, cleanupErr)
	}
	if err := coordinator.Initialize(startupContext); err != nil {
		admission.Close()
		stopErr := stopApplication(application, settings.timeouts.Shutdown)
		cleanupErr := runCleanups(settings.timeouts.Shutdown, prepared.cleanups)
		return Result{Reason: process.StopReasonStartupFailure}, errors.Join(fmt.Errorf("initialize application: %w", err), stopErr, cleanupErr)
	}
	admission.Open()

	result := Result{}
	var runtimeErr error
	select {
	case <-ctx.Done():
		result.Reason = process.StopReasonContextCancelled
	case <-controller.Done():
		request := controller.Request()
		result.Reason = request.Reason
		result.ExternalRestartRequired = request.ExternalRestartRequired
		runtimeErr = request.Err
	}

	admission.Close()
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), settings.timeouts.Shutdown)
	defer cancelShutdown()
	stopErr := application.Stop(shutdownContext)
	cleanupErr := runCleanupsWithContext(shutdownContext, prepared.cleanups)
	return result, errors.Join(runtimeErr, stopErr, cleanupErr)
}

func Main[C any](spec Spec[C], options ...Option[C]) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	result, err := Run(ctx, spec, options...)
	stop()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%s: %v\n", spec.Name, err)
	}
	os.Exit(exitCode(result, err))
}

func resolveSettings[C any](spec Spec[C], options []Option[C]) (runSettings[C], error) {
	if spec.Name == "" {
		return runSettings[C]{}, errors.New("application name is empty")
	}
	if spec.Load == nil {
		return runSettings[C]{}, errors.New("configuration loader is nil")
	}
	if spec.Modules == nil {
		return runSettings[C]{}, errors.New("module factory is nil")
	}
	settings := runSettings[C]{
		timeouts: Timeouts{Startup: 15 * time.Second, Shutdown: 30 * time.Second},
		logger:   hanamilog.New(hanamilog.Config{ServiceName: spec.Name}),
	}
	for _, option := range options {
		if option == nil {
			return runSettings[C]{}, errors.New("run option is nil")
		}
		if err := option.apply(&settings); err != nil {
			return runSettings[C]{}, err
		}
	}
	names := make(map[string]struct{}, len(settings.integrations))
	for _, integration := range settings.integrations {
		name := integration.Name()
		if name == "" {
			return runSettings[C]{}, errors.New("integration name is empty")
		}
		if _, exists := names[name]; exists {
			return runSettings[C]{}, fmt.Errorf("integration %q is selected more than once", name)
		}
		names[name] = struct{}{}
	}
	return settings, nil
}

func buildModules[C any](factory func(C) fx.Option, config C) (modules fx.Option, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("compose modules: %v", recovered)
		}
	}()
	modules = factory(config)
	if modules == nil {
		return nil, errors.New("module factory returned nil")
	}
	return modules, nil
}

type preparedIntegrations struct {
	cleanups []func(context.Context) error
	values   []any
}

func prepareIntegrations[C any](ctx context.Context, config C, integrations []Integration[C]) (preparedIntegrations, error) {
	prepared := preparedIntegrations{
		cleanups: make([]func(context.Context) error, 0, len(integrations)),
		values:   make([]any, 0, len(integrations)),
	}
	for _, integration := range integrations {
		result, err := integration.Prepare(ctx, config)
		if err != nil {
			return prepared, fmt.Errorf("prepare integration %s: %w", integration.Name(), err)
		}
		if result.Cleanup != nil {
			prepared.cleanups = append(prepared.cleanups, result.Cleanup)
		}
		prepared.values = append(prepared.values, result.Values...)
	}
	return prepared, nil
}

func runCleanups(timeout time.Duration, cleanups []func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return runCleanupsWithContext(ctx, cleanups)
}

func runCleanupsWithContext(ctx context.Context, cleanups []func(context.Context) error) error {
	var result error
	for _, cleanup := range slices.Backward(cleanups) {
		if err := cleanup(ctx); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func stopApplication(application *fx.App, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return application.Stop(ctx)
}

func exitCode(result Result, err error) int {
	if err == nil && !result.ExternalRestartRequired {
		return 0
	}
	if result.Reason == process.StopReasonStartupFailure {
		return 2
	}
	return 1
}
