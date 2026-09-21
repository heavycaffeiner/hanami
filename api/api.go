// Package api integrates Huma's typed API with the native Gin engine.
package api

import (
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	gingonic "github.com/gin-gonic/gin"
	"go.uber.org/fx"
)

// Config controls the Huma API exposed by Module.
//
// The Huma configuration is passed to Huma unchanged. Prefix, when set,
// mounts the API on a native Gin router group created from that path.
type Config struct {
	Huma   huma.Config
	Prefix string
}

// Module provides a native huma.API backed by the injected Gin engine.
//
// Routes registered through the returned API use Huma's normal typed request,
// response, validation, and OpenAPI behavior. The adapter is the only layer
// that knows about Gin; callers receive huma.API directly.
func Module(config Config) fx.Option {
	return fx.Module(
		"hanami-api",
		fx.Provide(func(engine *gingonic.Engine) huma.API {
			return New(engine, config.Huma, config.Prefix)
		}),
	)
}

// New creates a Huma API on engine. If prefix is non-empty, New creates a
// native Gin router group and mounts all Huma routes below it.
func New(engine *gingonic.Engine, config huma.Config, prefix string) huma.API {
	if prefix = normalizePrefix(prefix); prefix != "" {
		return humagin.NewWithGroup(engine, engine.Group(prefix), config)
	}
	return humagin.New(engine, config)
}

func normalizePrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" || prefix == "/" {
		return ""
	}
	if prefix[0] != '/' {
		prefix = "/" + prefix
	}
	return strings.TrimRight(prefix, "/")
}

var _ fx.Option = Module(Config{})
