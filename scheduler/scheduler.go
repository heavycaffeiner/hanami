// Package scheduler provides the optional gocron scheduler integration.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
	"github.com/heavycaffeiner/hanami/bootstrap"
	"github.com/heavycaffeiner/hanami/health"
	"go.uber.org/fx"
)

const JobGroup = "hanami_scheduler_jobs"

// Job registers one or more native gocron jobs with the scheduler. The
// registration function is deliberately native: applications retain access to
// every gocron job definition and option without a framework scheduling DSL.
type Job struct {
	Name     string
	Register func(gocron.Scheduler) error
}

// Config controls scheduler construction and lifecycle bounds.
type Config struct {
	SchedulerOptions []gocron.SchedulerOption
	ShutdownTimeout  time.Duration
	HealthTimeout    time.Duration
	HealthName       string
}

type Params struct {
	fx.In

	Jobs   []Job        `group:"hanami_scheduler_jobs"`
	Logger *slog.Logger `optional:"true"`
}

type runtime struct {
	config     Config
	jobs       []Job
	logger     *slog.Logger
	healthName string
	healthTime time.Duration
	monitor    *monitor
	ref        *schedulerRef
	started    atomic.Bool
	stopping   atomic.Bool
	stopOnce   sync.Once
}

// Module constructs a native gocron.Scheduler on lifecycle start, registers
// application jobs, and owns its context-bounded shutdown hook. Construction
// is intentionally deferred because gocron starts an internal goroutine from
// NewScheduler.
func Module(config Config) fx.Option {
	return fx.Module("hanami-scheduler",
		fx.Provide(func(lifecycle fx.Lifecycle, params Params) (*runtime, error) {
			return newRuntime(config, lifecycle, params)
		}),
		fx.Provide(func(value *runtime) gocron.Scheduler { return value.ref }),
		fx.Provide(fx.Annotate(
			func(value *runtime) bootstrap.Condition { return condition(value) },
			fx.ResultTags(`group:"hanami_startup_conditions"`),
		)),
		fx.Provide(fx.Annotate(
			func(value *runtime) health.Check {
				return health.Check{
					Name:    value.healthName,
					Kind:    health.Readiness,
					Timeout: value.healthTime,
					Run:     value.check,
				}
			},
			fx.ResultTags(`group:"hanami_health_checks"`),
		)),
	)
}

func newRuntime(config Config, lifecycle fx.Lifecycle, params Params) (*runtime, error) {
	if config.ShutdownTimeout < 0 {
		return nil, errors.New("scheduler shutdown timeout is negative")
	}
	if config.HealthTimeout < 0 {
		return nil, errors.New("scheduler health timeout is negative")
	}
	logger := params.Logger
	if logger == nil {
		logger = slog.Default()
	}
	name := config.HealthName
	if name == "" {
		name = "scheduler"
	}
	value := &runtime{
		config:     config,
		jobs:       slices.Clone(params.Jobs),
		logger:     logger,
		healthName: name,
		healthTime: config.HealthTimeout,
		monitor:    &monitor{logger: logger},
	}
	value.ref = &schedulerRef{runtime: value}
	lifecycle.Append(fx.Hook{OnStart: value.start, OnStop: value.stop})
	return value, nil
}

func condition(value *runtime) bootstrap.Condition {
	return bootstrap.Condition{Name: "scheduler", Run: value.initialize}
}

