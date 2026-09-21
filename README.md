# Hanami

Hanami is a modular Go application framework for explicit startup, dependency injection, health, observability, graceful shutdown, optional Linux process security, and managed HTTP listener replacement.

It composes upstream libraries instead of hiding them. Applications use ordinary `context.Context`, `fx.Option`, `*gin.Engine`, `http.Handler`, `*http.Server`, `*slog.Logger`, `*sql.DB`, `*gorm.DB`, OpenTelemetry providers, Huma APIs, gocron schedulers, and Kong command grammars.

Hanami is used by [Stowcloud](https://github.com/heavycaffeiner/stowcloud), but the framework contains no Stowcloud domain types or product-specific modes.

## Status

Hanami currently implements:

- Generic `Spec`, `Run`, and `Main` application bootstrap
- Typed configuration loading and validation
- Uber Fx composition and lifecycle integration
- Structured logging with `log/slog`
- Liveness, readiness, and startup conditions
- Fixed native HTTP servers
- Managed HTTP generations with verified promotion and bounded draining
- Optional Linux Landlock and seccomp enforcement on amd64 and arm64
- OpenTelemetry providers and Gin instrumentation
- Huma typed APIs
- GORM database lifecycle with PostgreSQL, MySQL, and pure-Go SQLite dialects
- Goose migrations
- gocron scheduling
- Kong CLI integration

The public API is pre-v1. Breaking changes may occur before `v1.0.0`.

## Requirements

- Go 1.27.1 or later
- Linux for `security/linux`
- Linux amd64 or arm64 for the verified seccomp syscall mappings
- `CGO_ENABLED=0` is supported by every official module

## Core quickstart

Install the core module:

```sh
go get github.com/heavycaffeiner/hanami
```

Create an application definition:

```go
package main

import (
    "context"
    "log/slog"

    "github.com/heavycaffeiner/hanami"
    "github.com/heavycaffeiner/hanami/process"
    "go.uber.org/fx"
)

type Config struct {
    Message string
}

func loadConfig(context.Context) (Config, error) {
    return Config{Message: "hello from Hanami"}, nil
}

func modules(Config) fx.Option {
    return fx.Invoke(func(logger *slog.Logger, controller *process.Controller) {
        logger.Info("application started")
        controller.RequestStop(process.Request{Reason: process.StopReasonRequested})
    })
}

func main() {
    hanami.Main(hanami.Spec[Config]{
        Name:    "example",
        Load:    loadConfig,
        Modules: modules,
    })
}
```

`Load` runs before optional pre-composition integrations. `Modules` is not called until every selected integration succeeds. `Run` never calls `os.Exit`. `Main` adds the conventional signal adapter and process exit mapping.

## Execution model

```text
Load
  -> selected pre-composition integrations
  -> build one Fx graph
  -> start lifecycle hooks
  -> run required startup conditions
  -> open admission
  -> supervise runtime
  -> close admission
  -> drain and stop Fx
  -> release pre-composition resources
```

The shutdown signal does not directly cancel accepted requests or jobs. Fx stop hooks receive an independent bounded shutdown context.

## Run the starters

### API service

```sh
make run-api
```

Endpoints:

```text
GET /health/live
GET /health/ready
GET /v1/greeting
```

Expected response:

```json
{"message":"hello from Hanami"}
```

Configuration example: [`starters/api/config.yaml`](starters/api/config.yaml)

### Worker

```sh
make run-worker
```

The worker uses the same `Spec`, `Load`, and `Modules` contract without Gin or an HTTP listener.

Configuration example: [`starters/worker/config.yaml`](starters/worker/config.yaml)

### Platform reference application

The platform starter demonstrates the optional H4 modules together:

```sh
cd starters/platform
go run ./cmd/app migrate up
go run ./cmd/app migrate status
go run ./cmd/app serve
```

It includes:

- Kong commands: `serve`, `migrate up`, `migrate status`, `version`
- Pure-Go SQLite through GORM
- Embedded Goose migrations
- Native gocron scheduling
- Gin and Huma typed note endpoints
- Health endpoints

Configuration example: [`starters/platform/config.yaml`](starters/platform/config.yaml)

After starting it:

```sh
curl -X POST http://127.0.0.1:8080/v1/notes \
  -H 'Content-Type: application/json' \
  -d '{"title":"first note","content":"hello"}'

curl http://127.0.0.1:8080/v1/notes
```

### Linux secured service

```sh
HANAMI_SECURED_DIRECTORY=/tmp go run ./starters/secured/cmd/service
```

This example requests preferred Landlock and seccomp enforcement. It reports the verified status and continues with explicit degradation when the current environment cannot complete the process-wide Landlock re-exec. Change `ModePreferred` to `ModeRequired` in an application that must fail closed.

### Reconfigurable HTTP service

```sh
go run ./starters/reconfigurable/cmd/service
```

To demonstrate a live listener transition:

```sh
HANAMI_RECONFIGURABLE_ADDRESS=127.0.0.1:8082 \
HANAMI_RECONFIGURABLE_NEXT_ADDRESS=127.0.0.1:8083 \
go run ./starters/reconfigurable/cmd/service
```

The new generation is bound, privately probed, and promoted before the old generation drains.

## Optional modules

Hanami keeps optional integrations in nested Go modules. Install only what the application uses.

| Module | Import path | Upstream API exposed |
|---|---|---|
| Typed configuration | `github.com/heavycaffeiner/hanami/config` | Viper-backed typed values |
| Gin | `github.com/heavycaffeiner/hanami/gin` | `*gin.Engine`, `gin.HandlerFunc` |
| Observability | `github.com/heavycaffeiner/hanami/observability` | OpenTelemetry providers |
| Huma | `github.com/heavycaffeiner/hanami/api` | `huma.API` |
| Database | `github.com/heavycaffeiner/hanami/database` | `*gorm.DB`, `*sql.DB` |
| Migrations | `github.com/heavycaffeiner/hanami/migration` | `*goose.Provider` |
| Scheduler | `github.com/heavycaffeiner/hanami/scheduler` | `gocron.Scheduler` |
| CLI | `github.com/heavycaffeiner/hanami/cli` | `*kong.Kong`, `*kong.Context` |

The root module depends directly only on Fx, `gofrs/flock`, and `x/sys`. A core or non-HTTP consumer does not pull Gin, Viper, OpenTelemetry, Huma, GORM, Goose, gocron, or Kong into its module graph.

## Typed configuration

```go
cfg, err := config.Load[Config](ctx, config.Source{
    File:      "config.yaml",
    EnvPrefix: "MYAPP",
    Defaults: map[string]any{
        "http.address": "127.0.0.1:8080",
    },
})
```

Precedence from low to high:

```text
framework defaults
application defaults
configuration file
environment variables
explicit overrides
```

An explicitly configured but unreadable file returns an error. It does not silently fall back to defaults.

## Fixed HTTP server

The fixed server module consumes a native `http.Handler` and exposes a native `*http.Server`.

```go
fx.Options(
    health.Module(),
    hanamigin.Module(hanamigin.Config{Mode: gin.ReleaseMode}),
    hanamihttp.Module(hanamihttp.ServerConfig{
        Address:           "127.0.0.1:8080",
        ReadHeaderTimeout: 5 * time.Second,
        IdleTimeout:       60 * time.Second,
    }),
)
```

Hanami does not set a universal short write timeout because streaming and long-lived responses require application-specific policy.

## Managed HTTP generations

Use `http.ManagedModule` for live listener replacement or construct `http.Manager` directly.

```go
result, err := manager.Replace(ctx, hanamihttp.ReplaceRequest{
    Server: hanamihttp.ServerConfig{
        Address:                "127.0.0.1:8081",
        Protocol:               hanamihttp.ProtocolHTTP,
        Handler:                handler,
        ProbePath:              "/.hanami/ready",
        ProbeIdentity:          "example",
        ProbeTimeout:           3 * time.Second,
        DrainTimeout:           10 * time.Second,
        MaxDrainingGenerations: 2,
    },
})
```

Possible states:

```text
rejected
applied
applied_with_drain_failure
```

Promotion is the linearization point. A drain failure after promotion does not claim that the old generation stayed active. TLS probes use normal certificate verification and never use `InsecureSkipVerify`.

Custom HTTP runtimes can implement:

```go
type Runtime interface {
    Serve(net.Listener) error
    Shutdown(context.Context) error
    Close() error
}
```

The runtime factory receives `RuntimeControl`, which provides the candidate admission state and private probe contract without importing a particular web framework.

## Linux process security

Select security before module construction:

```go
hanami.Main(spec,
    securitylinux.WithPolicy(func(cfg Config) (securitylinux.Policy, error) {
        return securitylinux.Policy{
            Mode:          securitylinux.ModeRequired,
            WritablePaths: []string{cfg.DataDir},
            ExceptExec:    true,
            Landlock:      true,
            Seccomp:       true,
            DenySyscalls:  []string{"ptrace", "mount", "bpf"},
        }, nil
    }),
)
```

Security properties:

- Landlock is applied on a locked OS thread and inherited through `exec`
- The new image verifies a denied path before claiming Landlock enforcement
- seccomp uses `SECCOMP_FILTER_FLAG_TSYNC`
- Unexpected syscall ABIs are killed
- amd64 x32 syscalls are rejected
- Unlisted descriptors are marked close-on-exec before handoff
- Required mode fails closed
- Preferred mode reports every missing protection
- Permission expansion is classified as requiring an external restart

A handoff environment value is routing metadata, not proof by itself. Verification also checks kernel-observed enforcement behavior.

## Database and migrations

Database modules expose upstream handles directly:

```go
fx.Options(
    health.Module(),
    sqlite.Module(sqlite.Config{
        DSN:         "file:app.db",
        WAL:         true,
        ForeignKeys: true,
        Pool: database.PoolConfig{
            MaxOpenConns: 1,
        },
    }),
    migration.Module(migration.Config{
        Policy:  migration.MigrateBeforeStart,
        Dialect: goose.DialectSQLite3,
        FS:      migrations,
    }),
)
```

The migration module reuses the same `*sql.DB`. It never closes a pool owned by the database module. Production schema changes use explicit Goose migrations, not GORM AutoMigrate.

## Scheduler

Applications register jobs through an unordered Fx value group. Registration order is normalized by job name and does not determine scheduling semantics.

```go
func cleanupJob() scheduler.Job {
    return scheduler.Job{
        Name: "cleanup",
        Register: func(native gocron.Scheduler) error {
            _, err := native.NewJob(
                gocron.DurationJob(time.Hour),
                gocron.NewTask(func(ctx context.Context) {
                    // Application work.
                }),
                gocron.WithName("cleanup"),
            )
            return err
        },
    }
}
```

Scheduler construction, job registration, and start run as a required startup condition. Registration failure blocks application admission.

## Repository layout

```text
bootstrap/       startup conditions and admission
process/         stop and external restart outcomes
health/          health aggregation
http/            fixed and managed native HTTP lifecycle
security/linux/  Landlock and seccomp integration
ownership/       exclusive resource ownership
log/             slog and Fx event integration
config/          typed configuration module
observability/   OpenTelemetry module
gin/             Gin module
api/             Huma module
database/        GORM and dialect modules
migration/       Goose module
scheduler/       gocron module
cli/             Kong module
starters/        runnable applications
```

## Verification

Run the full local matrix:

```sh
make test
make race
make vet
make test-cgo
make build
```

CI runs those checks independently for the root and every nested module.

## Design documents

- [Framework specification](docs/hanami-framework-specification-20260921.md)
- [Architecture design](docs/hanami-architecture-design-20260920.md)

## License

Hanami is available under the [MIT License](LICENSE).
