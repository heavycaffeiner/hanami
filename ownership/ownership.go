package ownership

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/heavycaffeiner/hanami"
)

type Requirement struct {
	LockPath      string
	RetryInterval time.Duration
}

type integration[C any] struct {
	build func(C) (Requirement, error)
}

func WithRequirement[C any](build func(C) (Requirement, error)) hanami.Option[C] {
	return hanami.WithIntegration[C](integration[C]{build: build})
}

func (integration[C]) Name() string {
	return "ownership"
}

func (i integration[C]) Prepare(ctx context.Context, config C) (hanami.Prepared, error) {
	if i.build == nil {
		return hanami.Prepared{}, errors.New("ownership requirement builder is nil")
	}
	requirement, err := i.build(config)
	if err != nil {
		return hanami.Prepared{}, fmt.Errorf("build ownership requirement: %w", err)
	}
	if requirement.LockPath == "" {
		return hanami.Prepared{}, errors.New("ownership lock path is empty")
	}
	if !filepath.IsAbs(requirement.LockPath) {
		return hanami.Prepared{}, errors.New("ownership lock path must be absolute")
	}
	if requirement.RetryInterval <= 0 {
		requirement.RetryInterval = 100 * time.Millisecond
	}
	parent := filepath.Dir(requirement.LockPath)
	info, err := os.Stat(parent)
	if err != nil {
		return hanami.Prepared{}, fmt.Errorf("inspect ownership lock directory: %w", err)
	}
	if !info.IsDir() {
		return hanami.Prepared{}, errors.New("ownership lock parent is not a directory")
	}

	lock := flock.New(requirement.LockPath)
	locked, err := lock.TryLockContext(ctx, requirement.RetryInterval)
	if err != nil {
		return hanami.Prepared{}, fmt.Errorf("acquire ownership lock: %w", err)
	}
	if !locked {
		return hanami.Prepared{}, errors.New("ownership lock was not acquired")
	}
	return hanami.Prepared{Cleanup: func(context.Context) error {
		if err := lock.Close(); err != nil {
			return fmt.Errorf("release ownership lock: %w", err)
		}
		return nil
	}}, nil
}
