# Security Policy

## Supported versions

Hanami is currently pre-v1. Security fixes are applied to the latest commit on the default branch and to the latest published release when releases exist.

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability.

Use GitHub private vulnerability reporting for this repository. Include:

- affected package and version or commit;
- operating system, architecture, and kernel version where relevant;
- configuration needed to reproduce the issue;
- a minimal reproduction;
- the security impact;
- whether the issue affects core bootstrap, managed HTTP generations, or Linux security enforcement.

Avoid including live credentials, tokens, private keys, DSNs, or user data.

## Security scope

Security-sensitive areas include:

- `security/linux`: Landlock, seccomp, re-exec handoff, and descriptor inheritance;
- `http`: candidate verification, TLS trust, promotion, draining, and hijacked connections;
- `ownership`: exclusive resource acquisition and release;
- `hanami.Run`: startup ordering, admission, shutdown, and cleanup ownership.

A capability query alone is not accepted as proof that process security is enforced. Reports that demonstrate a denied operation succeeding under a required policy are treated as high priority.
