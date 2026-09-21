package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"go.uber.org/fx"
)

const StartupConditionGroup = "hanami_startup_conditions"

type Condition struct {
	Name    string
	Timeout time.Duration
	Run     func(context.Context) error
}

type conditionParams struct {
	fx.In

	Conditions []Condition `group:"hanami_startup_conditions"`
}

type Coordinator struct {
	conditions []Condition

	mu          sync.Mutex
	initialized bool
	result      error
}

func newCoordinator(params conditionParams) (*Coordinator, error) {
	conditions := slices.Clone(params.Conditions)
	names := make(map[string]struct{}, len(conditions))
	for index := range conditions {
		condition := &conditions[index]
		if condition.Name == "" {
			return nil, errors.New("startup condition name is empty")
		}
		if condition.Run == nil {
			return nil, fmt.Errorf("startup condition %q has no runner", condition.Name)
		}
		if condition.Timeout < 0 {
			return nil, fmt.Errorf("startup condition %q has a negative timeout", condition.Name)
		}
		if _, exists := names[condition.Name]; exists {
			return nil, fmt.Errorf("startup condition %q is registered more than once", condition.Name)
		}
		names[condition.Name] = struct{}{}
	}
	slices.SortFunc(conditions, func(left, right Condition) int {
		if left.Name < right.Name {
			return -1
		}
		if left.Name > right.Name {
			return 1
		}
		return 0
	})
	return &Coordinator{conditions: conditions}, nil
}

func Module() fx.Option {
	return fx.Module("hanami-bootstrap", fx.Provide(newCoordinator))
}

func (c *Coordinator) Initialize(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.initialized {
		return c.result
	}
	c.initialized = true

	type outcome struct {
		name string
		err  error
	}
	outcomes := make(chan outcome, len(c.conditions))
	for _, condition := range c.conditions {
		go func(condition Condition) {
			conditionContext := ctx
			cancel := func() {}
			if condition.Timeout > 0 {
				conditionContext, cancel = context.WithTimeout(ctx, condition.Timeout)
			}
			defer cancel()
			result := make(chan error, 1)
			go func() {
				result <- condition.Run(conditionContext)
			}()
			select {
			case err := <-result:
				outcomes <- outcome{name: condition.Name, err: err}
			case <-conditionContext.Done():
				outcomes <- outcome{name: condition.Name, err: conditionContext.Err()}
			}
		}(condition)
	}

	var result error
	for range c.conditions {
		outcome := <-outcomes
		if outcome.err != nil {
			result = errors.Join(result, fmt.Errorf("startup condition %s: %w", outcome.name, outcome.err))
		}
	}
	c.result = result
	return result
}
