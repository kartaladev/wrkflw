# Raw `time.After` deadlines in tests

`scripts/check-test-timeout.sh` bounds the wall-clock a mass failure in one
package can burn. Until #66 it could only see `require.Eventually` sites passing
`eventuallyBudget`, because it finds them by grepping that identifier. A raw
`select { case <-time.After(d) }` carries no identifier, so 45 of them across 27
files sat outside the count — outside even the script's own stated caveat, which
anticipated an *Eventually site with a literal*, not a different construct.

The guard now sums them into the same per-package ceiling. Run it to see the
current numbers; they are derived from source, never written down here.

## Five constructs wear this shape

`select` with a `time.After` clause is one syntax over several unrelated things.
Classify by **what the deadline clause does**, because that decides who pays for
it and when. Reading the duration instead gets the answer backwards: the ticket
that prompted this work called the 100 ms site "the flake-prone end", and it is
the site in the tree that most needs to stay exactly as short as it is.

| Construct | The deadline clause… | Paid | Wants to be | n |
| --- | --- | --- | --- | --- |
| **Timeout** | fails the test | on FAILURE only | *usually* generous — but see the watchdogs below | 34 |
| **Negative window** | is the benign exit; a sibling clause fails | EVERY GREEN RUN | short | 6 |
| **Drain / loop exit** | is a loop's only exit; nothing fails | EVERY GREEN RUN | short | 1 |
| **Fixture fallback** | returns a value from a test double or helper | rarely, often never | irrelevant | 3 |
| **Asserting branch** | does real work and asserts | when that branch is taken | case by case | 1 |

Those five are exhaustive over the 45 and sum to it. Re-derive rather than trust
the counts: they come from an AST pass classifying each clause body, and the
table is only as current as the last person to run one. Give that pass an
UNCLASSIFIED bucket and read it — the first version of this table had three rows
and was written by hand-filing the sites the classifier could not place, which
put two of them under a heading defined to exclude them. The tool flagged the
right sites and was overruled by prose.

A **negative window** is a hand-rolled `require.Never`. Everything the repo says
about Never budgets applies unchanged: raising one is pure cost on every passing
run, which is why `scheduler/waitbudget_test.go` refuses `eventuallyBudget` for
Never and why the guard excludes Never budgets from its ceiling.

**Seven deadlines sit under one second.** Six are negative windows; the seventh
is the drain loop at `internal/persistence/store/notifier_pgx_test.go:299`, which
is not a negative window — no clause there fails, it is the loop's only exit.
Different construct, same cost profile: both are paid in full on every green run,
and short is correct for both. Do not read "short" as "flake-prone".

The remaining two are neither deadlines on an assertion nor windows. A **fixture
fallback** lives in a test double or helper — a fake blocking action that gives
up after 2 s, or `transport/http/httpcore/resolve_actor_internal_test.go:32`,
whose fallback deliberately converts a hang into a readable failure. An
**asserting branch** (`runtime/human_example_test.go:421`) takes the deadline as
a legitimate control-flow path: it cancels the context and then asserts on the
cancellation outcome, so the deadline is part of what the test exercises.

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
non-blocking receive adds latency and expresses the intent worse. Two sites
say so out loud — `runtime/calllink/notifier_test.go:271` and
`internal/persistence/store/relay_clockdriven_test.go:85`: *"This receive IS the
assertion."*

Two further reasons, either sufficient on its own:

- **It would buy nothing.** The point of converting was to bring these inside the
  guard. The guard now counts them where they are.
- **The defect it would treat is still a hypothesis.** Conversion means adopting
  `eventuallyBudget`, i.e. 2 s → 10 s, across 34 sites. Exactly one of those —
  the fenced `scheduler/elector_test.go:179`, the failure #66 was filed on — has
  ever been observed to fail, once, in CI, and it has never been reproduced. The
  other 33 have not been observed at all. CLAUDE.md's proof rule turns on that
  distinction: a defect is real once a failing test *reproduces* it, and an
  observation is not a reproduction. So the disciplined move is not "never
  widen" — it is to name the fix and hold it until there is something to fix.
  That fix is stated above: a shared, named, generous constant for real-I/O
  timeouts. This defers conversion pending evidence rather than vetoing it, and
  #66 fences that one site off precisely so widening it does not get mistaken
  for fixing it.

  Independently of that, several sites are hang watchdogs whose duration bounds
  an algorithmic property — `scheduler/trigger_test.go:749`, *"the scan is walking
  off-grid months day by day"* — where widening actively weakens the test, with or
  without a reproduction.

Converting would also have shrunk the guard's population toward the vacuity this
repo keeps having to reject: a check whose subject matter has been refactored out
from under it passes from day one and proves nothing.

## The literal-budget gap, closed

#66 left one hole and recorded it here: the guard found Eventually sites by
grepping the `eventuallyBudget` identifier, so an Eventually passing a bare
literal was in no ceiling at all — neither an identifier site nor a raw
`time.After`. **#99 closed it**: 18 such sites across 8 packages are now counted,
and this section's previous text — "still open, owned by nobody" — is superseded.

Finding them needed something the earlier grep could not do. A literal budget has
no distinctive token; what identifies it is *which call it belongs to*. An
adjacency regex over `<duration>, <duration>,` also matches
`schedule.EveryRandom(5*time.Second, 5*time.Second)`, which is not a wait budget
at all. So the guard carries one bit of state — the wait call most recently
opened — and attributes the next literal budget to it. That attribution is also
what finally lets it honour the long-standing rule that `Never` budgets stay out
of this ceiling: they are recognised and discarded rather than being
indistinguishable.

Because attribution is a heuristic, the guard reconciles: it counts Eventually
*calls* independently and fails if the budgets it found do not cover them. A
budget it cannot read — a named local, a computed expression — stops the build
instead of quietly leaving the ceiling.

## Related

- `docs/agents/eventually-waits.md` — adjacent and disjoint. That one asks
  *"did you wait for the right thing?"*; this one asks *"did you wait long
  enough, and is the sum bounded?"* Neither subsumes the other, and #66 and #86
  were deliberately kept apart.
- `scripts/check-test-timeout.sh` — the guard, and its own statement of scope.
