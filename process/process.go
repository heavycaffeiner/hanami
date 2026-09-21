package process

import (
	"errors"
	"sync"
)

type StopReason string

const (
	StopReasonContextCancelled StopReason = "context_cancelled"
	StopReasonRequested        StopReason = "requested"
	StopReasonRuntimeFailure   StopReason = "runtime_failure"
	StopReasonStartupFailure   StopReason = "startup_failure"
)

type Request struct {
	Reason                  StopReason
	Err                     error
	ExternalRestartRequired bool
}

type Controller struct {
	once sync.Once
	done chan struct{}

	mu      sync.RWMutex
	request Request
}

func NewController() *Controller {
	return &Controller{done: make(chan struct{})}
}

func (c *Controller) RequestStop(request Request) bool {
	accepted := false
	c.once.Do(func() {
		if request.Reason == "" {
			if request.Err != nil {
				request.Reason = StopReasonRuntimeFailure
			} else {
				request.Reason = StopReasonRequested
			}
		}
		c.mu.Lock()
		c.request = request
		c.mu.Unlock()
		close(c.done)
		accepted = true
	})
	return accepted
}

func (c *Controller) RequestExternalRestart(reason error) bool {
	if reason == nil {
		reason = errors.New("external restart required")
	}
	return c.RequestStop(Request{
		Reason:                  StopReasonRequested,
		Err:                     reason,
		ExternalRestartRequired: true,
	})
}

func (c *Controller) Done() <-chan struct{} {
	return c.done
}

func (c *Controller) Request() Request {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.request
}