func (value *runtime) start(context.Context) error { return nil }
func (value *runtime) initialize(context.Context) error {
	if value.stopping.Load() {
		return errors.New("scheduler is stopping")
	}
	if err := value.constructAndRegister(); err != nil {
		value.monitor.fail(err)
		value.logger.Error("scheduler.start_failed", "error", err)
		return err
	}
	value.ref.start()
	value.started.Store(true)
	value.logger.Info("scheduler.start")
	return nil
}
func (value *runtime) constructAndRegister() error {
	options := slices.Clone(value.config.SchedulerOptions)
	options = append(options, gocron.WithSchedulerMonitor(value.monitor))
	native, err := gocron.NewScheduler(options...)
	if err != nil {
		return fmt.Errorf("construct scheduler: %w", err)
	}
	jobs := slices.Clone(value.jobs)
	slices.SortFunc(jobs, func(left, right Job) int { return compare(left.Name, right.Name) })
	seen := make(map[string]struct{}, len(jobs))
	for _, job := range jobs {
		if job.Name == "" {
			_ = native.Shutdown()
			return errors.New("scheduler job requires a name")
		}
		if job.Register == nil {
			_ = native.Shutdown()
			return fmt.Errorf("scheduler job %q has no registration function", job.Name)
		}
		if _, exists := seen[job.Name]; exists {
			_ = native.Shutdown()
			return fmt.Errorf("scheduler job %q is registered more than once", job.Name)
		}
		seen[job.Name] = struct{}{}
		if err := job.Register(native); err != nil {
			_ = native.Shutdown()
			return fmt.Errorf("register scheduler job %q: %w", job.Name, err)
		}
		value.monitor.setRequired(job.Name, true)
		value.logger.Info("scheduler.job_registered", "job", job.Name)
	}
	value.ref.set(native)
	return nil
}

func compare(left, right string) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func (value *runtime) stop(ctx context.Context) error {
	value.stopOnce.Do(func() { value.stopping.Store(true) })
	if !value.started.Load() {
		return nil
	}
	value.logger.Info("scheduler.stop")
	if value.config.ShutdownTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, value.config.ShutdownTimeout)
		defer cancel()
	}
	if err := value.ref.shutdown(ctx); err != nil {
		value.logger.Error("scheduler.stop_failed", "error", err)
		return err
	}
	return nil
}
func (value *runtime) check(context.Context) health.Result {
	if err := value.monitor.failureError(); err != nil {
		return health.Result{Status: health.StatusFail, Detail: "scheduler failed", Err: err}
	}
	if !value.started.Load() {
		return health.Result{Status: health.StatusFail, Detail: "scheduler is not started"}
	}
	if value.stopping.Load() {
		return health.Result{Status: health.StatusFail, Detail: "scheduler is stopping"}
	}
	return health.Result{Status: health.StatusPass, Detail: "scheduler is running"}
}

// schedulerRef exposes the native gocron interface while keeping scheduler
// construction behind the lifecycle boundary. Calls made before admission are
// rejected rather than silently creating or running a scheduler.
type schedulerRef struct {
	mu      sync.RWMutex
	runtime *runtime
	native  gocron.Scheduler
}

var errNotStarted = errors.New("scheduler has not started")

func (value *schedulerRef) set(native gocron.Scheduler) {
	value.mu.Lock()
	value.native = native
	value.mu.Unlock()
}
func (value *schedulerRef) get() (gocron.Scheduler, error) {
	value.mu.RLock()
	native := value.native
	value.mu.RUnlock()
	if native == nil {
		return nil, errNotStarted
	}
	return native, nil
}
func (value *schedulerRef) start() {
	if native, err := value.get(); err == nil {
		value.runtime.monitor.clearIntentionalStop()
		native.Start()
	}
}
func (value *schedulerRef) shutdown(ctx context.Context) error {
	native, err := value.get()
	if err != nil {
		return nil
	}
	value.runtime.monitor.markIntentionalStop()
	return native.ShutdownWithContext(ctx)
}
func (value *schedulerRef) Jobs() []gocron.Job {
	native, err := value.get()
	if err != nil {
		return nil
	}
	return native.Jobs()
}
func (value *schedulerRef) NewJob(d gocron.JobDefinition, t gocron.Task, o ...gocron.JobOption) (gocron.Job, error) {
	native, err := value.get()
	if err != nil {
		return nil, err
	}
	return native.NewJob(d, t, o...)
}
func (value *schedulerRef) RemoveByTags(tags ...string) {
	if native, err := value.get(); err == nil {
		native.RemoveByTags(tags...)
	}
}
func (value *schedulerRef) RemoveJob(id uuid.UUID) error {
	native, err := value.get()
	if err != nil {
		return err
	}
	return native.RemoveJob(id)
}
func (value *schedulerRef) Shutdown() error {
	native, err := value.get()
	if err != nil {
		return nil
	}
	value.runtime.monitor.markIntentionalStop()
	return native.Shutdown()
}
func (value *schedulerRef) ShutdownWithContext(ctx context.Context) error { return value.shutdown(ctx) }
func (value *schedulerRef) Start()                                        { value.start() }
func (value *schedulerRef) StopJobs() error {
	native, err := value.get()
	if err != nil {
		return err
	}
	value.runtime.monitor.markIntentionalStop()
	return native.StopJobs()
}
func (value *schedulerRef) StopJobsWithContext(ctx context.Context) error {
	native, err := value.get()
	if err != nil {
		return err
	}
	value.runtime.monitor.markIntentionalStop()
	return native.StopJobsWithContext(ctx)
}
func (value *schedulerRef) Update(id uuid.UUID, d gocron.JobDefinition, t gocron.Task, o ...gocron.JobOption) (gocron.Job, error) {
	native, err := value.get()
	if err != nil {
		return nil, err
	}
	return native.Update(id, d, t, o...)
}
func (value *schedulerRef) JobsWaitingInQueue() int {
	native, err := value.get()
	if err != nil {
		return 0
	}
	return native.JobsWaitingInQueue()
}

