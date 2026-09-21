## Problem

Describe the concrete consumer problem or contract violation.

## Decision

Describe the implementation and why this boundary belongs in Hanami.

## Verification

List exact commands and runtime scenarios exercised.

## Checklist

- [ ] Public API uses standard or upstream types where possible.
- [ ] Optional dependencies stay outside the root module.
- [ ] Resource ownership and cleanup are explicit.
- [ ] Startup and shutdown failure behavior is covered.
- [ ] `CGO_ENABLED=0` build remains supported.
- [ ] Documentation and runnable examples are updated when the public contract changes.
