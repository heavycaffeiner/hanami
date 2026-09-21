# Hanami : Reusable Go Backend Framework Architecture Design

Status: H0 through H4 implemented, including Stowcloud adoption and demand-driven integration modules.
Date: 2026-09-20.
Repository: https://github.com/heavycaffeiner/hanami.
Scope: Hanami reusable Go backend framework architecture, declarative bootstrap, optional process security, managed HTTP generations, dependency stack, persistence, configuration, observability, health, scheduling, CLI, CGO policy, testing, release policy, and Stowcloud adoption.

Normative execution contract: [Hanami Framework Specification](hanami-framework-specification-20260921.md). This architecture supplies integration rationale; the specification owns bootstrap, security enforcement, server transitions and starter acceptance.

The framework's official project name is **Hanami**. This document defines Hanami as a reusable backend framework intended for Stowcloud and future Go projects. It is not a Stowcloud-specific runtime extraction. Stowcloud is the most demanding first consumer and dogfooding target, but framework public APIs must not contain Stowcloud concepts, storage models, product-specific filesystem assumptions, protocol semantics, or product-specific security-policy types. Generic access requirements and lifecycle mechanisms belong in Hanami; Stowcloud maps its domain into those requirements.

The central design principle is:

> Build the framework by composing mature libraries. Own conventions, integration, lifecycle policy, and developer experience. Do not reimplement solved infrastructure.

The intended result is an opinionated, modular, observable, CGO-free Go application framework built around Uber Fx and Gin, with Spring-like application structure without Java-style reflection scanning, annotations, proxy AOP, or hidden runtime magic. Hanami remains deliberately thin: it standardizes production wiring while exposing upstream Fx, Gin, net/http, slog, GORM, and OpenTelemetry types directly.

---

# 1. Executive decision

The baseline technology stack is:

| Concern | Decision | Role |
|---|---|---|
| Go | Go 1.27.1 baseline | Language and standard library |
| Dependency injection and lifecycle | Uber Fx v1.24.x | Constructor injection, module graph, startup/shutdown hooks, value groups |
| HTTP | Gin v1.12.x over net/http | Primary HTTP router and middleware runtime |
| Typed REST contract | Huma v2.39.x, optional official module | Typed request/response binding, validation, OpenAPI 3.1, JSON Schema, Problem Details |
| Configuration | Viper v1.21.x | File, environment, defaults, explicit overrides, watching where explicitly enabled |
| ORM | GORM v1.31.x | Primary object-relational mapper |
| Database migration | Goose v3.28.x | Explicit versioned schema migrations |
| Logging | log/slog | Structured application logging |
| Tracing and metrics | OpenTelemetry Go v1.46.x stable line | Framework-wide telemetry backbone |
| Gin telemetry | OpenTelemetry otelgin v0.71.x | Gin request instrumentation |
| Health | Tiny framework-owned primitive | Small readiness/liveness registry and aggregation only |
| Scheduler | gocron v2.22.x, optional official module | Scheduled jobs |
| CLI | Kong v1.16.x, optional official module | Declarative command tree |
| PostgreSQL | GORM PostgreSQL driver over pgx v5 | Preferred server database |
| MySQL | GORM MySQL driver over go-sql-driver/mysql | Supported server database |
| SQLite | Pure-Go GORM dialect based on modernc.org/sqlite, optional | Embedded database without CGO |

These choices define the target integrations. Section 41 stages the bootstrap core, independent starters, optional Linux security and managed HTTP generations, then Stowcloud adoption. Being listed here does not commit every integration to the first release.

The following architectural decisions apply:

1. Uber Fx is the framework runtime and dependency-injection foundation.
2. Gin is the official HTTP router/middleware runtime; net/http remains the transport foundation.
3. Health remains a tiny owned primitive rather than adding an inactive or weakly maintained health framework.
4. CGO-free compilation is a framework guarantee for all official modules.
5. GORM is ORM-first persistence. The framework does not create another generic repository ORM layer.
6. Goose owns production migrations. GORM AutoMigrate is development convenience only.
7. OpenTelemetry owns tracing and metrics. The framework does not invent a telemetry API.
8. Viper is the configuration engine, but raw global Viper access is not the default application programming model.
9. Huma is an official optional REST-contract module through its Gin adapter, not a mandatory part of Gin itself.
10. Optional integrations must not be imported or started by core modules. Build dependency isolation and Go module graph isolation are separate guarantees, measured as described in Section 27.
11. Hanami core owns the shared bootstrap from typed configuration loading through selected pre-composition integrations, Fx construction, admission and shutdown.
12. Linux security and HTTP generation management are opt-in execution modules. Unselected modules add no runtime workers or unrelated dependencies, and applications retain their product policy.
13. Stowcloud is a full bootstrap consumer. Hanami owns its common security, signal, shutdown, and listener-generation execution paths.

---

# 2. Goals

The framework must make a new production backend cheap to start without making long-lived applications dependent on framework-specific domain abstractions.

Primary goals:

1. Provide constructor-based dependency injection with explicit dependency graphs.
2. Provide deterministic application startup and reverse-order graceful shutdown.
3. Provide production-safe HTTP defaults on Gin and net/http.
4. Provide typed configuration loaded from conventional sources.
5. Provide ORM-first persistence with explicit schema migration.
6. Provide logs, traces, metrics, request correlation, and runtime instrumentation by default.
7. Provide readiness and liveness without introducing a large health subsystem.
8. Provide optional scheduling, CLI, REST-contract, database-dialect, and integration modules.
9. Compile and test with CGO_ENABLED=0.
10. Make application business logic usable without importing framework runtime types.
11. Be suitable for Stowcloud, but remain general enough for unrelated services.
12. Prefer mature upstream libraries over custom equivalents.
13. Make defaults secure and observable, while keeping advanced behavior overridable.
14. Support static deployment in containers and minimal images.
15. Let maintainers understand a basic application's execution through the same small Spec, module and lifecycle contracts, learning advanced security or listener-transition rules only when those modules are selected.
16. Provide reusable security enforcement and listener replacement without product-specific modes.
17. Ship independent API and non-HTTP starters that exercise the public bootstrap.

A representative application declares its bootstrap and feature composition:

~~~go
func main() {
    hanami.Main(hanami.Spec[Config]{
        Name:    "example",
        Load:    loadConfig,
        Modules: applicationModules,
    })
}
~~~

These are the implemented API names. The normative signature and phase contracts are in the [specification](hanami-framework-specification-20260921.md). Modules returns ordinary fx.Option. A basic service needs no security or ownership plan; when a pre-composition option such as Linux security is selected, Modules is called only after that option succeeds.

---

# 3. Non-goals

The framework must not become a replacement for the Go ecosystem.

It will not implement:

- a custom dependency-injection container;
- classpath or package scanning;
- annotation-style component discovery;
- a custom HTTP router;
- a custom ORM;
- a custom SQL query language;
- a custom migration engine;
- a custom logging framework;
- a custom tracing system;
- a custom metrics registry;
- a custom cron parser;
- a custom CLI parser;
- a custom serializer;
- a custom validation language for REST contracts;
- a custom event bus in the initial design;
- proxy-based AOP;
- method-name-derived repository queries;
- a distributed transaction coordinator;
- a universal cache abstraction;
- framework-owned authentication business rules;
- automatic hot reload for arbitrary application configuration.

Spring-like means application structure, DI, configuration ergonomics, persistence ergonomics, lifecycle, production defaults, and operational tooling. It does not mean copying Java runtime patterns into Go.

---

# 4. Design principles

## 4.1 Composition over reinvention

Every framework-owned package must answer one of these questions:

1. What upstream libraries need to be connected?
2. What production default should be selected?
3. What lifecycle ordering must be guaranteed?
4. What application-facing convention reduces repeated boilerplate?
5. What small missing primitive is not adequately served by a maintained dependency?

If a package is primarily reimplementing upstream functionality, it should not exist.

## 4.2 Plain Go application constructors

Application constructors should remain ordinary Go functions:

