# Contributing

## Development setup

Requirements:

- Go 1.27.1 or later
- Linux for runtime tests under `security/linux`
- A C compiler only for race tests

Clone the repository and run:

```sh
make test
make vet
make test-cgo
make build
```

Run the race matrix before submitting a change:

```sh
make race
```

## Repository modules

Hanami is a multi-module repository. The root module contains the shared bootstrap, lifecycle, health, HTTP, ownership, logging, process, and Linux security packages. Optional integrations use nested modules.

When an import crosses a module boundary, update the relevant nested `go.mod` and run `go mod tidy` from that module directory. Do not add an optional integration dependency to the root module.

## Design rules

- Use upstream types instead of parallel framework abstractions.
- Keep application services independent of Gin, Huma, Fx, and Kong types.
- Do not create a second dependency injection container.
- Do not use Fx value group order for middleware, migrations, startup, or shutdown.
- One acquired resource has one close owner.
- Required security capabilities fail closed.
- Do not use `InsecureSkipVerify` for managed server probes.
- Official modules must build with `CGO_ENABLED=0`.

## Tests

Test framework-owned behavior and failure semantics, not upstream library internals. Security enforcement and managed HTTP transitions require subprocess or integration tests when an in-process unit test cannot prove the contract.

## Commit messages

Use Conventional Commits:

```text
feat(http): add generation inspection
fix(security): reject stale handoff policy
```
