# Raw `time.After` deadlines in tests

`scripts/check-test-timeout.sh` bounds the wall-clock a mass failure in one
package can burn. Until #66 it could only see `require.Eventually` sites passing
`eventuallyBudget`, because it finds them by grepping that identifier. A raw
`select { case <-time.After(d) }` carries no identifier, so 45 of them across 27
files sat outside the count — outside even the script's own stated caveat, which
anticipated an *Eventually site with a literal*, not a different construct.

The guard now sums them into the same per-package ceiling. Run it to see the
current numbers; they are derived from source, never written down here.

## Three constructs wear this shape

`select` with a `time.After` clause is one syntax covering three unrelated
things. Reading the duration without the construct gets the answer backwards —
the issue that prompted this work called the 100 ms site "the flake-prone end",
and it is the one site in the tree that most needs to stay exactly as short as
it is.

| Construct | Shape | Paid | Wants to be |
| --- | --- | --- | --- |
| **Timeout** | the `time.After` clause calls `t.Fatal` | on FAILURE only | generous |
| **Negative window** | a sibling clause calls `t.Fatal`; the deadline is the benign exit | on EVERY GREEN RUN | short |
| **Fixture fallback** | inside a test double or helper, not gating an assertion | usually never | irrelevant |

A **negative window** is a hand-rolled `require.Never`. Everything the repo
already says about Never budgets applies to it unchanged: raising one is pure
cost on every passing run, which is why `scheduler/waitbudget_test.go` refuses to
use `eventuallyBudget` for Never, and why the guard excludes Never budgets from
its ceiling. Every short raw deadline in this tree (100–500 ms) is a negative
window, and short is correct there.

A **fixture fallback** is not a deadline on an assertion at all — a fake blocking
action that gives up after 2 s, or a resolver whose fallback converts a hang into
a readable failure. Nothing to bound.

## What the guard can and cannot do

It bounds deadlines from **above**: keep the sum small enough that a mass failure
still prints assertion messages instead of `panic: test timed out`.

The flake that prompted #66 was a deadline too small from **below** — 3 s of real
wall-clock against container I/O under CI load. **A ceiling cannot ask "is this
one deadline generous enough?", only "is the sum small enough?"** Those are
opposite directions, and no widening of this script reaches the second one. If
that flake recurs with a reproduction, the answer is a sizing convention for
real-I/O timeouts (a shared, named, generous constant), not a tighter ceiling.

## Why these were not converted to `require.Eventually`

#66 offered conversion as an option. It was declined for all 45, on one
structural reason plus two situational ones.

**A `select` waits on a channel operation; `Eventually` polls a condition.** That
is a language-level fact, not a survey result — it holds for every site without
reading any of them. A channel delivery is a *signal*: it wakes the receiver
immediately and often carries the value being asserted on
(`case err := <-done: assert.ErrorIs(...)`). Rewriting a signal as a polled
non-blocking receive adds latency and expresses the intent worse. One site says
so out loud: *"This receive IS the assertion."*

Two further reasons, either sufficient on its own:

- **It would buy nothing.** The point of converting was to bring these inside the
  guard. The guard now counts them where they are.
- **It would be an unreproduced widening.** Conversion means adopting
  `eventuallyBudget`, i.e. 2 s → 10 s. #66 explicitly declines to widen
  `elector_test.go`'s 3 s for want of a reproduction; routing the same widening
  through a refactor does not make it better evidenced. Several sites are hang
  watchdogs whose duration bounds an algorithmic property — *"the scan is walking
  off-grid months day by day"* — where widening actively weakens the test.

Converting would also have shrunk the guard's population toward the vacuity this
repo keeps having to reject: a check whose subject matter has been refactored out
from under it passes from day one and proves nothing.

## Related

- `docs/agents/eventually-waits.md` — adjacent and disjoint. That one asks
  *"did you wait for the right thing?"*; this one asks *"did you wait long
  enough, and is the sum bounded?"* Neither subsumes the other, and #66 and #86
  were deliberately kept apart.
- `scripts/check-test-timeout.sh` — the guard, and its own statement of scope.