~~~go
func NewUserRepository(db *gorm.DB) *UserRepository
func NewUserService(repo *UserRepository) *UserService
func NewUserController(service *UserService) *UserController
~~~

These functions must not need:

- framework.Container;
- framework.Injector;
- service-locator lookup;
- a global registry;
- reflection annotations.

Fx may appear in framework module assembly code, but domain and service constructors should generally remain Fx-independent.

## 4.3 Standard types before framework types

Prefer these types directly:

- context.Context
- *slog.Logger
- *gorm.DB
- *http.Client
- time.Duration
- io.Reader
- io.Writer
- errors

Do not wrap a mature standard or ecosystem abstraction only to rename it.

## 4.4 Pay only for what is imported

A command-line worker should not need Gin. A simple Gin service should not require Huma, Goose, gocron, or SQLite unless explicitly selected.

Optional integrations must be isolated by package or Go module boundary.

## 4.5 Fail during startup

Missing configuration, invalid configuration, duplicate route registration, database initialization failure, migration failure, invalid lifecycle wiring, and telemetry exporter misconfiguration should fail before serving traffic when the failure makes safe operation impossible.

Degraded optional capabilities must be explicitly modeled, never silently dropped.

## 4.6 Context flows downward

Request, worker, job, and command contexts flow from adapters into services and repositories.

Framework-global state must not be used as a replacement for context propagation.

## 4.7 No silent fallback across security boundaries

Examples:

- a failed TLS configuration must not silently become plaintext;
- a failed authentication middleware must not silently become anonymous access;
- an invalid database DSN must not silently fall back to an in-memory database;
- a failed migration must not silently continue with an unknown schema;
- an unavailable required OTel exporter must follow explicit policy rather than disappearing without notice.

---

# 5. Dependency acceptance policy

A dependency may be adopted into an official framework module only if it passes the following review.

## 5.1 Required checks

- Repository is not archived.
- License is compatible with framework distribution.
- Tagged stable versions exist unless there is a compelling exception.
- No required CGO dependency exists for official modules.
- Current supported Go releases are supported.
- Critical security issues are addressed.
- API stability is acceptable for the module tier.
- Maintenance activity is sufficient for the dependency's maturity level.
- A realistic replacement boundary exists.
- The dependency materially removes implementation and maintenance burden.

Recent release frequency is evidence, not the sole criterion. Fx is intentionally accepted despite a slower release cadence because it is stable, SemVer v1, battle-tested, and provides architectural primitives that would otherwise require substantial custom infrastructure.

## 5.2 Dependency tiers

### Foundation

Foundation dependencies define framework architecture and require the highest review threshold:

- Uber Fx
- Gin
- Viper
- GORM
- OpenTelemetry

### Official integration

Supported by framework maintainers and covered by CI:

- Huma
- Goose
- gocron
- Kong
- PostgreSQL driver
- MySQL driver
- pure-Go SQLite dialect
- OpenTelemetry otelgin middleware

### Application-selected

Supported by normal Go composition but not guaranteed by the framework:

- alternate ORMs;
- alternate schedulers;
- alternate log handlers;
- alternate migration tools;
- alternate API contract libraries;
- alternate storage engines.

---

# 6. Version baseline as of 2026-09-20

The design baseline was verified against current stable releases available on 2026-09-20.

| Dependency | Baseline | Maintenance note |
|---|---:|---|
| Go | 1.27.1 | Stable patch release on 2026-09-01 |
| Uber Fx | 1.24.0 | Latest stable release 2025-05-13; retained for maturity and architecture |
| Gin | 1.12.0 | Current stable release verified 2026-09-21 |
| Viper | 1.21.0 | Released 2026-09-08 |
| Huma | 2.39.1 | Released 2026-07-29; Gin adapter available |
| GORM | 1.31.2 | Released 2026-06-25 |
| Goose | 3.28.0 | Released 2026-09-02 |
| OpenTelemetry Go | 1.46.0 | Stable release 2026-08-25 |
| OpenTelemetry otelgin | 0.71.0 | Published 2026-08-26 |
| gocron | 2.22.0 | Released 2026-07-09 |
| Kong | 1.16.1 | Published 2026-08-09 |
| libtnb/sqlite | 1.2.2 | Published 2026-08-04; pure-Go GORM SQLite adapter |

Pre-release dependencies are not used as framework defaults. For example, OpenTelemetry 1.47.0-rc.1 is not the baseline even when it contains desirable upcoming log stabilization.

Before implementation begins, the dependency lock must be refreshed to the latest stable compatible patch/minor release and revalidated under the acceptance policy.

---

# 7. Runtime architecture

The framework runtime consists of five conceptual layers:

~~~text
Application
  |
  | constructors and domain types
  v
Framework conventions
  |
  | module composition, defaults, integration
  v
Upstream libraries
  |
  | Fx, Gin, Viper, GORM, OTel, Goose, ...
  v
Go standard library
  |
  v
Operating system / database / network
~~~

The framework is not intended to hide upstream libraries completely. It should expose stable upstream types where doing so prevents pointless abstraction.

Examples:

- inject *gorm.DB, not framework.Database;
- use *slog.Logger, not framework.Logger;
- use context.Context, not framework.Context;
- use fx.Option for advanced module composition rather than inventing a second option system.

---

# 8. Uber Fx runtime design

## 8.1 Why Fx is the foundation

Fx is selected because it provides:

- constructor injection;
- explicit dependency graphs;
- error propagation during construction;
- lifecycle hooks;
- startup failure rollback;
- reverse-order shutdown;
- value groups;
- reusable modules;
- private providers;
- decoration and replacement;
- strong test utilities;
- mature production adoption.

The critical architectural property is that constructors remain plain Go functions.

## 8.2 Fx usage policy

Framework code may use:

- fx.Module
- fx.Provide
- fx.Supply
- fx.Invoke
- fx.Annotate
- fx.In
- fx.Out
- fx.Lifecycle
- fx.OnStart
- fx.OnStop
- value groups
- fx.Private
- fx.Decorate where it improves integration

Application service code should normally use none of them.

## 8.3 Value-group extension points

Value groups are the primary extension mechanism for independent modules.

Planned groups include:

- independent route contributors
- api_operations
- health_checks
- scheduled_jobs
- startup_validators
- cli_commands

Middleware sequences, migration execution, and shutdown coordination are explicitly ordered outside these groups.

Example conceptual route registration:

~~~go
type Route struct {
    Method  string
    Path    string
    Handler gin.HandlerFunc
}

func NewUserRoutes(controller *UserController) []Route {
    return []Route{
        {
            Method:  http.MethodGet,
            Path:    "/users/:id",
            Handler: controller.Get,
        },
    }
}
~~~

The provider is annotated into the routes group. The HTTP module consumes all routes without requiring feature modules to know about each other.

### 8.3.1 Ordered operations are not value groups

Fx value groups are unordered. Never use their iteration order to install middleware, run migrations, choose route precedence, start workers, or stop resources.

Application HTTP composition supplies one explicitly ordered slice of native `gin.HandlerFunc` values. Hanami installs it before route registration. Generic middleware presets return an ordered slice and remain optional. Do not add a second middleware dependency language merely to sort a value group.

Unordered groups remain appropriate for health checks and independent route contributors. If route contributors are collected in a group, construct all contributors, run their registration callbacks during one assembly step, detect registration conflicts, and only then allow a server to start. No contributor may mutate a serving engine or install global middleware after routes have been registered.

Migration ordering belongs to Goose's versioned migration provider, not Fx group order. A group of startup validators may run only after its required infrastructure is ready; validator failure blocks request admission.

## 8.4 Lifecycle rules

Constructors must not start long-running goroutines merely because an object was constructed.

Long-running components register lifecycle hooks with explicit ownership:

| Phase | Required behavior |
|---|---|
| Construct | Assemble values after selected process security succeeds |
| Start | Initialize infrastructure, validate schema, recover features, then start producers |
| Admit | Enable requests only after required conditions pass |
| Stop admission | Mark unready and reject new requests, jobs and configuration changes |
| Drain | Wait for all HTTP generations and accepted jobs with dependencies available |
| Close | Close consumers and infrastructure; preserve telemetry until its final producers stop |
| Finish | Flush remaining telemetry, release ownership and return the outcome |

