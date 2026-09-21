package testkit

import (
	"context"

	"github.com/heavycaffeiner/hanami"
)

func Run[C any](ctx context.Context, spec hanami.Spec[C], options ...hanami.Option[C]) (hanami.Result, error) {
	return hanami.Run(ctx, spec, options...)
}
