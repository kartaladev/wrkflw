# Security Policy

## Supported versions

`wrkflw` is pre-1.0. Until a `v1.0.0` release, security fixes are applied to the `main` branch and
the most recent tagged release only. See [`STABILITY.md`](STABILITY.md) for the API-stability policy.

## Reporting a vulnerability

**Please do not report security vulnerabilities through public GitHub issues.**

Instead, report them privately by either:

- using GitHub's **["Report a vulnerability"](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing/privately-reporting-a-security-vulnerability)**
  private advisory flow on this repository, or
- emailing **security@kartala.id** (or **zaky@kartala.id**).

Please include:

- a description of the vulnerability and its impact,
- the affected version / commit,
- step-by-step reproduction (a failing test or minimal program is ideal), and
- any suggested remediation.

## What to expect

- We aim to acknowledge a report within **3 business days**.
- We will work with you to confirm the issue, assess severity, and prepare a fix.
- We follow **coordinated disclosure**: please give us a reasonable window to release a fix before
  any public disclosure. We will credit reporters who wish to be named once a fix ships.

## Scope notes for embedders

`wrkflw` is an embeddable library; the consumer owns the deployed surface. A few responsibilities sit
with the embedder and are documented rather than enforced by default:

- **Authorization** of the gRPC service (gate admin RPCs with an interceptor / `NewSecureServer`).
- **TLS** for the database, SMTP, and transport servers.
- **Untrusted definitions** — enable the expression-evaluation timeout (injectable evaluator) before
  loading process definitions from untrusted input.

These and other hardening items are tracked in `docs/plans/2026-06-30-production-readiness-backlog.md`.