Resources close in dependency-reverse order. A small explicit admission transition complements ordinary Fx hook ordering.

### 8.4.1 Construction, startup, and resource ownership

Fx calls required constructors and invocations during `fx.New`, before `OnStart`. Hanami's runner must complete every selected pre-composition option before calling the application module factory or `fx.New`. Stowcloud selects ownership and Linux security options explicitly. The security boundary is before Fx construction, not merely before `app.Start`.

Prefer constructors that validate and assemble values without acquiring external resources. Modules that must open a pool, descriptor, or provider during construction must document ownership and close those resources if graph construction or validation later fails. Successful graph construction does not exempt a failed `OnStart` from cleaning resources it acquired before returning its error.

The runtime contract is:

| Stage | Required predecessor | Failure behavior |
|---|---|---|
| Configuration validation | Source loading and typed validation | No long-lived services or Fx construction |
| Selected pre-composition options | Only the contracts requested by the application, such as ownership or Linux security | Refuse selected-option failure; no module factory invocation |
| Infrastructure startup | Valid configuration and installed sandbox where required | Close partial acquisitions |
| Schema validation or migration | Usable SQL pool | No workers or HTTP admission |
| Feature recovery and validators | Required schema and storage available | Remain unready; report the failing feature |
| Worker and scheduler startup | Recovery complete | Stop already-started producers on failure |
| HTTP admission | Middleware and routes assembled; required services ready | Readiness remains false |

Implement these dependencies explicitly through module inputs and a small startup coordinator where resource dependencies alone do not express a completion barrier. Do not assume provider order or the presence of `*sql.DB` proves that migrations have finished.

### 8.4.2 Shutdown and serving failures

Hanami's runner must first set readiness false and close admission for new HTTP work, configuration transitions and background jobs. Drain existing HTTP requests and jobs while their dependencies remain available, then cancel remaining work under the shared deadline and close resources in dependency order. Telemetry stays available through its final producers and is flushed before its own shutdown.

Normal reverse hook order handles resource ownership; it does not by itself express the simultaneous admission stop required by HTTP and scheduler producers. Register that transition explicitly. Keep the database and telemetry available until their last consumers have drained.

An unexpected HTTP serve-loop or required worker failure must be reported to the application supervisor. It must not leave a process marked ready after its serving goroutine exits. Log timeout and shutdown failures and produce a nonzero process exit when required cleanup or operation fails.

Acceptance includes constructor failure after an earlier acquisition, partial `OnStart` failure, migration failure, randomized group input, shutdown during a transfer, exporter flush failure, a serve loop exiting after startup, forged security handoff metadata, candidate failure before promotion, and whole-application shutdown with several draining generations.

## 8.5 No framework service locator

The framework must not expose:

~~~go
container.Resolve[T]()
app.Get[T]()
injector.Invoke[T]()
~~~

as the normal application programming model.

This protects constructor transparency and testability.

---

# 9. Application bootstrap

Hanami provides the common bootstrap entry point as a core deliverable. Its purpose is to centralize a small execution order and failure model while preserving Fx dependency resolution. Exclusive ownership and process security are selected extensions, not concepts every application must model.

Proposed API shape:

~~~go
type Spec[C any] struct {
    Name    string
    Load    func(context.Context) (C, error)
    Modules func(C) fx.Option
}

func Run[C any](ctx context.Context, spec Spec[C], opts ...Option[C]) (Result, error)
func Main[C any](spec Spec[C], opts ...Option[C])
~~~

Run owns startup, supervision and bounded cleanup without calling os.Exit. Main supplies conventional signal handling and process exit mapping. Active request/job contexts remain valid during graceful drain; cancellation of the stop signal is not immediate cancellation of all work.

The runner executes configuration loading, only the selected pre-composition options, module construction, Fx initialization, required recovery, admission, drain and close. A basic API service can provide no options. Selected options must not start unrelated production services, and the module factory is deferred until all of them succeed.

Advanced state belongs to the module that needs it. The Linux security integration owns policy/resource-grant validation and any security-specific snapshot or bootstrap re-exec contract. The ownership integration owns its lock and descriptor lifetime. Startup conditions remain feature registrations after Fx construction rather than fields of a universal plan.

Applications may still compose individual modules directly with Fx. Full bootstrap guarantees apply only when the shared runner or an equivalent verified host controls the required phases.

A small set of named phases is sufficient. Arbitrary before/after callback chains, package scanning, a second container and hidden global state are excluded.

See [Hanami Framework Specification](hanami-framework-specification-20260921.md) for normative startup, security, failure and public API contracts.

---

# 10. Configuration architecture

## 10.1 Viper as the engine

Viper is selected because the framework optimizes for immediate production usability rather than minimum dependency count.

Viper provides:

- defaults;
- configuration files;
- environment variables;
- flags and explicit overrides;
- decoding into typed values;
- optional file watching;
- established ecosystem support.

## 10.2 No global Viper usage

Application packages must not depend on package-global Viper state.

Forbidden default style:

~~~go
host := viper.GetString("database.host")
~~~

Preferred model:

~~~go
type DatabaseConfig struct {
    Host string
    Port int
}

func NewRepository(cfg DatabaseConfig, db *gorm.DB) *Repository
~~~

Framework configuration bootstrapping creates a Viper instance, loads sources, decodes typed configuration, validates it, then provides immutable typed configuration through Fx.

## 10.3 Default precedence

The default precedence from low to high is:

~~~text
framework defaults
application defaults
configuration file
environment variables
CLI overrides
explicit programmatic overrides
~~~

Applications may customize source policy deliberately, but accidental implicit precedence must be avoided.

## 10.4 Configuration file formats

Initial official support:

- YAML
- JSON
- TOML

YAML may be the default documentation format, but none of these formats become part of domain APIs.

## 10.5 Environment naming

A conventional application prefix is required.

Example:

~~~text
MYAPP_HTTP_PORT=8080
MYAPP_DATABASE_DSN=...
MYAPP_OTEL_ENDPOINT=...
~~~

Nested key transformation must be deterministic and documented.

## 10.6 Typed validation

Configuration must be validated once before application services start.

The framework may integrate a maintained validator such as go-playground/validator in a dedicated config validation option, but configuration types may also expose a simple validation method:

~~~go
type Validatable interface {
    Validate() error
}
~~~

This interface should remain tiny. The framework must not create a custom validation DSL.

## 10.7 Hot reload policy

Arbitrary configuration hot reload is not enabled by default.

Configuration is immutable after startup unless a module explicitly declares reload semantics.

Reason:

- HTTP bind changes require listener semantics;
- database DSN changes require pool replacement;
- TLS changes require certificate replacement;
- worker concurrency changes require runtime coordination;
- log levels can usually change safely.

A universal "reload everything" mechanism would hide component-specific state transitions.

Hanami provides explicit operations for supported changes: log level, listener replacement and certificate rotation. Applications own authorization, desired configuration, revisions, operation identity and durable history. Framework managers report the observed runtime state and the immediate transition result; they do not become operation-history stores. A rejected change must not be presented as active; an applied change followed by persistence failure requires application reconciliation, not a fictitious rollback.

Sandbox permission expansion returns an external-restart-required outcome. Self re-exec cannot remove an inherited restriction. Hanami does not invoke a deployment supervisor. When the selected Linux security integration depends on mutable policy input, its pre-composition contract must prevent a second uncoordinated configuration load from changing that policy before graph construction.

---

# 11. Gin + net/http HTTP architecture

## 11.1 Gin is the official router and middleware runtime

Hanami standardizes on Gin for routing and middleware composition while keeping Go's standard `net/http` stack as the transport foundation.

The dependency direction is:

~~~text
net/http
   |
  Gin
   |
Hanami HTTP module
   |
application routes and middleware
~~~

Hanami must not hide Gin behind proprietary request, router, controller, response, or context abstractions.

Applications may directly use:

