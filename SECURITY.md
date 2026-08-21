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

- **Authorization** of the admin HTTP routes. Admin endpoints are default-absent by
  composition (ADR-0095): they exist only when you mount `AdminRoutes` on a router group
  that your own auth middleware already protects. They carry no built-in authentication.
- **TLS** for the database, SMTP, and transport servers.
- **Untrusted definitions** — enable the expression-evaluation timeout (injectable evaluator) before
  loading process definitions from untrusted input.

## Request body limits (ADR-0186)

Every HTTP route group mounted from `transport/http/{stdlib,gin,fiber}` bounds the request body at
**1 MiB by default**. Set your own with the adapter option, or pass a **non-positive** value to
disable the bound entirely:

```go
stdlib.Mount(mux, svc, stdlib.WithMaxBodyBytes(4<<20))  // 4 MiB
stdlib.Mount(mux, svc, stdlib.WithMaxBodyBytes(0))      // disabled
```

When the cap is active, the body read is also bounded in **time** — **30 s by default**, and a
non-positive value disables it:

```go
stdlib.Mount(mux, svc, stdlib.WithBodyReadTimeout(10*time.Second))
```

⚠ **This deadline overwrites the whole-request deadline `net/http` derives from
`http.Server.ReadTimeout`** for the duration of the body read. If you set a *shorter*
`ReadTimeout`, it is silently extended to `BodyReadTimeout` while the body is being read — keep
`ReadTimeout` no shorter than `BodyReadTimeout`. There is no `fiber` equivalent: fasthttp has
already read the body before the route group is entered.

Oversize requests are answered **413** with a static body that deliberately does **not** name the
configured limit. The bound applies to what is read from the wire, and each adapter's JSON decoding
is otherwise unchanged.

Five properties are documented rather than enforced. Read them before relying on the cap:

1. **The cap bounds SIZE; `BodyReadTimeout` bounds TIME — and you should set `ReadTimeout` too.**
   The body is read to completion before it is parsed, so without a deadline a slow client holds a
   handler open indefinitely. Measured: a request declaring `Content-Length: 400000` that sends a
   complete JSON value and then stalls returned in **0 s** before this change and **never returned**
   after it. That is why `BodyReadTimeout` defaults to **30 s** on `stdlib` and `gin` rather than
   being left to documentation. ⚠ It is **not** a substitute for `http.Server.ReadTimeout`, which
   also covers requests whose body the cap never wraps — all three `examples/` now set both.
2. **Peak memory is per-adapter and is NOT `MaxBodyBytes × in-flight`.** On `stdlib` and `gin` it is
   roughly **2.12 × the cap** per in-flight request (buffer growth), **including for requests that
   are ultimately rejected**. On `fiber` it is governed by **`fiber.Config.BodyLimit`** (default
   4 MiB), not by `MaxBodyBytes` at all, because fasthttp reads and limits the body before the route
   group is entered — ⚠ and for a **compressed** body, roughly **twice** that, because the size
   pre-check and the JSON binder each decompress once. Nothing here bounds concurrency.
3. **A 413 carries no correlation id and writes no log record.** Error-body correlation and 4xx
   logging are a separate, later delivery; today `writeErr` logs only at `status >= 500`.
4. **The cap is per route group, so pass the option to every group you mount.** `Mount` covers the
   instance, message and task groups — **6 of the 13 decode sites per adapter**. The remaining
   sites, **including the optional-body admin resolve-incident route**, live behind `AdminRoutes`,
   which ADR-0095 keeps out of `Mount` by design. `MountHealth` forwards no options.
   ```go
   stdlib.Mount(mux, svc, stdlib.WithMaxBodyBytes(n))
   stdlib.AdminRoutes{Svc: svc}.Customize(mux, stdlib.WithMaxBodyBytes(n))  // do this too
   ```
5. **On `fiber`, a body whose WIRE size exceeds `fiber.Config.BodyLimit` never reaches the route
   group.** fasthttp refuses it first, so the client receives a framework `text/plain` response with
   **no `ErrorBody` envelope, no correlation id and no log line**. Raising `fiber.Config.BodyLimit`
   above your cap restores identical behaviour across all three adapters — measured.
   ⚠ **A body whose DECOMPRESSED size exceeds `BodyLimit` does reach the route group**, and is
   answered **413 with a normal `ErrorBody`** — `fiber` sets that status itself during decoding and
   the adapter now preserves it instead of overwriting it with a 400.

⚠ **A known, pre-existing divergence, unrelated to the cap:** a body containing a complete JSON
value followed by trailing bytes is accepted by `stdlib` and `gin` (`json.Decoder` stops at the
first value) and rejected with **400** by `fiber` (`Bind().JSON` uses `json.Unmarshal`, which is
strict). This predates ADR-0186 and is now pinned by the parity suite.

These and other hardening items are tracked in `docs/plans/2026-06-30-production-readiness-backlog.md`.
