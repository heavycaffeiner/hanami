# Hanami Framework Specification

Status: H0 through H4 implemented. H2 security support is Linux amd64 and arm64. H3 Stowcloud adoption is complete.
Date: 2026-09-21.
Scope: reusable Go application bootstrap, optional process security, Fx composition, HTTP server lifecycle, observability, configuration changes, starter projects, and Stowcloud adoption.

## 1. Purpose and authority

Hanami provides a documented execution model that applications share. A maintainer who understands this model should be able to locate configuration, access policy, feature construction, readiness conditions, and shutdown behavior in a new project without rediscovering its infrastructure conventions.

Stowcloud is a first consumer of this model. Its account rules, file permissions, protocol compatibility, settings history, and data formats remain product responsibilities. Hanami core owns only the common execution path. Optional modules own reusable mechanisms such as process security enforcement and listener replacement when an application selects them.

MUST and MUST NOT identify release requirements. SHOULD identifies a default that requires a documented reason to change. MAY identifies optional behavior. Package names and Go declarations are implemented API shapes unless a section explicitly marks them as future work.

This specification is authoritative for Hanami bootstrap, process security integration, server generation management, and their application contracts. Companion documents retain their detailed responsibilities:

| Document | Responsibility |
|---|---|
| [Hanami architecture](hanami-architecture-design-20260920.md) | Library choices, integration rationale, package boundaries |
| Ecosystem migration records | Maintained by each adopting application repository |
| Storage and transfer contracts | Maintained by the specialized library or application that owns them |
| Search and sandbox worker contracts | Maintained by the specialized library or application that owns them |

An ownership change is complete only when these documents agree. The frontend E2E design is outside this specification's editing scope.

## 2. Product boundary

| Concern | Hanami responsibility | Application responsibility |
|---|---|---|
| Process bootstrap | Load validated configuration, run selected pre-composition integrations, construct one Fx application, and coordinate stop/error handling | Select optional integrations and supply application modules |
| Security | Optional module validates and applies only the policy explicitly selected by the application | Decide which paths, operations and subprocess purposes are required |
| Composition | Construct one Fx application after selected pre-composition integrations succeed | Return ordinary Fx modules and plain Go constructors |
| HTTP | Optional module constructs and owns only the native servers/listeners it creates or explicitly adopts | Routes, authentication, authorization, protocol behavior and body classes |
| Configuration changes | Report the actual outcome and current state of a supported component operation | Authorize changes, validate business rules, persist desired state, operation identity and history |
| Readiness | Aggregate required checks and admission state | Define required schema, storage and recovery conditions |
| Shutdown | Stop admission, coordinate drain, enforce a deadline, report failures | Supply feature-specific drain and recovery behavior |
| Restart | Return an explicit external-restart-required outcome when an in-process transition is impossible | Decide whether and how the deployment restarts the process |

Hanami MUST expose standard and upstream types: context.Context, fx.Option, *gin.Engine, gin.HandlerFunc, http.Handler, *http.Server, net.Listener, *slog.Logger, *sql.DB, *gorm.DB where selected, and OpenTelemetry types.

Hanami MUST NOT introduce a second DI container, custom HTTP context, domain base class, generic repository layer, universal reload transaction, distributed scheduler, container runtime, or application ACL engine.

## 3. Packages and dependency direction

Package names are provisional, but responsibilities are fixed.

| Package | Purpose | Activation |
|---|---|---|
| hanami | Spec, Run, Main, application outcome | Core |
| bootstrap | Minimal pre-composition option coordination and common sequencing | Core |
| config | Typed source loading and validation helpers | Optional loader integration |
| log | slog setup and Fx event integration | Core default |
| health | Liveness, readiness and bounded check aggregation | Optional module, API starter default |
| gin | Native engine construction and middleware assembly | Optional |
| http | Native server setup and server generation manager | Optional |
| observability | OTel providers and HTTP integration | Optional, explicitly configured by starter |
| security/linux | Landlock/seccomp integration and verified bootstrap strategy | Optional Linux integration |
| process | Bounded shutdown outcomes and external-restart-required classification | Core helper; no supervisor integration |
| testkit | Run the same application definition under controlled dependencies | Development only |
| database, migration, scheduler, api, cli | Demand-driven ecosystem integrations | Optional |
| starters | Working API and non-HTTP application templates | Release deliverables |

