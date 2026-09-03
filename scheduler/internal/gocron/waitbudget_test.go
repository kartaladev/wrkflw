package gocron_test

import "time"

// eventuallyBudget is the ceiling every require.Eventually in this package waits
// before declaring failure. It is a FAILURE ceiling, not an expected latency: a
// green run returns as soon as its condition holds — typically microseconds — so
// sizing this for the worst contended CI machine costs nothing on the passing
// path, and only a test that was already going to fail waits it out.
//
// It replaces per-site literals of 1–3 s. Those made
// TestGocronScheduleJobTriggers/"At (past-due) fires immediately" fail under
// full-suite -race contention while passing -count=25 in isolation:
// that job fires via gocron.OneTimeJobStartImmediately on a REAL-time goroutine,
// so the fake clock does not bound it and one second of wall time is a bet on
// machine load rather than an assertion about the code.
//
// ⚠ Deliberately NOT used for Never (require.Never OR assert.Never). A Never
// budget is an observation window paid in full on every GREEN run, so raising it
// is pure cost.
//
// ⚠ SIZED AGAINST THE BINARY, NOT THE SITE. go test's default timeout is 600s
// per binary and these sites are predominantly SERIAL (2 of the 31 run under
// t.Parallel), so budget × site count is an UPPER BOUND, not an exact figure —
// parallelism only lowers the real wall-clock sum. scheduler/internal/gocron
// carries 31 of the 40 sites, and the realistic red is systemic (every site
// shares one scheduler), so a mass failure costs budget × 31. At 30s that is
// 930s — the binary dies with "panic: test timed out" and a goroutine dump
// printing NO assertion messages. At 10s it is 310s, inside the timeout, with
// every broken site still naming itself. Rule: budget × the densest package's
// site count must stay under 600s.
const eventuallyBudget = 10 * time.Second
