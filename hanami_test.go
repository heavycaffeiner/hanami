package hanami

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/heavycaffeiner/hanami/process"
	"go.uber.org/fx"
)

type orderedIntegration struct {
	prepared *bool
}

func (orderedIntegration) Name() string {
	return "ordered"
}

func (integration orderedIntegration) Prepare(context.Context, struct{}) (Prepared, error) {
	*integration.prepared = true
	return Prepared{}, nil
}

type preparedValue struct {
	Ready bool
}

type valueIntegration struct{}

func (valueIntegration) Name() string {
	return "value"
}

func (valueIntegration) Prepare(context.Context, struct{}) (Prepared, error) {
	return Prepared{Values: []any{preparedValue{Ready: true}}}, nil
}

func TestRunSuppliesPreparedIntegrationValues(t *testing.T) {
	result, err := Run(context.Background(), Spec[struct{}]{
		Name: "prepared-value-test",
		Load: func(context.Context) (struct{}, error) {
			return struct{}{}, nil
		},
		Modules: func(struct{}) fx.Option {
			return fx.Invoke(func(value preparedValue, controller *process.Controller) {
				if !value.Ready {
					t.Fatal("prepared integration value was not supplied")
				}
				controller.RequestStop(process.Request{Reason: process.StopReasonRequested})
			})
		},
	}, WithIntegration[struct{}](valueIntegration{}))
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}
	if result.Reason != process.StopReasonRequested {
		t.Fatalf("unexpected stop reason: %s", result.Reason)
	}
}

func TestRunPreparesIntegrationsBeforeModules(t *testing.T) {
	prepared := false
	moduleBuilt := false
	ctx, cancel := context.WithCancel(context.Background())

	result, err := Run(ctx, Spec[struct{}]{
		Name: "ordering-test",
		Load: func(context.Context) (struct{}, error) {
			return struct{}{}, nil
		},
		Modules: func(struct{}) fx.Option {
			if !prepared {
				t.Fatal("module factory ran before integration")
			}
			moduleBuilt = true
			return fx.Invoke(func(controller *process.Controller) {
				controller.RequestStop(process.Request{Reason: process.StopReasonRequested})
			})
		},
	}, WithIntegration[struct{}](orderedIntegration{prepared: &prepared}))
	cancel()

	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}
	if !moduleBuilt {
		t.Fatal("module factory did not run")
	}
	if result.Reason != process.StopReasonRequested {
		t.Fatalf("unexpected stop reason: %s", result.Reason)
	}
}

type cleanupIntegration struct {
	name    string
	events  *[]string
	failure error
	mu      *sync.Mutex
}

func (integration cleanupIntegration) Name() string {
	return integration.name
}

func (integration cleanupIntegration) Prepare(context.Context, struct{}) (Prepared, error) {
	if integration.failure != nil {
		return Prepared{}, integration.failure
	}
	return Prepared{Cleanup: func(context.Context) error {
		integration.mu.Lock()
		defer integration.mu.Unlock()
		*integration.events = append(*integration.events, integration.name)
		return nil
	}}, nil
}

func TestRunCleansPreparedIntegrationsInReverseOrder(t *testing.T) {
	var events []string
	var mu sync.Mutex
	expected := errors.New("refused")

	_, err := Run(context.Background(), Spec[struct{}]{
		Name: "cleanup-test",
		Load: func(context.Context) (struct{}, error) {
			return struct{}{}, nil
		},
		Modules: func(struct{}) fx.Option {
			t.Fatal("module factory ran after integration failure")
			return fx.Options()
		},
	},
		WithTimeouts[struct{}](Timeouts{Startup: time.Second, Shutdown: time.Second}),
		WithIntegration[struct{}](cleanupIntegration{name: "first", events: &events, mu: &mu}),
		WithIntegration[struct{}](cleanupIntegration{name: "second", events: &events, mu: &mu}),
		WithIntegration[struct{}](cleanupIntegration{name: "failure", failure: expected, events: &events, mu: &mu}),
	)

	if !errors.Is(err, expected) {
		t.Fatalf("expected integration error, got %v", err)
	}
	if len(events) != 2 || events[0] != "second" || events[1] != "first" {
		t.Fatalf("unexpected cleanup order: %v", events)
	}
}