Core MUST NOT import Gin, a SQL driver, a Linux sandbox implementation, or initialize a telemetry exporter by default. Package isolation and Go module graph isolation MUST be reported separately. A nested module is justified only by measured dependency or toolchain isolation needs.

Thinness has two release gates. At runtime, an unselected optional module MUST NOT add background work, initialization, or unrelated runtime dependencies to a basic consumer. At the API level, an API or CLI starter that does not select Linux security or managed HTTP generations MUST NOT require users to understand resource grants, security revisions, re-exec, listener replacement, or deployment restart policy.

Storage, transfer, namesearch, durablefs, and sandbox-worker MUST remain usable without Hanami, Fx, or Gin. Integration adapters depend on these libraries; the libraries never depend on the framework.

## 4. Proposed application API

The public surface should begin with a small application definition that does not require a universal bootstrap plan.

~~~go
type Spec[C any] struct {
    Name    string
    Load    func(context.Context) (C, error)
    Modules func(C) fx.Option
}

func Run[C any](ctx context.Context, spec Spec[C], opts ...Option[C]) (Result, error)

func Main[C any](spec Spec[C], opts ...Option[C])
~~~

The declarations above describe an API proposal, not compilable code available from a released package. Option[C] is a narrow framework integration value, not an arbitrary before/after callback registry. Core and optional Hanami packages may expose typed option constructors for responsibilities they own.

Contract:

- Load validates external input and returns a typed configuration value.
- A basic application can omit all options and proceed directly from Load to module composition.
- Selected options may perform bounded pre-composition work. Each option owns only its own contract, dependencies and cleanup.
- Modules is invoked only after every selected pre-composition option succeeds. A selected security option therefore finishes enforcement before module construction.
- Modules returns fx.Option; ordinary fx.Module, fx.Provide, fx.Invoke, fx.Decorate and fx.Replace remain available at the composition boundary.
- Run performs startup, waits for context cancellation or an internal stop request, completes shutdown, and returns the outcome. It MUST NOT call os.Exit.
- Run does not install a second OS signal loop when the caller already controls cancellation. Main supplies the conventional OS signal adapter and maps the returned result to a process exit code after cleanup.
- The signal context MUST NOT directly cancel active HTTP requests or jobs at shutdown initiation. Drain uses an independent bounded context; work contexts are cancelled only when the configured drain policy requires it.
- Main owns common stderr error reporting and exit mapping. Configuration/security refusal, runtime failure, and clean stop must remain distinguishable.
- Result records the stop reason and any required external restart. An error preserves root causes through errors.Is/errors.As.
- Hanami core does not invoke a deployment supervisor. An external-restart-required result is information for the application or deployment layer.
- A selected security backend may use a verified exec-based bootstrap mechanism before graph construction when needed for process-wide enforcement. That mechanism belongs to the security integration, not to the generic runner or configuration loader.

A Stowcloud entry point should eventually look like:

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

applicationModules selects Hanami integrations and Stowcloud feature modules. It MUST NOT implement another signal loop, manually apply a sandbox, or start an independent listener supervisor.

Ordinary module-only use with direct Fx remains supported. It does not automatically provide the guarantees of the full bootstrap entry point.

## 5. Optional pre-composition requirements and configuration consistency

Hanami does not require every application to build a generic Plan. The default path is only Load, selected options if any, Compose, Run, Drain and Close. A small API service that does not select process security or exclusive ownership does not need configuration revisions, resource grants, re-exec metadata or lock semantics.

Optional modules request only the information they need:

| Selected integration | Additional input it may require |
|---|---|
| Ownership | Resource identity and acquisition policy for the one resource it will own |
| Linux security | Effective policy, validated resource grants and any security-specific source identity needed to reject stale enforcement |
| HTTP | Native server configuration and explicit owned/borrowed resource semantics |
| Common runner | Startup, drain and total shutdown budgets |
| Feature modules | Required startup conditions registered after Fx construction |

An application that does not select sandboxing has no security profile object at all. It does not select a synthetic disabled profile. If the Linux security module is selected and a required capability is unsupported, that module fails closed.

Go values containing slices or maps are not automatically immutable. Framework-owned option data is copied and validated where necessary. Application configuration is treated as read-only after Load, with secrets held only where needed.

Load and selected pre-composition options MUST NOT create unrelated long-lived workers, exporters, listeners or production database pools. A bounded read needed by a selected option is allowed. It must use least privilege, close temporary handles on all paths, and report corruption or access failures. A missing first-run store may use explicit defaults; an unreadable existing store must not silently become a default configuration.

Exclusive ownership is opt-in. When a selected ownership option must stabilize mutable state used by a security decision, that option coordinates the lock and bounded revalidation required for that decision. Basic consumers are not required to implement repeated Load calls or a global revision protocol.

When a selected security option depends on mutable policy input, it must prove that the policy enforced before composition matches the policy supplied to application modules. A second uncoordinated configuration load that can change that policy is forbidden. Applications without such a pre-composition security dependency need only ordinary typed configuration consistency.

If the Linux security module requires a bootstrap re-exec to enforce its policy process-wide, that module owns the bounded, versioned handoff contract. Secrets must not be placed in argv or a general environment dump. Intentionally inherited descriptors must have explicit transfer and close ownership.

## 6. Execution phases and startup barriers

| Phase | Allowed work | Completion condition |
|---|---|---|
| Load | Parse sources and validate external input | Typed configuration available |
| Pre-compose | Run only selected bounded options such as ownership or process security | Every selected option completed its own contract and cleanup responsibility is established |
| Compose | Invoke Modules and construct one Fx graph | Dependencies and route assembly valid |
| Initialize | Start infrastructure, telemetry where selected, schema validation and feature recovery | Named required conditions complete |
| Admit | Start producers and activate application request admission | Required checks pass and serving loop is healthy |
| Run | Serve, execute work and accept supported reconfiguration | Runtime remains supervised |
| Drain | Refuse new work and complete active operations | Consumers stopped within budget |
| Close | Release infrastructure, flush remaining telemetry and release ownership | Cleanup completed or failure returned |

Constructors SHOULD assemble values without opening external resources. If a dependency requires constructor-time acquisition, its adapter MUST own cleanup even if a later constructor fails before OnStart. The failing OnStart must clean its own partial acquisitions; framework rollback cannot assume that hook completed.

Resource ownership follows one rule: one acquired resource has one close owner. A module that creates a listener, pool, descriptor or provider closes it. A module that merely borrows an application-owned resource does not close it. APIs that accept externally created resources MUST make borrow versus ownership-transfer semantics explicit.

Fx owns dependency injection and ordinary lifecycle hooks. Hanami adds a bounded admission coordinator for facts such as migration completion and recovery completion. It MUST NOT create another general dependency scheduler.

Provider presence does not imply operational readiness. A database object existing in the graph does not prove schema readiness. Startup validators that depend on migration completion must run after that completion.

Unordered Fx groups may collect independent checks. They MUST NOT determine middleware order, migration order, worker startup sequence, or shutdown sequence. Cycles and missing required conditions fail before public admission.

A startup failure closes admission and unwinds only acquired resources. Security enforcement is irreversible within the process; a failed secured startup terminates that execution instead of attempting to remove the sandbox and retry with weaker permissions.

## 7. Linux security integration

### 7.1 Policy and mechanism

Applications supply required operations. Hanami's optional Linux adapter validates and enforces them using reviewed upstream libraries. The initial candidates are go-landlock and go-seccomp-bpf. Their exact versions, capabilities, CGO behavior and architecture support must be proven before adoption.

The application policy may name read-only paths, writable directories, explicitly required execution, and supported network restrictions. Hanami does not infer broad directory or syscall permissions from failed operations.

