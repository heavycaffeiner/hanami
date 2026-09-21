package scheduler

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/heavycaffeiner/hanami/bootstrap"
	"go.uber.org/fx"
)

func TestJobsWaitForAdmissionAndRunAfterStart(t *testing.T) {
	var runs atomic.Int64
	admission := bootstrap.NewAdmission()
	var native gocron.Scheduler
	app, coordinator := newTestApp(admission, []Job{{Name: "run", Register: func(s gocron.Scheduler) error {
		_, err := s.NewJob(gocron.DurationJob(10*time.Millisecond), gocron.NewTask(func() { runs.Add(1) }), gocron.WithName("run"))
		return err
	}}}, &native)
	startApp(t, app, coordinator, admission)
	if got := runs.Load(); got != 0 {
		t.Fatalf("job ran before admission opened: %d", got)
	}
	waitFor(t, func() bool { return runs.Load() > 0 })
	waitFor(t, func() bool { return runs.Load() > 0 })
	stopApp(t, app)
}

func TestJobsRegisterIndependentlyOfGroupOrder(t *testing.T) {
	admission := bootstrap.NewAdmission()
	var native gocron.Scheduler
	app, coordinator := newTestApp(admission, []Job{
		{Name: "zeta", Register: registerNamed("zeta")},
		{Name: "alpha", Register: registerNamed("alpha")},
	}, &native)
	startApp(t, app, coordinator, admission)
	waitFor(t, func() bool { return len(native.Jobs()) == 2 })
	jobs := native.Jobs()
	if jobs[0].Name() != "alpha" && jobs[1].Name() != "alpha" {
		t.Fatalf("alpha job missing: %#v", jobs)
	}
	if jobs[0].Name() != "zeta" && jobs[1].Name() != "zeta" {
		t.Fatalf("zeta job missing: %#v", jobs)
	}
	stopApp(t, app)
}

func TestJobReceivesCancellationOnShutdown(t *testing.T) {
	admission := bootstrap.NewAdmission()
	started := make(chan struct{})
	cancelled := make(chan struct{})
	var native gocron.Scheduler
	app, coordinator := newTestApp(admission, []Job{{Name: "cancel", Register: func(s gocron.Scheduler) error {
		_, err := s.NewJob(gocron.DurationJob(5*time.Millisecond), gocron.NewTask(func(ctx context.Context) {
			select {
			case <-started:
			default:
				close(started)
			}
			<-ctx.Done()
			close(cancelled)
		}), gocron.WithName("cancel"))
		return err
	}}}, &native)
	startApp(t, app, coordinator, admission)
	waitChannel(t, started)
	stopApp(t, app)
	waitChannel(t, cancelled)
}

func TestShutdownWaitIsBounded(t *testing.T) {
	admission := bootstrap.NewAdmission()
	started := make(chan struct{})
	var native gocron.Scheduler
	app, coordinator := newTestAppWithConfig(admission, Config{ShutdownTimeout: 25 * time.Millisecond}, []Job{{Name: "blocked", Register: func(s gocron.Scheduler) error {
		_, err := s.NewJob(gocron.DurationJob(5*time.Millisecond), gocron.NewTask(func(context.Context) {
			select {
			case <-started:
			default:
				close(started)
			}
			select {}
		}), gocron.WithName("blocked"))
		return err
	}}}, &native)
	startApp(t, app, coordinator, admission)
	waitChannel(t, started)
	begin := time.Now()
	err := app.Stop(context.Background())
	elapsed := time.Since(begin)
	if elapsed > time.Second {
		t.Fatalf("shutdown was not bounded: %s", elapsed)
	}
	if err == nil {
		t.Fatal("expected bounded shutdown error")
	}
}

func registerNamed(name string) func(gocron.Scheduler) error {
	return func(s gocron.Scheduler) error {
		_, err := s.NewJob(gocron.DurationJob(time.Hour), gocron.NewTask(func() {}), gocron.WithName(name))
		return err
	}
}

func newTestApp(admission *bootstrap.Admission, jobs []Job, native *gocron.Scheduler) (*fx.App, *bootstrap.Coordinator) {
	return newTestAppWithConfig(admission, Config{}, jobs, native)
}

func newTestAppWithConfig(admission *bootstrap.Admission, config Config, jobs []Job, native *gocron.Scheduler) (*fx.App, *bootstrap.Coordinator) {
	var coordinator *bootstrap.Coordinator
	options := []fx.Option{fx.Supply(admission), Module(config), bootstrap.Module(), fx.Populate(native), fx.Populate(&coordinator)}
	for _, job := range jobs {
		job := job
		options = append(options, fx.Provide(fx.Annotate(func() Job { return job }, fx.ResultTags(`group:"hanami_scheduler_jobs"`))))
	}
	return fx.New(options...), coordinator
}

func startApp(t *testing.T, app *fx.App, coordinator *bootstrap.Coordinator, admission *bootstrap.Admission) {
	t.Helper()
	if err := app.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	admission.Open()
}

func stopApp(t *testing.T, app *fx.App) {
	t.Helper()
	if err := app.Stop(context.Background()); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition did not become true")
}

func waitChannel(t *testing.T, channel <-chan struct{}) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(time.Second):
		t.Fatal("channel was not signaled")
	}
}
