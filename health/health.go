package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/heavycaffeiner/hanami/bootstrap"
	"go.uber.org/fx"
)

const DefaultTimeout = 5 * time.Second

type Kind uint8

const (
	Liveness Kind = iota + 1
	Readiness
)

type Status uint8

const (
	StatusPass Status = iota + 1
	StatusWarn
	StatusFail
)

const CheckGroup = "hanami_health_checks"

type Check struct {
	Name    string
	Kind    Kind
	Timeout time.Duration
	Run     func(context.Context) Result
}

type Result struct {
	Status Status
	Detail string
	Err    error
}

type CheckResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type Report struct {
	Version string        `json:"version"`
	Status  string        `json:"status"`
	Checks  []CheckResult `json:"checks,omitempty"`
}

type Params struct {
	fx.In

	Checks []Check `group:"hanami_health_checks"`
}

type Registry struct {
	checks    []Check
	admission *bootstrap.Admission
}

func New(params Params, admission *bootstrap.Admission) (*Registry, error) {
	checks := slices.Clone(params.Checks)
	names := make(map[string]struct{}, len(checks))
	for _, check := range checks {
		if check.Name == "" || check.Run == nil {
			return nil, errors.New("health check requires a name and runner")
		}
		if check.Kind != Liveness && check.Kind != Readiness {
			return nil, errors.New("health check has an invalid kind")
		}
		if check.Timeout < 0 {
			return nil, errors.New("health check timeout is negative")
		}
		key := fmt.Sprintf("%d:%s", check.Kind, check.Name)
		if _, exists := names[key]; exists {
			return nil, errors.New("health check is registered more than once")
		}
		names[key] = struct{}{}
	}
	slices.SortFunc(checks, func(left, right Check) int {
		if left.Name < right.Name {
			return -1
		}
		if left.Name > right.Name {
			return 1
		}
		return int(left.Kind) - int(right.Kind)
	})
	return &Registry{checks: checks, admission: admission}, nil
}

func Module() fx.Option {
	return fx.Module("hanami-health", fx.Provide(New))
}

func (registry *Registry) Check(ctx context.Context, kind Kind, revealDetails bool) Report {
	selected := make([]Check, 0, len(registry.checks))
	for _, check := range registry.checks {
		if check.Kind == kind {
			selected = append(selected, check)
		}
	}
	type outcome struct {
		index  int
		result Result
	}
	outcomes := make(chan outcome, len(selected))
	for index, check := range selected {
		go func(index int, check Check) {
			timeout := check.Timeout
			if timeout == 0 {
				timeout = DefaultTimeout
			}
			checkContext, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			result := make(chan Result, 1)
			go func() {
				result <- check.Run(checkContext)
			}()
			select {
			case value := <-result:
				outcomes <- outcome{index: index, result: value}
			case <-checkContext.Done():
				outcomes <- outcome{index: index, result: Result{Status: StatusFail, Err: checkContext.Err()}}
			}
		}(index, check)
	}

	results := make([]CheckResult, len(selected))
	overall := StatusPass
	for range selected {
		outcome := <-outcomes
		result := outcome.result
		if result.Status == 0 {
			if result.Err != nil {
				result.Status = StatusFail
			} else {
				result.Status = StatusPass
			}
		}
		if result.Status > overall {
			overall = result.Status
		}
		detail := ""
		if revealDetails {
			detail = result.Detail
			if detail == "" && result.Err != nil {
				detail = result.Err.Error()
			}
		}
		results[outcome.index] = CheckResult{Name: selected[outcome.index].Name, Status: statusName(result.Status), Detail: detail}
	}
	if kind == Readiness && !registry.admission.IsOpen() {
		overall = StatusFail
	}
	return Report{Version: "v1", Status: statusName(overall), Checks: results}
}

func (registry *Registry) Handler(kind Kind, revealDetails bool) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		report := registry.Check(request.Context(), kind, revealDetails)
		status := http.StatusOK
		if report.Status == "fail" {
			status = http.StatusServiceUnavailable
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(status)
		_ = json.NewEncoder(writer).Encode(report)
	}
}

func statusName(status Status) string {
	switch status {
	case StatusPass:
		return "pass"
	case StatusWarn:
		return "warn"
	default:
		return "fail"
	}
}