var _ gocron.Scheduler = (*schedulerRef)(nil)

type monitor struct {
	logger          *slog.Logger
	mu              sync.RWMutex
	required        map[string]bool
	failure         error
	intentionalStop atomic.Bool
}

func (value *monitor) setRequired(name string, required bool) {
	value.mu.Lock()
	defer value.mu.Unlock()
	if value.required == nil {
		value.required = make(map[string]bool)
	}
	value.required[name] = required
}
func (value *monitor) failureError() error {
	value.mu.RLock()
	defer value.mu.RUnlock()
	return value.failure
}
func (value *monitor) fail(err error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	if value.failure == nil {
		value.failure = err
	}
}
func (value *monitor) markIntentionalStop()  { value.intentionalStop.Store(true) }
func (value *monitor) clearIntentionalStop() { value.intentionalStop.Store(false) }
func (value *monitor) SchedulerStarted() {
	value.clearIntentionalStop()
	value.logger.Info("scheduler.started")
}
func (value *monitor) SchedulerStopped() {
	value.logger.Warn("scheduler.stopped")
	if !value.intentionalStop.Swap(false) {
		value.fail(errors.New("scheduler stopped unexpectedly"))
	}
}
func (value *monitor) SchedulerShutdown() { value.logger.Info("scheduler.shutdown") }
func (value *monitor) JobRegistered(job gocron.Job) {
	value.logger.Info("scheduler.job_registered", "job", job.Name())
}
func (value *monitor) JobUnregistered(job gocron.Job) {
	value.logger.Info("scheduler.job_unregistered", "job", job.Name())
}
func (value *monitor) JobStarted(job gocron.Job) {
	value.logger.Debug("scheduler.job_started", "job", job.Name())
}
func (value *monitor) JobRunning(job gocron.Job) {
	value.logger.Debug("scheduler.job_running", "job", job.Name())
}
func (value *monitor) JobFailed(job gocron.Job, err error) {
	value.logger.Error("scheduler.job_failed", "job", job.Name(), "error", err)
	value.mu.RLock()
	required := value.required[job.Name()]
	value.mu.RUnlock()
	if required {
		value.fail(fmt.Errorf("required scheduler job %q failed: %w", job.Name(), err))
	}
}
func (value *monitor) JobCompleted(job gocron.Job) {
	value.logger.Debug("scheduler.job_completed", "job", job.Name())
}
func (value *monitor) JobExecutionTime(job gocron.Job, duration time.Duration) {
	value.logger.Debug("scheduler.job_execution", "job", job.Name(), "duration", duration)
}
func (value *monitor) JobSchedulingDelay(job gocron.Job, scheduled, actual time.Time) {
	value.logger.Debug("scheduler.job_scheduling_delay", "job", job.Name(), "scheduled", scheduled, "actual", actual)
}
func (value *monitor) ConcurrencyLimitReached(limitType string, job gocron.Job) {
	value.logger.Warn("scheduler.job_concurrency_limit", "job", job.Name(), "limit", limitType)
}

var _ gocron.SchedulerMonitor = (*monitor)(nil)