Required rights that the kernel or selected binding cannot enforce cause refusal. Explicitly selected degraded mode reports every missing protection and the resulting guarantee. Masking away required rights and reporting full enforcement is forbidden.

Denied-operation tests must verify the shipped backend on the supported kernel and architecture matrix. A successful capability query alone is not proof that a profile was installed.

### 7.2 Process-wide enforcement

Landlock and seccomp integration must cover every relevant thread before application composition. The implementation must select a verified process-wide mechanism for the running kernel and binding. The kernel documentation describes Landlock thread synchronization for supported ABIs; this does not prove the chosen Go binding exposes it. See the [Linux Landlock API](https://docs.kernel.org/userspace-api/landlock.html).

Where process-wide application is unavailable, use a tested bootstrap re-exec or launcher strategy that preserves restrictions into all runtime threads. Otherwise refuse a required profile. Do not assume applying a rule from one Go goroutine secures the entire process.

An environment variable such as a re-exec marker is routing metadata, never proof of enforcement. A forged marker, stale handoff or ordinary direct invocation must not skip sandbox installation. The backend must document how its controlled handoff and kernel enforcement establish the guarantee; if that cannot be established, startup fails.

Bootstrap-only descriptors, ownership locks, worker IPC descriptors and application descriptors have separate allowlists and lifetimes. Inherited descriptors may grant access beyond a path policy; unlisted descriptors must not survive the handoff. Close or transfer failures remain visible.

### 7.3 Policy updates and restart

A policy revision is classified against the actually enforced profile, not merely the last saved setting.

| Change | Result |
|---|---|
| Same effective required profile | No security transition |
| More restrictive profile | Requires an explicitly supported update path; initial release may require process replacement |
| Additional access beyond the enforced profile | Report external restart required to the application |
| Required capability unsupported | Rejected |
| Explicit switch to a weaker mode | Requires an authorized configuration change and suitable process replacement; never an automatic fallback |

Landlock restrictions survive exec and cannot be relaxed in place. Self re-exec and a child forked by the restricted application cannot supply newly denied rights. See the [Landlock inheritance contract](https://docs.kernel.org/userspace-api/landlock.html).

For expanded access, only a launcher outside the old restriction can start a new instance under the approved policy. Hanami reports external restart required and stops there. The application and deployment layer decide whether, when and how to restart. Hanami MUST NOT invoke systemd, Docker, Kubernetes or another supervisor as part of the generic contract.

An embedded privileged broker, arbitrary remote execution service and supervisor-management API are out of scope.

## 8. Fx and feature integration

Features use normal constructors and narrow dependencies. Domain services accept context.Context and domain arguments. They MUST NOT accept *gin.Context or resolve dependencies from a global application object.

Each feature module registers:

- providers and route contributors;
- required startup validation or recovery;
- background work owned by the application lifecycle;
- health checks where meaningful;
- drain or cancellation participation where it owns active work.

The data store that implements a feature remains an application choice. Stowcloud adoption MUST NOT require rewriting existing SQL stores to GORM.

A required worker or serving loop failure is delivered to the runner. A normal, supervised replacement of one pool worker need not terminate the application, but exhaustion of the declared recovery policy must affect readiness and the configured stop policy.

## 9. Gin and HTTP contract

Hanami constructs and exposes the native Gin engine. Applications use ordinary Group, Use, Handle and native handlers. Domain authentication, authorization, CSRF, trusted proxies and WebDAV/Nextcloud compatibility remain explicit application middleware or adapters.

Global middleware is installed before routes. Router assembly finishes before serving and does not mutate a live Gin engine. The optional middleware preset is an ordered native slice.

The HTTP integration has two supported selections:

| Selection | Behavior |
|---|---|
| Fixed server | One configured native server with standard lifecycle |
| Managed generations | The same basic server setup plus candidate validation, promotion and tracked draining |

Stowcloud selects managed generations. Other consumers may select either mode, or provide their own server with explicit lifecycle ownership. One server or listener MUST have one start/close owner.

A server generation is a native *http.Server and its owned listener with immutable serving configuration, generation identity, admission state and terminal result. Application services are shared across generations. An assembled handler may be shared where all its state is safe for concurrent use; changing generation-specific routing requires a new assembled handler.

Generation replacement MUST NOT rebuild the entire Fx graph or close shared databases, storage handles, worker pools or transfer managers.

Public native types remain usable for assembly and inspection. One resource has one close owner. A server or listener created by the HTTP module is owned and closed by that module. A borrowed externally created resource is never closed by Hanami unless the API explicitly transfers ownership. Once ownership transfers to a manager, callers must not independently call Serve, Close or Shutdown on those same resources.

HTTP limits must distinguish short API requests from large uploads, downloads and long-lived connections. A universal short WriteTimeout or unconditional whole-body buffering is not an acceptable streaming default.

## 10. Server generation transition

The manager exposes a small transition API at the application composition boundary. Proposed API shape:

~~~go
type ReplaceRequest struct {
    Server ServerConfig
}

type ReplaceResult struct {
    GenerationID string
    State        ReplaceState
}

func (m *Manager) Replace(ctx context.Context, req ReplaceRequest) (ReplaceResult, error)
func (m *Manager) Current() ServerState
~~~

ServerConfig describes validated native HTTP/TLS settings; it is not a custom request API. ReplaceState distinguishes applied, rejected and applied-with-drain-failure. An error after promotion accompanies an applied state. Current reports the manager's observed server state; it is not an operation-history store.

The manager does not allocate operation IDs, retain request history, define a retention period or persist application revisions. Stowcloud's settings service may do all of those above the manager. The manager serializes replacement attempts for each logical server and returns the exact outcome it observed.

| Step | Action | Failure outcome |
|---|---|---|
| Validate | Validate address, protocol, TLS, security permissions and time budgets | Reject without changing active state |
| Prepare | Acquire candidate listener and immutable server state | Close candidate acquisitions; keep old server |
| Verify | Start candidate behind closed application admission and perform a bounded serving check | Stop candidate and keep old server |
| Promote | Record the new generation and enable its admission under the manager's transition lock | This is the replacement linearization point |
| Drain | Close old admission and drain its owned connections | Report cleanup failure with the applied replacement; do not pretend the old generation remained active |
| Retire | Join the old serving loop and release remaining resources | Remove from the draining set only after terminal accounting |

Candidate verification must prove the intended server is serving, not merely that a socket can be dialed. Use the configured protocol, direct the check to the candidate, disable redirects, and verify its expected response identity. TLS checks require the correct server name and a trust configuration appropriate to that certificate, including a scoped local trust anchor for a generated certificate. No InsecureSkipVerify probe is permitted.

Before promotion, only a narrowly scoped authenticated management check may pass the candidate admission gate; application routes remain closed. A root-page response from an arbitrary service does not satisfy readiness. The internal check must not leak a reusable credential or become a public authentication bypass.

A serving error observed before promotion rejects the candidate. An error after promotion is a runtime failure handled by the manager and runner. No claim of future availability is made from a successful probe.

Caller cancellation before promotion aborts and cleans the candidate. After promotion, cleanup remains owned by the manager independently of the caller's request context. Return an applied outcome when known. If the caller loses the response, Current exposes the actual runtime generation so the application can reconcile its own durable intent.

A timeout while draining an old generation does not undo a completed promotion. The result distinguishes applied, rejected and applied-with-drain-failure. The final error and replacement state must agree.

Identical effective configuration is a no-op. Certificate rotation is handled separately from address replacement. Same-address replacement that would require rebinding an occupied socket must be rejected or use an explicitly supported deployment mechanism; the baseline does not promise seamless same-port handoff.

The manager tracks the active generation and every candidate/draining generation until their terminal results are joined. A configured finite draining-generation limit prevents repeated changes from accumulating unbounded sockets and goroutines. Reject further changes visibly when the limit is reached.

## 11. Configuration changes and durable intent

Hanami provides component operations, not a transaction spanning application databases and operating-system listeners.

| Change | Handler |
|---|---|
| Logging level | Explicit supported log control |
| HTTP address | Server generation manager |
| TLS certificate | Validate a new immutable certificate snapshot; atomically publish it for future handshakes |
| Worker concurrency | Pool-specific reconfiguration if supported |
| Database identity or unsupported runtime option | Restart-required outcome |
| Sandbox rights expansion | External restart-required outcome |

Certificate rotation preserves the listener and existing connections where supported. A key mismatch or invalid certificate leaves the old snapshot active. Authorization policy and durable certificate storage remain explicit application choices.

Stowcloud's settings service owns authorization, schema validation, desired state, application revisions, operation identity and operation history. It uses compare-and-swap revision checks to reject stale concurrent updates. The HTTP manager owns only observed runtime server state and transition results.

The recommended application protocol is:

1. Validate and durably record the application's pending desired revision and operation identity if the product needs them.
2. Request the component transition without transferring application history ownership to Hanami.
3. Record the returned transition result and observed current server state in the application store.
4. Reconcile pending or uncertain application operations after a crash from durable intent and actual runtime state.

A rejected bind can leave an explicit failed pending revision while the previous revision remains active. The user interface must not label desired state as active. An applied transition followed by a persistence failure is reported as applied with reconciliation required, never as an automatic rollback.

The authoritative store defines transaction and durability rules. Use the storage/durablefs contracts where files carry intent. Do not invent a distributed transaction between disk and socket state.

After process restart, the current runtime generation is rebuilt from validated durable intent under application policy. Historical operation records cannot prove that an old listener is still active.

## 12. Shutdown and external restart result

One runner owns shutdown for each application. Stop requests are idempotent and share the terminal outcome.

The shutdown sequence is:

1. Mark readiness false and close HTTP, scheduler and worker admission.
2. Stop configuration transitions; abort unpromoted candidates and retain ownership of promoted/draining generations.
3. Drain existing HTTP requests, upgraded connections and accepted jobs with their dependencies available.
4. At the configured deadline, apply the declared cancellation/termination policy and report incomplete work.
5. Close feature and infrastructure resources in dependency order, keeping logging and telemetry available to their final producers.
6. Flush remaining telemetry, release the ownership lock last, and return the combined result.

A single total budget bounds shutdown. Sub-operations receive the remaining budget, not a fresh full timeout per resource. Startup and shutdown budgets are explicit settings with documented starter values. Stowcloud must select values appropriate to transfers.

Go's Server.Shutdown does not wait for hijacked connections such as WebSockets, and an expired shutdown context does not itself force all active connections closed. The integration must separately track upgraded connections and apply the declared forced-close policy when needed. See [net/http shutdown](https://pkg.go.dev/net/http#Server.Shutdown).

Closing an old listener generation does not stop application-wide transfer recovery, storage or indexing services. Those remain available until every dependent generation and accepted job has drained.

Persisted upload sessions are not discarded because their current HTTP request is interrupted. They retain the recovery behavior defined by the transfer contract. Uncertain publication remains uncertain until reconciled.

Runtime restart orchestration is not a Hanami responsibility. The common runner supports normal bounded shutdown and can return an external-restart-required reason. It does not call a supervisor, keep deployment-specific restart state or promise that exiting will cause a restart.

A security backend may still use a pre-composition re-exec solely as an enforcement mechanism described in Section 7. That is not a general runtime restart facility and cannot widen inherited restrictions.

## 13. Health, observability and diagnostics

Readiness is the conjunction of required startup conditions, serving/admission state and required runtime health. Liveness must not restart a service merely because an optional exporter or remote dependency is temporarily unavailable.

Diagnostics must identify the phase, module or component and wrapped cause. An application-supplied correlation or operation identity may be propagated in logs, but Hanami does not allocate or retain it. Diagnostics must not log credentials, raw tokens, encrypted-share secrets or complete DSNs.

Minimum runtime visibility:

- bootstrap phase and bounded duration;
- selected security backend, required and enforced capability status;
- required startup condition failures;
- current server generation and sanitized observed server state;
- candidate rejection, promotion, draining count and drain failure;
- required worker failure and shutdown timeout;
- desired/active configuration mismatch.

Request telemetry labels use route patterns and bounded categories. Generation IDs, application operation IDs, filenames and user identities belong in controlled logs or traces, not unbounded metric labels.

No debug dashboard, profiling route or remote management listener is exposed by default. Operational state can be consumed through typed inspection and application-authorized endpoints.

## 14. Stowcloud integration

| Existing responsibility | Target location |
|---|---|
| CLI dispatch and product command meanings | Stowcloud command adapter |
| Stored setting discovery and share-to-grant mapping | Stowcloud security policy builder |
| Security application and verified re-exec sequencing | Hanami optional Linux adapter |
| Signal handling and whole-application shutdown | Hanami runner |
| Bind, verify, promote, drain and serving failure supervision | Hanami HTTP generation manager |
| Administrator authorization and settings persistence | Stowcloud settings service |
| Recovery of sessions, index state and storage operations | Feature services registered through Fx |
| Resource lock mechanism | Hanami bootstrap ownership integration |
| Which data directory is exclusively owned | Stowcloud ownership requirement |
| ACL, quota, grants, DAV and Nextcloud behavior | Stowcloud feature services and HTTP adapters |

Stowcloud must use the same runner and HTTP integration exposed to independent consumers. An application-named special mode in Hanami is forbidden.

The legacy listener manager and server-process security bootstrap are retired. Hanami is the sole owner for those phases. Stowcloud retains its specialized preview-worker jail.

Stowcloud's optional sandbox-worker integration starts and stops the pool through the common lifecycle. sandbox-worker still owns subprocess handshake, FD protocol and child isolation. Its worker entry point must not start the entire application graph.

## 15. Starter deliverables

The first reusable release must include working starts, not only module examples.

| Starter | Required contents |
|---|---|
| API | Spec/Load/Modules with no required advanced bootstrap object, native Gin routes, fixed HTTP server, health, structured logs, explicitly selectable OTel configuration, graceful shutdown |
| CLI or worker | Same bootstrap model without Gin or an HTTP listener; explicit completion/stop behavior |
| Secured service example | Linux profile, supported/unsupported behavior, enforced-status diagnostics |
| Reconfigurable service example | Managed generations, certificate rotation, desired/active state example and shutdown |

Each shipped starter includes a configuration example, documented precedence, local run instructions, a development command, meaningful tests, a production Dockerfile where applicable, and CI that verifies its advertised platforms with CGO_ENABLED=0.

The API and non-HTTP starters must be independent of Stowcloud models and fixtures. Repository templates are sufficient initially; an application code generator is out of scope until needed.

Security and managed-generation examples ship independently and Stowcloud consumes the same public integrations.

## 16. Delivery milestones

| Milestone | Deliverable | Completion gate |
|---|---|---|
| H0 | Core Spec/Run/Main, typed config path, slog, Fx lifecycle, health contracts | Basic consumers need no advanced bootstrap object; selected pre-composition options run before module construction; cleanup and stop ownership demonstrated |
| H1 | Native Gin/server integration, selected OTel integration, API and non-HTTP starters | Independent consumers run with documented commands; no unwanted Gin dependency in non-HTTP consumer |
| H2 | Optional Linux enforcement and managed server generations | Supported-kernel proof, denied-operation checks, transition and drain failure cases pass |
| H3 | Stowcloud adoption | Common runner coordinates selected security and HTTP modules; each module owns only its resources; product policy remains in Stowcloud; legacy owners retired after rollback window |
| H4 | Demand-driven integrations | Each optional ORM, migration, scheduler or REST-contract module has a real consumer and support evidence |

H0 through H4 are implemented. Later framework integrations remain demand-driven and must not be advertised before a real consumer and support evidence exist.

Feature decomposition, transport replacement, security-backend replacement and persistent-format changes remain separate changes with independent rollback records.

## 17. Required verification before release

These are implementation acceptance requirements, not claims that tests were run while writing this specification.

| Area | Required case |
|---|---|
| Phase boundary | Module factory and Fx constructors cannot run before required security succeeds |
| Input failure | Invalid existing configuration, unsafe grant or stale source revision refuses admission |
| Partial startup | Constructor acquisition and failed OnStart release owned resources |
| Security truth | Forged marker and unsupported required rights cannot produce enforced status |
| Thread coverage | Every relevant thread is covered for each supported enforcement strategy |
| Descriptor boundary | Unlisted inherited FDs do not survive; intended ownership lock does survive a supported handoff |
| Restart | Same-policy re-exec preserves restrictions; expanded policy requires external launch |
| Startup barriers | Migration/recovery failure prevents HTTP and worker admission |
| HTTP candidate | Bind, TLS, readiness and serve-loop failures preserve the old active generation |
| Promotion | Cancellation around the linearization point reports the actual outcome |
| Concurrent changes | HTTP replacement is serialized and reports exact current state; application-level stale revisions, retries and operation history remain application tests |
| Drain | Multiple replaced generations and upgraded connections are tracked through whole-app shutdown |
| Boundaries | Old generation drain does not close shared DB, storage, jobs or transfer state |
| Configuration durability | Crash before/after promotion and outcome persistence reconciles desired and active state |
| Transfers | Shutdown during upload preserves acknowledged progress and publication uncertainty |
| Isolation | Non-HTTP consumer has no Gin dependency; low-level libraries have no Fx dependency |
| Operational failure | Required worker or server failure clears readiness and reaches the runner |
| Compatibility | Existing public routes, authentication, streaming and protocol behavior remain compatible |

Performance gates measure startup cost, dependencies, binary size, memory and transition latency against a recorded baseline. Do not choose thresholds after observing results or claim improvement from fewer lines alone.

## 18. API and compatibility policy

Public types use application-neutral names and document ownership, borrowing, concurrency, cancellation and error outcomes. Hanami runtime transition state is ephemeral; any durable settings or operation-history format belongs to the application and is versioned there.

Before v1, signature changes still require release notes and migration guidance. A framework update must not silently change security mode, TLS trust, request limits, shutdown behavior or configuration precedence.

Every adoption stage records the previous binary, supported state formats, rollback procedure and retirement condition. Transport rollback does not imply data-format rollback.

## 19. Remaining implementation decisions

The following require implementation evidence:

- exact upstream versions and the verified kernel/architecture support matrix;
- exact bootstrap handoff transport for any re-exec backend;
- exact external-restart-required result shape and reason vocabulary;
- concrete starter time budgets and streaming overrides;
- whether a measured dependency constraint requires a nested Go module;
- whether a shared lower-level security adapter is justified beyond direct upstream use.

These decisions cannot weaken the phase ordering, enforcement truth, resource ownership or transition outcomes defined above. Unsupported combinations must be rejected or explicitly excluded from the release.

## 20. Primary references

References checked for the bootstrap revision on 2026-09-21:

- [Fx lifecycle](https://uber-go.github.io/fx/lifecycle.html): initialization versus execution and lifecycle ordering.
- [Fx value groups](https://uber-go.github.io/fx/value-groups/index.html): group iteration is not an ordering contract.
- [Go net/http](https://pkg.go.dev/net/http): native server, shutdown and connection lifecycle.
- [Go crypto/tls](https://pkg.go.dev/crypto/tls): certificate selection and TLS configuration.
- [Linux Landlock](https://docs.kernel.org/userspace-api/landlock.html): capability negotiation, thread coverage and irreversible restrictions.
- [Go Landlock bindings](https://github.com/landlock-lsm/go-landlock): candidate upstream integration, subject to capability verification.
- [Seccomp BPF library](https://github.com/elastic/go-seccomp-bpf): candidate upstream filter integration.

The existing architecture document retains the dated dependency stack. Implementation must resolve and verify compatible versions; this specification adds no unverified version pin.