- `*gin.Engine`;
- `gin.IRoutes`;
- `*gin.RouterGroup`;
- `gin.HandlerFunc`;
- `*http.Server`;
- `http.Handler`;
- `*http.Request`;
- `http.ResponseWriter`.

The framework should make Gin cheaper to assemble, not replace Gin's programming model.

The architectural contract is explicit:

> **Hanami bootstraps Gin; it does not abstract Gin.**

> **Gin and net/http types remain public application APIs.**

> **Application middleware and routing policy remain application-owned.**

> **Hanami provides integration, defaults, lifecycle glue, and observability:not a second HTTP framework.**

## 11.2 Standard request context semantics

Gin is built on `net/http`, so request cancellation and deadlines use standard `*http.Request.Context()` semantics.

Rules:

1. Domain and application services accept `context.Context`.
2. Gin handlers pass `c.Request.Context()` into services.
3. Domain services must not accept `*gin.Context`.
4. Repository APIs must not accept `*gin.Context`.
5. Long-running request work should honor standard request cancellation where semantically appropriate.
6. Background workers and application jobs use lifecycle-owned contexts rather than request contexts.
7. A handler must not retain `*gin.Context` after the request returns.

This simplifies integration with `database/sql`, WebDAV, standard middleware, streaming, and request-scoped cancellation.

## 11.3 Thin Gin module responsibilities

The official Gin module may own only:

- `*gin.Engine` construction and Fx provisioning;
- release/debug mode selection;
- deterministic installation of caller-provided `gin.HandlerFunc` middleware;
- optional generic middleware presets such as recovery, request ID, access logging, `otelgin`, security headers, and CORS;
- optional health endpoint mounting helpers;
- optional standard `http.Server` lifecycle helpers;
- graceful shutdown helpers;
- test bootstrap based on `httptest`.

The module must return and expose the real `*gin.Engine`. Middleware remains the real `gin.HandlerFunc`. Route groups remain the real `*gin.RouterGroup`. Hanami must not introduce parallel middleware, router, request, or response concepts.

It must not own:

- business authentication or authorization;
- application-specific trusted-proxy policy;
- a custom router tree;
- `hanami.Context`;
- `hanami.Controller`;
- `hanami.Response`;
- listener generation management inside the Gin router module; this belongs to Hanami's separate optional HTTP module;
- protocol-specific WebDAV or Nextcloud semantics.

## 11.4 Middleware ordering

The optional generic preset returns the following explicit order. Fx value groups must not determine this order:

~~~text
panic recovery
request identity
OpenTelemetry
access logging
security headers
generic CORS if configured
application middleware
route handler
~~~

Authentication, authorization, CSRF, trusted-proxy interpretation, tenant resolution, and similar policy-heavy middleware remain application modules unless they can be expressed as genuinely generic primitives.

## 11.5 Route definition

Raw Gin registration is the default application model and is always supported. Applications are expected to register routes with normal Gin APIs such as `GET`, `POST`, `Handle`, `Group`, and `Use`.

A small Fx route descriptor may exist only as an opt-in convenience to remove repetitive module glue. It must never become mandatory and must still carry the real `gin.HandlerFunc`:

~~~go
type Route struct {
    Method  string
    Path    string
    Handler gin.HandlerFunc
}
~~~

The HTTP module may aggregate opt-in route groups and reject duplicate method/path registrations during startup. All global middleware must already be installed. Direct Gin registrations follow the same assembly-before-serving rule.

Direct registration remains equally valid:

~~~go
func RegisterRoutes(r *gin.Engine, h *UserHandler, auth gin.HandlerFunc) {
    api := r.Group("/api")
    api.Use(auth)
    api.GET("/users/:id", h.Get)
}
~~~

No package scanning, annotation registration, reflection-based controller discovery, or hidden route mutation is allowed.

## 11.6 Server lifecycle and ownership

Gin construction and server ownership are separate integrations. The optional HTTP module supports a fixed native server and a managed-generation server. Stowcloud selects the latter; its product-local listener supervisor has been retired.

A generation owns one native http.Server and listener, its serving result, admission state and immutable configuration. Application services and the Fx graph are shared across generations. Replacing a listener does not rebuild the graph or close shared stores and workers.

| Transition | Required behavior |
|---|---|
| Validate | Check address, protocol, TLS, permissions and transition budgets |
| Prepare and verify | Build the candidate behind closed public admission; perform a bounded check of the intended server |
| Promote | Record the new active generation at one defined linearization point |
| Drain | Stop old admission and track existing connections under a bounded policy |
| Retire | Join the old serving loop and release resources before removing it from tracking |

Before promotion, failure preserves the old active server. After promotion, a drain failure is reported as an applied transition with a cleanup failure. It cannot be described as an uncommitted change.

Checks use explicit protocol and verified TLS with appropriate server identity and trust. No protocol guessing, disabled certificate verification, or unrelated root-page response is acceptable. The candidate's internal supervisor check must not bypass application authentication for ordinary routes.

The manager owns candidates, the active generation and every draining generation until completion. It serializes replacement attempts and bounds concurrent draining generations. Product-level stale revision checks remain in the application settings service. Whole-application shutdown joins all owned generations. Unexpected serving failure reaches the runner and readiness state.

Certificate updates use a validated immutable TLS snapshot when supported. Same-address listener replacement is not assumed possible while the existing socket is bound. WebSocket and other upgraded connections need explicit lifecycle participation.

Native types remain exposed. One resource has one close owner. Servers and listeners created by the HTTP module are closed by that module. Borrowed resources are not closed unless an API explicitly transfers ownership. Once a manager owns a server, external code must not start or close it independently. The manager reports the immediate replacement result and current runtime state only; product revisions, operation IDs, history and retention belong to the application.

---

# 12. Huma REST-contract module

## 12.1 Huma is optional

Huma is an official module for conventional REST APIs.

It is not required for:

- WebSockets;
- protocol compatibility surfaces;
- streaming-heavy endpoints;
- reverse proxies;
- custom binary protocols;
- specialized Gin/net/http middleware applications.

## 12.2 Huma responsibilities

When enabled, Huma provides:

- typed request binding;
- typed response modeling;
- validation;
- OpenAPI 3.1;
- JSON Schema;
- RFC-style Problem Details;
- generated API documentation metadata.

The framework must not duplicate these capabilities.

## 12.3 Domain isolation

Huma request and response types belong in transport packages.

Services should receive domain commands and values, not Huma operation structures.

~~~text
Huma Input
   |
   v
Controller / adapter
   |
   v
Domain command
   |
   v
Service
~~~

---

# 13. Persistence architecture

## 13.1 ORM-first policy

GORM is the primary persistence abstraction.

The framework intentionally does not require users to start from database/sql repositories.

Applications may use database/sql directly where needed, but official examples should show GORM-first development.

## 13.2 Spring-like application structure

Recommended feature package structure:

~~~text
internal/
  user/
    entity.go
    repository.go
    service.go
    controller.go
  article/
    entity.go
    repository.go
    service.go
    controller.go
~~~

This is a convention, not a hard framework requirement.

## 13.3 No framework JpaRepository clone

Do not introduce a universal:

~~~go
Repository[T, ID]
~~~

as a mandatory abstraction in v0.x.

GORM already abstracts SQL sufficiently. A second generic repository layer would reduce access to ORM capabilities while introducing framework lock-in.

Application repositories may remain simple:

~~~go
type UserRepository struct {
    db *gorm.DB
}

func NewUserRepository(db *gorm.DB) *UserRepository {
    return &UserRepository{db: db}
}
~~~

## 13.4 GORM generics

New framework examples should prefer the current GORM generics API where it improves type clarity.

Classic GORM API remains supported because the framework exposes *gorm.DB directly.

## 13.5 Database lifecycle

The database module owns:

- dialing;
- pool configuration;
- initial Ping or equivalent validation;
- GORM construction;
- instrumentation;
- health registration;
- graceful pool close.

The module provides:

- *gorm.DB
- underlying *sql.DB when useful through a dedicated provider

The framework does not expose a custom Database wrapper.

---

# 14. Database dialect policy

## 14.1 PostgreSQL

PostgreSQL is the preferred server database for examples requiring a full database server.

