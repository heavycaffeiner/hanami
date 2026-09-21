package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/heavycaffeiner/hanami"
	"github.com/heavycaffeiner/hanami/bootstrap"
	"github.com/heavycaffeiner/hanami/config"
	"github.com/heavycaffeiner/hanami/process"
	"go.uber.org/fx"
)

type applicationConfig struct {
	Work struct {
		Delay time.Duration `mapstructure:"delay"`
	} `mapstructure:"work"`
}

func (cfg applicationConfig) Validate() error {
	if cfg.Work.Delay <= 0 {
		return errors.New("work delay must be positive")
	}
	return nil
}

func main() {
	hanami.Main(hanami.Spec[applicationConfig]{
		Name:    "hanami-worker-starter",
		Load:    loadConfig,
		Modules: applicationModules,
	}, hanami.WithTimeouts[applicationConfig](hanami.Timeouts{
		Startup:  10 * time.Second,
		Shutdown: 10 * time.Second,
	}))
}

func loadConfig(ctx context.Context) (applicationConfig, error) {
	return config.Load[applicationConfig](ctx, config.Source{
		File:      os.Getenv("HANAMI_WORKER_CONFIG"),
		EnvPrefix: "HANAMI_WORKER",
		Defaults:  map[string]any{"work.delay": "250ms"},
	})
}

func applicationModules(cfg applicationConfig) fx.Option {
	return fx.Module(
		"worker",
		fx.Provide(newWorker),
		fx.Invoke(registerWorker),
	)
}

type worker struct {
	delay      time.Duration
	admission  *bootstrap.Admission
	controller *process.Controller
	logger     *slog.Logger

	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func newWorker(cfg applicationConfig, admission *bootstrap.Admission, controller *process.Controller, logger *slog.Logger) *worker {
	return &worker{
		delay:      cfg.Work.Delay,
		admission:  admission,
		controller: controller,
		logger:     logger,
		done:       make(chan struct{}),
	}
}

func registerWorker(lifecycle fx.Lifecycle, worker *worker) {
	lifecycle.Append(fx.Hook{OnStart: worker.Start, OnStop: worker.Stop})
}

func (worker *worker) Start(context.Context) error {
	ctx, cancel := context.WithCancel(context.Background())
	worker.cancel = cancel
	go worker.run(ctx)
	return nil
}

func (worker *worker) run(ctx context.Context) {
	defer close(worker.done)
	for !worker.admission.IsOpen() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(worker.delay):
		worker.logger.InfoContext(ctx, "Work completed")
		worker.controller.RequestStop(process.Request{Reason: process.StopReasonRequested})
	}
}

func (worker *worker) Stop(ctx context.Context) error {
	worker.once.Do(func() {
		if worker.cancel != nil {
			worker.cancel()
		}
	})
	select {
	case <-worker.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