Stack:

~~~text
GORM
  |
gorm.io/driver/postgres
  |
pgx v5
~~~

## 14.2 MySQL

Supported official module:

~~~text
GORM
  |
gorm.io/driver/mysql
  |
go-sql-driver/mysql
~~~

## 14.3 SQLite

The official SQLite module must remain pure Go.

The standard GORM SQLite driver uses a CGO-based SQLite implementation and is therefore not the official framework choice.

Current baseline:

~~~text
GORM
  |
libtnb/sqlite
  |
modernc.org/sqlite
~~~

The SQLite adapter is isolated because its ecosystem is less mature than PostgreSQL/MySQL support.

Required CI for the adapter:

- Linux amd64
- Linux arm64
- macOS arm64
- Windows amd64
- CGO_ENABLED=0
- representative GORM behavior
- transaction behavior
- migration behavior
- WAL configuration example
- SQLITE_BUSY behavior tests where applicable

---

# 15. Migration architecture

## 15.1 Goose is authoritative for production schema changes

Production schema migration is explicit and versioned.

GORM AutoMigrate must not be the production source of truth.

## 15.2 Why Goose

Goose provides:

- library API;
- CLI support;
- SQL migrations;
- Go migrations;
- version tracking;
- database locking support;
- active maintenance;
- a provider API that can operate over an existing *sql.DB.

## 15.3 Connection reuse

The framework should use the same underlying SQL pool as GORM where possible:

~~~text
database module
  |
  +-- *gorm.DB
  |
  +-- *sql.DB
         |
         +-- Goose Provider
~~~

This avoids hidden duplicate connection pools.

## 15.4 Startup migration policy

Applications choose one explicit policy:

- disabled;
- validate only;
- migrate before start.

The default for production presets should be conservative and documented.

A failed required migration prevents HTTP startup.

## 15.5 Development AutoMigrate

AutoMigrate may be exposed as a development-only convenience option, visibly separate from Goose production migrations.

---

# 16. Transaction policy

The framework should initially use GORM's native transaction API.

Preferred service code:

~~~go
return db.Transaction(func(tx *gorm.DB) error {
    // use tx
    return nil
})
~~~

The framework must not immediately create proxy-based or context-hidden transaction magic analogous to Spring @Transactional.

A small transaction coordinator may be reconsidered only if repeated multi-repository transaction composition becomes a real cross-project problem.

---

# 17. Logging architecture

## 17.1 slog is the logging API

The framework uses log/slog directly.

It does not define:

- framework.Logger;
- framework.Field;
- framework.LogEvent.

## 17.2 Default handlers

Recommended presets:

Development:

~~~text
human-readable structured console handler
debug-capable
source optional
~~~

Production:

~~~text
slog JSON handler
UTC timestamps
stable field naming
no secrets
~~~

Third-party pretty handlers may be optional integrations rather than foundation dependencies.

## 17.3 Correlation fields

Standard framework fields should include where available:

- service.name
- service.version
- environment
- request_id
- trace_id
- span_id
- route
- method
- status
- duration
- job_name
- worker_name

High-cardinality or secret data must not be added automatically.

---

# 18. OpenTelemetry architecture

## 18.1 OTel is the telemetry backbone

Tracing and metrics use OpenTelemetry directly.

The framework does not create a parallel abstraction.

## 18.2 Stable signals

As of the design baseline:

- traces are stable;
- metrics are stable;
- logs are not yet selected as the framework logging API.

Therefore logs remain slog-first.

## 18.3 Provider lifecycle

The observability module owns:

- Resource construction;
- TracerProvider;
- MeterProvider;
- propagators;
- exporters;
- batching;
- ForceFlush during shutdown;
- provider shutdown.

## 18.4 Gin instrumentation

Use OpenTelemetry's official `otelgin` middleware rather than custom request-span middleware unless an upstream limitation requires a narrowly scoped patch. Hanami should configure service name, propagators, providers, filters, and span naming through the upstream middleware rather than wrapping it in a new telemetry API.

## 18.5 Outgoing HTTP

Use standard OTel net/http instrumentation for outgoing clients rather than inventing a framework HTTP-client telemetry layer.

## 18.6 Database telemetry

Prefer maintained GORM or database/sql OTel instrumentation that preserves standard semantic conventions.

Do not use unbounded SQL text, user identifiers, file paths, or object names as metric dimensions.

---

# 19. Tiny owned health primitive

Health is the deliberate exception to the "use an external library" rule.

The ecosystem candidates reviewed did not provide enough advantage to justify a foundation dependency given maintenance uncertainty and the small size of the required primitive.

## 19.1 Scope

The framework health package owns only:

- check registration;
- check type classification;
- timeout;
- concurrent execution;
- result aggregation;
- deterministic naming;
- readiness and liveness HTTP projection;
- optional detail redaction.

It does not own:

- service discovery;
- leader election;
- Kubernetes client integration;
- auto-remediation;
- restart policy;
- alerting;
- dependency graph construction;
- retries;
- circuit breakers.

## 19.2 Proposed types

~~~go
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
~~~

Exact exported names may change, but the primitive should remain this small.

## 19.3 Fx registration

Checks are registered using an Fx value group.

~~~text
database module --------\
scheduler module --------+--> health_checks --> health registry
application checks ------/
~~~

## 19.4 Semantics

Liveness asks:

> Is the process internally capable of continuing to run?

Readiness asks:

> Should this process currently receive normal traffic?

Examples:

- temporary database outage may fail readiness;
- exporter outage may warn but not fail readiness depending on policy;
- internal invariant corruption may fail liveness;
- optional degraded features may return warn.

## 19.5 HTTP endpoints

Default conventional endpoints:

- GET /health/live
- GET /health/ready

The response schema must be versionable and stable once published.

Health details exposed publicly must not include secrets, DSNs, filesystem paths, access tokens, or internal stack traces.

---

# 20. Scheduler module

gocron v2 is an optional official integration.

The module owns:

- scheduler construction;
- Fx start and stop;
- job aggregation;
- OTel spans;
- structured job logging;
- health registration when meaningful.

Applications provide jobs through a value group.

The framework must not wrap all gocron scheduling types. Advanced users should be able to use native gocron options.

---

# 21. CLI module

Kong is the optional official CLI parser.

The CLI module should support framework-level commands such as:

~~~text
app serve
app migrate up
app migrate down
app migrate status
app version
~~~

Applications may extend the command tree.

Kong types should stay at the executable/adapter boundary, not inside business services.

The framework should avoid creating a second CLI schema.

---

# 22. CGO-free guarantee

## 22.1 Policy

All official framework modules must compile and test with:

~~~sh
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go build ./...
~~~

CGO-only dependencies are not accepted into official modules.

## 22.2 Rationale

This enables:

- predictable cross-compilation;
- static deployment;
- scratch/distroless containers;
- no system C toolchain requirement;
- fewer libc compatibility issues;
- simpler CI;
- easier embedded and NAS deployment;
- simpler Stowcloud distribution.

## 22.3 SQLite enforcement

The official SQLite module must never silently switch to a CGO driver.

If the pure-Go adapter becomes unmaintained, the module should be replaced or temporarily unsupported rather than violating the guarantee.

---

# 23. Security defaults

The framework provides secure defaults and optional process enforcement. Applications determine domain access policy; Hanami validates and applies the declared generic requirements.

Baseline HTTP policies:

- panic recovery without stack leakage to clients;
- request body limits;
- explicit trusted proxy configuration;
- safe default `http.Server` timeouts;
- secure header module or documented preset;
- CORS disabled unless configured;
- profiling/debug endpoints disabled unless explicitly enabled;
- no permissive wildcard credentials CORS preset;
- no insecure TLS verification helpers in public framework API.

The optional Linux adapter owns capability discovery, profile validation, process-wide application, a verified bootstrap handoff where needed, and enforcement status. It delegates Landlock and seccomp mechanics to reviewed upstream libraries. A required capability cannot be silently removed to fit an older kernel or binding.

Supported-kernel detection does not prove enforcement. Environment re-exec markers are not evidence that a sandbox is active. Thread coverage, descriptor inheritance and denied-operation behavior are release gates.

Restrictions that survive exec cannot be relaxed by self re-exec. Permission expansion requires a new launch from outside the old restriction. Hanami reports external restart required and does not manage or invoke the deployment supervisor. Generic security integration is optional and must not force Linux dependencies into core.

Secrets:

- never log config values marked or documented as secrets;
- never include DSNs with credentials in health output;
- support environment/secret-manager injection without requiring a framework secret store.

---

# 24. Error model

The core framework does not define domain errors.

Raw Gin applications use application-defined error mapping.

Huma applications use Huma's Problem Details model for HTTP contract errors.

Framework startup errors should:

- wrap root causes;
- preserve errors.Is/errors.As behavior;
- include operation context;
- avoid secret values;
- fail once at the top-level application boundary rather than being swallowed.

---

# 25. Recommended application architecture

The framework recommends a feature-oriented structure:

~~~text
cmd/
  server/
    main.go

internal/
  user/
    entity.go
    repository.go
    service.go
    controller.go
    module.go

  article/
    entity.go
    repository.go
    service.go
    controller.go
    module.go

  platform/
    auth/
    storage/

migrations/
config/
~~~

Each feature may expose an Fx option or module from module.go while keeping implementation details private.

The framework should not force one global controllers/, services/, repositories/ tree for large applications.

---

# 26. Module composition example

Conceptual feature module:

~~~go
func Module() fx.Option {
    return fx.Module(
        "user",
        fx.Provide(
            NewRepository,
            NewService,
            NewController,
        ),
        fx.Invoke(RegisterRoutes),
    )
}

func RegisterRoutes(r *gin.Engine, h *UserController, auth gin.HandlerFunc) {
    api := r.Group("/api")
    api.Use(auth)
    api.GET("/users/:id", h.Get)
}
~~~

This is preferable to scanning packages for struct tags or naming conventions.

The dependency graph remains visible in code and inspectable by tools.

---

# 27. Hanami repository topology

Hanami is the official project and repository identity for the application framework. It is intentionally independent from the Stowcloud GitHub organization and from Stowcloud-specific infrastructure repositories.

The preferred long-term topology is a single Hanami repository with modular Go package boundaries first, followed by Go submodule splitting only where dependency isolation proves valuable.

Initial layout:

~~~text
hanami/
  app/
  bootstrap/
  process/
  security/
    linux/
  config/
  health/
  gin/
  http/
  api/
  observability/
  database/
    postgres/
    mysql/
    sqlite/
  migration/
  scheduler/
  cli/
  testkit/
  starters/
  internal/
~~~

Do not prematurely split every integration into independent repositories.

If optional dependency isolation becomes a measurable problem, heavy integrations can become nested modules while remaining in one repository.

Criteria for splitting a module:

- large transitive dependency graph;
- independent release cadence;
- consumers commonly use core without the integration;
- semantic versioning can remain manageable;
- CI can test compatibility independently.

---

## 27.1 Optional integration isolation

A separate package avoids importing its runtime code but does not promise an independent module dependency graph, minimum Go version, or release policy. Document these separately.

The minimal release contains only its actual consumers' required packages. Add a heavy integration only after a consumer demonstrates the need. If an integration imposes unrelated module or toolchain requirements on core consumers, isolate it in a nested module before advertising module graph isolation.

Measure `go list -deps`, module requirements, binary size, and startup behavior for the core-only and Gin examples. A core-only application must neither import Gin nor initialize any database, telemetry exporter, scheduler, or HTTP listener.

Implemented isolation uses nested modules for `config`, `gin`, `observability`,
`api`, `cli`, `database`, `migration`, and `scheduler`. API, worker, and platform
starters are independent modules. The measured root module graph fell from 261
modules before isolation to 16 modules. The core worker builds without Gin,
Huma, GORM, Goose, gocron, Kong, Viper, or OpenTelemetry dependencies.

# 28. Framework-owned code budget

To prevent infrastructure creep, each integration package should remain primarily glue.

A review warning is triggered when a framework integration begins duplicating the upstream library's concepts.

Examples of suspicious growth:

- custom router tree inside `gin/`;
- custom query builder inside database;
- custom config parser inside config;
- custom span implementation inside observability;
- custom cron parser inside scheduler;
- custom CLI AST inside cli.

Framework-owned code is justified when it implements documented lifecycle, safety, compatibility or developer experience that upstream does not provide. The bootstrap coordinator, enforcement integration and server generation manager are such owned mechanisms. Each needs a bounded contract and independent tests; none justifies duplicating an upstream router, kernel binding or DI container.

---

# 29. Testing strategy

## 29.1 Unit tests

Test framework-owned policy and glue, not upstream library internals.

Examples:

- config precedence;
- invalid config failure;
- health aggregation;
- lifecycle registration;
- route duplicate detection;
- graceful shutdown ordering;
- telemetry resource metadata;
- SQLite CGO-free startup;
- Goose and GORM connection reuse.

## 29.2 Integration tests

Required combinations:

- Gin + Fx startup/shutdown;
- Gin + Huma typed endpoint;
- Gin + OTel instrumentation;
- Viper config file + env override;
- GORM + PostgreSQL;
- GORM + MySQL where CI permits;
- GORM + pure-Go SQLite;
- Goose + each supported database;
- gocron + Fx shutdown;
- health + database failure;
- CLI migration command.

## 29.3 Example applications and starters

Ship an independent API starter and a CLI or worker starter that uses no Gin. Both use the same Spec/Load/Modules contract, configuration examples, local commands and CI. Neither requires ownership, security policy or restart metadata unless that starter explicitly selects the corresponding optional module. The API starter includes native Gin handlers, health, structured logs, optional configured OTel, graceful shutdown and a production Dockerfile.

Secured-service and managed-generation examples cover enforced security status, candidate rejection, certificate update, and full drain independently of Stowcloud.

Database and scheduler examples follow actual integration delivery. Repository templates suffice initially; a generator is not required.

Examples are executable compatibility evidence, not decorative documentation.

## 29.4 Stowcloud dogfooding

Stowcloud is a high-complexity consumer used to validate:

- non-standard HTTP verbs;
- large streaming bodies;
- long-running transfers;
- dynamic runtime behavior;
- database lifecycle;
- background work;
- graceful drain;
- telemetry cardinality;
- security-sensitive startup.

Stowcloud-specific product policy remains outside the framework. Shared security enforcement, server generation management and application shutdown are Hanami contracts exercised by Stowcloud and independent examples.

---

# 30. CI gates

Minimum framework CI:

~~~sh
go test ./...
go test -race ./...
go vet ./...
govulncheck ./...

CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go build ./...
~~~

Additional gates:

- Linux amd64;
- Linux arm64;
- macOS arm64;
- Windows amd64;
- latest stable Go;
- previous supported Go release if the support policy allows it;
- dependency license check;
- static analysis;
- examples build;
- public API compatibility check once v1 approaches.

Optional modules must each have an explicit CI matrix. The portable core and ordinary integrations run on their advertised platforms. Linux security conformance runs only on declared Linux kernels/architectures; other platforms must still build core and reject an explicitly required unsupported security profile.

---

# 31. Dependency update policy

Use automated dependency PRs through Renovate or Dependabot.

Policy:

- security updates: immediate;
- patch updates: automatic PR;
- minor updates: automatic PR with full integration suite;
- major updates: design review;
- pre-release updates: never automatic for foundation dependencies.

A weekly compatibility workflow should attempt current stable versions and report failures.

"Latest" is a continuously enforced property, not a one-time architecture document claim.

---

# 32. Go version policy

Initial minimum Go version: 1.27.

Reasons:

- the framework is greenfield;
- current ecosystem baseline already supports modern Go;
- using the current language version reduces compatibility baggage;
- framework consumers are expected to be new or actively maintained services.

After v1, follow a documented rolling support policy instead of opportunistically increasing the minimum.

A possible policy is current stable and previous stable Go major release, subject to foundation dependency requirements.

---

# 33. API stability policy

Before v1:

- breaking changes are allowed but documented;
- framework-owned APIs should remain intentionally small;
- upstream types are preferred when they reduce lock-in;
- experimental modules are explicitly labeled.

At v1:

- Semantic Versioning applies;
- published optional integrations obey SemVer within their own module; experimental status and support commitments must be explicit;
- database adapters may have independent compatibility notes;
- removal requires a deprecation window unless security requires immediate action.

---

# 34. Observability defaults

A production preset should provide useful telemetry without configuration explosion.

Default resource attributes:

- service.name
- service.version
- deployment.environment.name where available

Default server metrics should include:

- request count;
- request duration;
- active requests if upstream instrumentation supports it safely;
- response status class;
- route template, not raw path.

Do not label metrics by:

- user ID;
- email;
- file path;
- object key;
- request ID;
- trace ID.

Those belong in logs/traces when appropriate, not metric dimensions.

---

# 35. Startup sequence

The common runner uses the same small phases for Stowcloud and independent applications:

| Phase | Owner and completion condition |
|---|---|
| Load | Application loader returns validated typed configuration |
| Pre-compose | Only selected options run; ownership or Linux security validates and acquires/enforces only its own contract |
| Compose | Deferred module factory returns Fx options; Fx constructs the graph and routes |
| Initialize | Required infrastructure, schema checks and feature recovery complete |
| Admit | Workers and HTTP begin accepting application work only when required conditions pass |
| Run | Hanami supervises required components and explicit reconfiguration |

There is no separate Stowcloud security execution path in the target. Stowcloud's selected Linux security option receives its policy/resource grants, owns security-specific consistency checks and performs any verified bootstrap re-exec. Applications that do not select that option skip those concepts entirely.

No arbitrary callback may start background services before selected pre-composition options complete. The exact contract and failure outcomes are defined in the [specification](hanami-framework-specification-20260921.md).

# 36. Shutdown sequence

Hanami owns a single bounded shutdown:

1. Set readiness false and close request, job and configuration-change admission.
2. Abort unpromoted candidates and track all current and draining server generations.
3. Drain accepted requests, upgraded connections and jobs with dependencies available.
4. Cancel or terminate remaining work according to the declared deadline policy.
5. Close consumers before their dependencies; preserve logs and telemetry until their final producers stop.
6. Flush remaining telemetry, release the ownership lock last, and return the combined outcome.

The stop signal does not immediately cancel active request/job contexts. Every operation uses the remaining total budget rather than receiving a fresh full timeout.

Restart requests enter the same cleanup path. Self re-exec is limited to compatible effective permissions and a validated handoff. Permission expansion requires external launch under the deployment contract. No feature callback may close the engine and exec independently.

# 37. Health and shutdown interaction

On shutdown initiation:

1. readiness immediately transitions to fail;
2. liveness remains pass while graceful drain is functioning;
3. HTTP health endpoints may remain reachable during the drain period if the server architecture permits;
4. once listener shutdown completes, the process proceeds to dependent resource shutdown.

This prevents load balancers from sending new work while allowing a deployment system to observe controlled termination.

---

# 38. Developer experience target

The framework should make the common path short while retaining direct access to underlying libraries.

A new REST service should mostly write:

- configuration struct;
- entities;
- repositories;
- services;
- controllers;
- feature modules;
- migrations.

It should not repeatedly write:

- OS signal handling;
- Gin/net/http construction boilerplate;
- OTel provider setup;
- database pool cleanup;
- migration runner wiring;
- health endpoint aggregation;
- Fx event logger plumbing;
- scheduler shutdown;
- request correlation plumbing.

That repeated operational glue is the framework's reason to exist.

---

# 39. Rejected alternatives

## 39.1 samber/do instead of Fx

Rejected for the baseline because Fx better matches:

- plain constructor injection;
- value-group module composition;
- complete start/stop lifecycle;
- startup rollback;
- mature framework usage.

samber/do remains a watchlist dependency and may be reconsidered if its lifecycle and provider ergonomics materially improve.

## 39.2 Koanf instead of Viper

Koanf remains attractive for modularity and dependency size.

Viper is selected because the framework prioritizes immediate production usability, ecosystem maturity, configuration features, and low application setup work over the smallest configuration dependency graph.

Application code is protected from Viper lock-in by typed config injection.

## 39.3 Fiber, Echo, or Chi instead of Gin

Gin is selected as Hanami's primary HTTP integration because it keeps standard `net/http` semantics, has a large and mature ecosystem, and still provides a concise router/middleware API.

Fiber remains a reasonable performance-oriented alternative, but its fasthttp foundation imposes request-lifetime and interoperability semantics Hanami does not need to impose on all consumers.

Chi remains architecturally attractive and closer to the standard library. Hanami intentionally chooses Gin because the project wants a batteries-enough router/middleware baseline rather than a router-minimal identity. This choice does not justify wrapping Gin behind Hanami-owned transport types.

## 39.4 Ent instead of GORM

Ent has stronger generated type safety but imposes schema/code-generation architecture on every consumer.

GORM better matches the desired low-friction, Spring-like ORM experience.

## 39.5 Bun instead of GORM

Bun is a strong SQL-first ORM but does not align as closely with the intended entity/association-first developer experience.

## 39.6 Atlas instead of Goose

Atlas is more powerful for schema management but is heavier operationally and conceptually.

Goose is sufficient as an embedded, explicit migration engine. Atlas can remain an external advanced tool.

## 39.7 External health framework

Rejected for now because the needed feature set is sufficiently small and candidate maintenance quality did not justify a new foundation dependency.

The owned health package is intentionally constrained to a tiny primitive.

---

# 40. Framework versus Stowcloud boundary

Hanami owns the common execution model from bounded preflight to shutdown. Applications describe their requirements and features through that model.

| Responsibility | Hanami | Stowcloud |
|---|---|---|
| Configuration | Load/validate integration and snapshot injection | Schema, precedence compatibility and admin persistence |
| Security | Optional Linux enforcement and verified bootstrap strategy | Required resources and product access policy |
| HTTP | Gin setup, native servers, fixed or managed generations | Routes, authentication, authorization and protocol semantics |
| Ownership | Lock lifecycle and explicit descriptor handoff | Which data directory requires exclusive ownership |
| Startup | Fx graph, conditions and request admission | Schema validation and feature recovery operations |
| Shutdown/restart | Common drain plus an external-restart-required outcome when needed | Authorized restart intent and all deployment/supervisor behavior |
| Storage/transfer/search | Optional lifecycle adapters | Product policy and independent library composition |

Hanami does not absorb Stowcloud VFS semantics, accounts, grants, quota, WebDAV/Nextcloud behavior, SMB configuration or file-transfer protocols.

## 40.1 Hanami thinness contract

Thinness means preserving native library APIs and a bounded responsibility. It allows owning reusable execution mechanisms with substantial failure semantics.

Removing Hanami should replace composition and operational wiring, not rewrite domain services. Fx options, Gin, net/http, slog, GORM and OpenTelemetry remain visible. Core can run without HTTP, databases or Linux security.

Thinness is evaluated in two dimensions. Runtime thinness means an unselected module contributes no background workers, initialization or unrelated dependencies. Cognitive thinness means a developer building a basic API or CLI does not need to understand sandbox revisions, re-exec, listener generations or deployment restart policy until selecting those integrations.

Forbidden abstractions include a custom Context, Controller, Service, Repository, Logger wrapper or service locator. The small bootstrap option boundary and the optional HTTP generation manager are legitimate owned concepts because they define concrete lifecycle and ownership semantics without forcing advanced state onto basic applications.

## 40.2 Stowcloud-specific ownership

Stowcloud retains configuration discovery, share-to-resource mapping, route requirements, body classes, ACL/quota/share/public-link policy, protocol compatibility, desired settings and storage/transfer recovery semantics.

The product does not retain its own server-process sandbox sequence, signal loop, or listener supervisor. Product policy and specialized worker isolation remain in Stowcloud.

## 40.3 Stowcloud adopted composition

The adopted entry point uses the public bootstrap:

~~~go
func main() {
    hanami.Main(
        hanami.Spec[Config]{
            Name:    "stowcloud",
            Load:    loadConfig,
            Modules: applicationModules,
        },
        ownership.WithRequirement(buildOwnershipRequirement),
        securitylinux.WithPolicy(buildSecurityPolicy),
    )
}
~~~

The deferred applicationModules factory returns fx.Options containing common logging, OTel, health, Gin, managed HTTP and Stowcloud feature modules. When Linux security is selected, its policy builder and the module factory must observe the same validated policy-relevant configuration.

Stowcloud feature modules supply plain constructors, routes, startup conditions and drain participation. A narrow runtime integration may translate product events into manager operations; it must not duplicate lifecycle ownership.

# 41. Initial implementation phases

The framework roadmap and Stowcloud migration are separate streams. A basic release must advertise only delivered modules. Full Stowcloud bootstrap adoption requires the later security and generation gates.

## Phase 0: Bootstrap core and ownership

Deliver Spec/Run/Main, Load/Modules sequencing, typed configuration injection, the optional pre-composition option boundary, slog, Fx lifecycle integration, small health contracts and cleanup ownership.

Acceptance: no module construction before required enforcement, no silent source fallback, bounded partial-failure cleanup, explicit startup conditions, one stop owner, and a core-only example without Gin.

## Phase 1: Independent starters

Deliver native Gin/fixed-server integration, selected OTel integration, an API starter and a non-HTTP CLI or worker starter. Include local commands, configuration, Dockerfile where appropriate and CGO-free CI.

Acceptance: independent consumers use the public bootstrap without Stowcloud fixtures; application code owns features while common initialization is reused. Record dependency, startup, binary and removal costs.

## Phase 2: Security and managed server generations

Deliver the optional Linux enforcement adapter and the HTTP generation manager, with independent secured and reconfigurable service examples.

Acceptance: truthful process-wide enforcement, security-specific validated re-exec or supported thread synchronization, descriptor ownership, candidate rejection, verified TLS, promotion outcomes, bounded retired-generation drain and external-restart classification without supervisor control.

## Phase 3: Full Stowcloud bootstrap adoption

Move security enforcement, signal handling, common shutdown and listener management to the same public Hanami integrations. Preserve product policy, existing settings semantics and low-level library independence.

Acceptance: no duplicate legacy owner remains after the rollback window; transfer/protocol/security behavior is preserved; the entry point only selects configuration, modules and the optional ownership/security integrations it actually needs.

## Phase 4: Demand-driven integrations

Huma, GORM PostgreSQL/MySQL/pure-Go SQLite, Goose, gocron, and Kong are implemented as nested modules. The independent platform starter consumes them together. Existing Stowcloud SQL stores remain unchanged.

## Stowcloud adoption stages

| Stage | Scope | Acceptance |
|---|---|---|
| A | Separate feature services and HTTP adapters while Fiber remains | Existing behavior and narrow dependencies |
| B | Introduce Fx modules behind a verified pre-construction security boundary | Startup and partial cleanup invariants |
| C | Adopt Hanami runner and validated common modules | Same configuration snapshot; one signal and shutdown owner |
| D | Migrate security enforcement through the optional Linux adapter as a separate change | Supported profile equivalence and restart classification |
| E | Add Gin adapters and managed-server integration behind a single selected transport | Protocol, authorization, streaming and lifecycle parity |
| F | Switch the selected transport/server owner after isolated validation | No duplicate bind or mutation; all generations drain |
| G | Retire legacy security/listener owners | Completed: Hanami is the sole production owner |

Feature decomposition and persistent-format ownership remain in Stowcloud. The preview worker retains its specialized child-process jail; the server process uses Hanami security.

## Migration compatibility and rollback

The ecosystem strategy owns the migration ledger: artifact versions, supported formats, baseline measurements, rollback command, observation window and evidence that the prior binary can read live state.

Keep transport, persistent-format, storage-backend and security-backend changes separate. Mutations run through exactly one selected adapter. Safe read replay and isolated fixtures may compare behavior; live uploads must not be duplicated for parity.

Rollback preserves settings, sessions, indexes and in-flight work. Readiness, TLS, denied operations, cancellation, upgraded connections, listener transitions and bounded drain are release gates. A router rollback does not downgrade state or relax an installed sandbox.

# 42. Open design questions

These questions should be resolved during implementation, not guessed prematurely:

1. Which demonstrated integration first requires a nested module under the isolation criteria in Section 27.
2. Whether config validation should officially integrate go-playground/validator or remain an application hook.
3. Whether Hanami should ship any optional Fx route-registration helper at all; direct Gin registration via `fx.Invoke` remains the baseline and must not depend on such a helper.
4. Exact health response JSON schema.
5. Whether development logging should depend on a third-party slog handler.
6. Default migration policy for generated starter projects.
7. Exact OTel exporter preset and environment variable compatibility.
8. Whether a worker module separate from gocron is needed in v0.x.
9. Whether the SQLite adapter's current ecosystem maturity is sufficient for v1 support.

These are intentionally left open because resolving them now would create API commitments without implementation evidence.

---

# 43. Definition of framework quality

The framework is ready to be called a reusable framework rather than a Stowcloud helper library when all of the following are true:

- at least two unrelated example applications can use it naturally;
- Stowcloud uses the common bootstrap and optional security/server integrations without a product-specific framework mode;
- application services remain framework-runtime independent;
- official modules compile with CGO_ENABLED=0;
- startup and shutdown are deterministic under failure;
- observability works without application-specific glue;
- health semantics are stable and documented;
- database migrations are explicit and reproducible;
- configuration precedence is deterministic;
- dependency updates are automated and continuously tested;
- optional integrations do not force unrelated runtime dependencies;
- upstream library capabilities are not redundantly reimplemented;
- public starters demonstrate the same execution model in unrelated applications;
- managed generations and sandbox enforcement have explicit, verified failure contracts.

---

# 44. Final architecture statement

Hanami owns a small public execution model and its cross-library integration.

| Layer | Responsibility |
|---|---|
| Application definition | Configuration, feature modules and only the optional integration requirements it needs |
| Hanami bootstrap | Typed load, selected pre-composition options, one Fx graph, admission, supervision and shutdown |
| Optional integrations | Gin/native HTTP generations, Linux security, OTel, SQL, migrations, scheduling |
| Upstream foundations | Fx, net/http, Gin, slog, Viper, GORM, Goose, OTel and reviewed kernel bindings |
| Application services | Plain Go domain behavior independent of the framework runtime |

The design succeeds when understanding this execution model makes another consumer's composition predictable. It does not require hiding native APIs or moving product rules into Hanami.

---

# 45. Upstream references used for this baseline

These references were initially checked on 2026-09-20 and the Gin/Fx HTTP baseline was refreshed on 2026-09-21:

- Go releases: https://go.dev/dl/
- Uber Fx: https://github.com/uber-go/fx
- Gin: https://github.com/gin-gonic/gin
- OpenTelemetry Gin instrumentation: https://pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin
- Viper: https://github.com/spf13/viper
- Huma: https://github.com/danielgtaylor/huma
- GORM: https://github.com/go-gorm/gorm
- Goose: https://github.com/pressly/goose
- OpenTelemetry Go: https://github.com/open-telemetry/opentelemetry-go
- gocron: https://github.com/go-co-op/gocron
- Kong: https://github.com/alecthomas/kong
- Pure-Go GORM SQLite adapter: https://github.com/libtnb/sqlite

Contract references:

- Fx value-group ordering: https://uber-go.github.io/fx/value-groups/index.html
- Fx construction and hook lifecycle: https://uber-go.github.io/fx/lifecycle.html
- Linux security enforcement and inheritance: https://docs.kernel.org/userspace-api/landlock.html
- Native HTTP shutdown: https://pkg.go.dev/net/http#Server.Shutdown
- Normative Hanami execution contract: [framework specification](hanami-framework-specification-20260921.md)

Version numbers in this document are a dated architecture baseline, not a permanent pin. Implementation must resolve the latest stable compatible versions under the dependency acceptance policy and commit the resulting module graph.
