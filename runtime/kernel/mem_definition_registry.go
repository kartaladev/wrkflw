package kernel

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/kartaladev/wrkflw/definition/model"
)

// ── Sentinel errors ────────────────────────────────────────────────────────

// ErrNilDefinition is returned by MemDefinitionRegistry.Register when the
// supplied definition pointer is nil.
var ErrNilDefinition = errors.New("workflow-runtime: nil definition")

// ErrEmptyDefinitionID is returned by MemDefinitionRegistry.Register when the
// supplied definition has an empty ID field.
var ErrEmptyDefinitionID = errors.New("workflow-runtime: empty definition ID")

// ErrDefinitionExists is returned by MemDefinitionRegistry.Register when a
// definition with the same Qualifier (ID+Version) key has already been registered
// (first-registration-wins on the versioned key).
var ErrDefinitionExists = errors.New("workflow-runtime: definition already registered")

// ErrInvalidDefinition is returned by MemDefinitionRegistry.Register when the
// supplied definition fails [model.Validate]. The returned error wraps both this
// sentinel and every rule the definition broke, so callers may match either
// ErrInvalidDefinition or a specific rule (e.g. [model.ErrNoStartEvent]) with
// errors.Is.
var ErrInvalidDefinition = errors.New("workflow-runtime: invalid definition")

// MaxDefinitionIDRunes is the longest definition ID that is publishable on
// every supported backend, measured in RUNES rather than bytes.
//
// It is the narrowest of the three durable schemas. MySQL declares
// def_id VARCHAR(255) and the migration sets no explicit CHARSET, so the column
// takes the server default (utf8mb4) and VARCHAR(255) bounds it at 255
// CHARACTERS. Postgres and SQLite both use unbounded TEXT. Taking the smallest
// limit is what makes the gate's promise dialect-independent: an ID that
// validates here is storable on all three, so the same definition is
// publishable everywhere.
//
// Runes, not bytes, for the same reason. A byte bound would reject multibyte
// IDs that MySQL accepts perfectly well — utf8mb4 VARCHAR(255) holds 255
// characters regardless of how many bytes they occupy — so len() would make the
// gate stricter than the storage it is protecting.
const MaxDefinitionIDRunes = 255

// ErrDefinitionIDTooLong is returned by [ValidateDefinition] when def.ID is
// longer than [MaxDefinitionIDRunes]. It is always wrapped together with
// [ErrInvalidDefinition], so callers may match either.
//
// This is a prevention gate, not a cosmetic limit. It is one of two defences:
// the durable store's MySQL statement also refuses an over-long value loudly
// (see dialect.Dialect.InsertIgnoreDefinition), so the row can no longer be
// silently truncated there. What this gate adds is PARITY and timing — every
// backend rejects the same IDs, before any I/O, with one matchable sentinel,
// rather than one backend surfacing a dialect-specific SQL error and the others
// accepting happily.
var ErrDefinitionIDTooLong = errors.New("workflow-runtime: definition ID too long")

// ErrDefinitionIDNotUTF8 is returned by [ValidateDefinition] when def.ID is not
// valid UTF-8. Always wrapped together with [ErrInvalidDefinition].
//
// Go strings may hold arbitrary bytes, and an ID assembled from a file, a
// network payload or a []byte conversion can carry invalid sequences. The three
// backends then disagree completely — measured, see [ValidateDefinition]:
// Postgres refuses the write outright (SQLSTATE 22021), MySQL rejects it with
// Error 1366, and SQLite stores the raw bytes and accepts it. Refusing it here
// is what makes that uniform.
//
// The MySQL half of this was worse before the store's statement stopped using
// INSERT IGNORE, which downgraded the rejection to a warning and truncated at
// the first bad byte: two distinct IDs sharing a prefix up to their first bad
// byte became ONE row, so a lookup for one identity returned another's
// definition with no error. Recorded because it is why the bound exists.
var ErrDefinitionIDNotUTF8 = errors.New("workflow-runtime: definition ID is not valid UTF-8")

// ErrDefinitionIDContainsNUL is returned by [ValidateDefinition] when def.ID
// contains a NUL byte. Always wrapped together with [ErrInvalidDefinition].
//
// NUL is perfectly valid UTF-8, so [ErrDefinitionIDNotUTF8] does not catch it,
// and it needs its own check. Measured: Postgres cannot store NUL in a text
// column at all and rejects it with SQLSTATE 22021, while MySQL and SQLite
// accept it — so an ID containing one is publishable on two backends and
// impossible on the third.
var ErrDefinitionIDContainsNUL = errors.New("workflow-runtime: definition ID contains a NUL byte")

// MaxDefinitionVersion is the largest version publishable on every supported
// backend.
//
// The version column is INT on both MySQL and Postgres — 32-bit — while Go's
// ProcessDefinition.Version is an int, 64-bit on every platform this runs on.
// SQLite's INTEGER is 64-bit and stores anything. So the narrowest backend sets
// the bound, exactly as it does for the ID length.
//
// Measured at 2147483648 (one over): SQLite stores it happily, Postgres refuses
// to encode it for an int4 parameter, and MySQL reports it out of range
// (Error 1264). Refusing it here is what makes the three agree.
//
// Before the store's statement stopped using INSERT IGNORE, MySQL's rejection
// was downgraded to a warning and the value CLAMPED to 2147483647: the publish
// returned nil and the row landed at a version the caller never asked for, so a
// read at the requested version found nothing. Recorded because it is why the
// bound exists.
const MaxDefinitionVersion = math.MaxInt32

// ErrDefinitionVersionTooLarge is returned by [ValidateDefinition] when
// def.Version exceeds [MaxDefinitionVersion]. Always wrapped together with
// [ErrInvalidDefinition]. The lower bound is model.Validate's business:
// it already refuses Version < 1.
var ErrDefinitionVersionTooLarge = errors.New("workflow-runtime: definition version too large")

// ── The shared authoring gate ─────────────────────────────────────────────

// ValidateDefinition is the authoring gate every definition passes through
// before it is admitted to a registry or published to the durable store. It
// returns:
//   - [ErrNilDefinition] if def is nil.
//   - [ErrEmptyDefinitionID] if def.ID is empty.
//   - [ErrDefinitionIDTooLong], [ErrDefinitionIDNotUTF8],
//     [ErrDefinitionIDContainsNUL] or [ErrDefinitionVersionTooLarge] — each
//     wrapped with [ErrInvalidDefinition] — if def.ID or def.Version falls
//     outside what every supported backend stores faithfully. See
//     "# The storable domain" below.
//   - [ErrInvalidDefinition], wrapped together with the qualifier and every
//     rule def broke, if def fails [model.Validate]. Callers may match either
//     this sentinel or a specific rule (e.g. [model.ErrNoStartEvent],
//     [model.ErrInvalidVersion]) with errors.Is.
//   - nil if def is admissible.
//
// [github.com/kartaladev/wrkflw/engine.Step] assumes the definition it is given
// has passed [model.Validate]. The builder and the YAML loader both end in that
// call, but a hand-constructed *model.ProcessDefinition literal has not — so
// every door into the system runs this gate, and they all run the SAME one:
// [MemDefinitionRegistry.Register] and the durable store's PublishDefinition
// both delegate here, so an in-memory registration and a durable publish accept
// and reject exactly the same definitions.
//
// A Version of 0 needs no separate check: it is the "latest" sentinel and
// [model.Validate] already refuses it with [model.ErrInvalidVersion].
//
// # The storable domain
//
// The checks on def.ID and def.Version are not cosmetic hygiene. Each one
// closes a case where the three supported backends do NOT agree, established by
// publishing hostile values against real Postgres, MySQL and SQLite rather than
// by reading their documentation:
//
// Measured through the CURRENT statement — the definitions site no longer uses
// INSERT IGNORE, so MySQL rejects rather than silently altering:
//
//	property              SQLite        Postgres            MySQL
//	----------------------------------------------------------------------
//	>255 runes            stores        stores              rejects (1406)
//	invalid UTF-8         stores raw    rejects (22021)     rejects (1366)
//	NUL byte              stores        rejects (22021)     stores
//	Version > MaxInt32    stores        rejects (int4)      rejects (1264)
//
// Every row still shows at least two backends disagreeing, which is the point:
// SQLite accepts three of the four that the others refuse. Under the previous
// INSERT IGNORE statement the MySQL column read TRUNCATES / TRUNCATES / stores
// / CLAMPS — silently, and reported as success — which is the history that
// motivated these bounds.
//
// The MySQL column is the narrowest schema in every row. Each of those
// disagreements is independently defended in the durable store, whose
// definitions statement suppresses only the duplicate key and so lets MySQL
// reject a bad value loudly (see dialect.Dialect.InsertIgnoreDefinition);
// before that change INSERT IGNORE downgraded every one of them to a warning
// and the write reported success while storing something the caller did not
// ask for.
//
// This gate is the other half, and it is the half that gives PARITY: rejecting
// the input here, before any I/O, is what makes the two promises it carries
// true — that an in-memory registration and a durable publish accept exactly
// the same definitions, and that an ID and version publishable on one backend
// are publishable on all of them. A store-side error alone would satisfy
// neither, because SQLite would still accept what MySQL refused.
//
// Scope: these bounds cover the two KEY columns, not the whole definition. The
// body is stored as JSON and has its own, narrower domain that is not gated
// here — a NUL byte inside a node name stores on MySQL and SQLite and is
// refused by Postgres with SQLSTATE 22P05. That fails closed (the publish
// errors; nothing altered is stored), so it is documented rather than
// enforced.
//
// A known divergence this gate CANNOT close: MySQL's def_id collation is
// utf8mb4_0900_ai_ci, which folds case, accent, WIDTH and NORMALISATION FORM.
// So "a"/"A", "resume"/"résumé", halfwidth "A"/fullwidth "Ａ", and NFC "é"
// versus NFD "e"+U+0301 are each ONE primary key on MySQL and two distinct
// keys on Postgres and SQLite. (Trailing whitespace is not affected — MySQL 8
// default collations are NO PAD.)
//
// That is a property of a PAIR of IDs, not of any single one, so no per-value
// check can detect it, and case-folding alone does not mitigate it. See the
// note on DefinitionStore.PublishDefinition.
//
// It is exported so a consumer can run the same check itself — before a publish,
// or in a test — instead of discovering the rejection at the write.
//
// [MapDefinitionRegistry] is the one registry that cannot enforce this — its
// variadic constructor returns no error — so a caller assembling one owns
// validation itself.
func ValidateDefinition(def *model.ProcessDefinition) error {
	if def == nil {
		return ErrNilDefinition
	}
	if def.ID == "" {
		return ErrEmptyDefinitionID
	}
	// The storable domain, checked before anything else looks at the
	// definition. Encoding is checked before length because a rune count over
	// invalid UTF-8 is not meaningful.
	if !utf8.ValidString(def.ID) {
		return fmt.Errorf("%w: %w", ErrInvalidDefinition, ErrDefinitionIDNotUTF8)
	}
	if strings.ContainsRune(def.ID, 0) {
		return fmt.Errorf("%w: %w", ErrInvalidDefinition, ErrDefinitionIDContainsNUL)
	}
	if n := utf8.RuneCountInString(def.ID); n > MaxDefinitionIDRunes {
		return fmt.Errorf("%w: %w: %d runes exceeds the %d-rune limit",
			ErrInvalidDefinition, ErrDefinitionIDTooLong, n, MaxDefinitionIDRunes)
	}
	if def.Version > MaxDefinitionVersion {
		return fmt.Errorf("%w: %w: %d exceeds the maximum of %d",
			ErrInvalidDefinition, ErrDefinitionVersionTooLarge, def.Version, MaxDefinitionVersion)
	}
	if err := model.Validate(def); err != nil {
		return fmt.Errorf("%w: %q: %w", ErrInvalidDefinition, def.Qualifier(), err)
	}
	return nil
}

// ── MemDefinitionRegistry ─────────────────────────────────────────────────

// MemDefinitionRegistry is a concurrency-safe, register-after-construction
// in-memory DefinitionRegistry. It is the mutable sibling of the immutable
// MapDefinitionRegistry; use it when definitions are registered incrementally
// (e.g. the process-global default populated at application init).
//
// Register indexes each definition under two keys:
//   - def.Qualifier()     — exact versioned key; first-registration-wins.
//   - model.Latest(def.ID) — latest key; held by the HIGHEST registered version,
//     so a Latest Qualifier always resolves the newest version rather than
//     whichever one happened to be registered last.
//
// # Concurrency
//
// All methods are safe for concurrent use. Never copy a MemDefinitionRegistry
// after first use — it contains a sync.RWMutex that must not be copied.
type MemDefinitionRegistry struct {
	mu sync.RWMutex
	m  map[model.Qualifier]*model.ProcessDefinition
}

// NewMemDefinitionRegistry returns an empty, ready-to-use MemDefinitionRegistry.
func NewMemDefinitionRegistry() *MemDefinitionRegistry {
	return &MemDefinitionRegistry{
		m: make(map[model.Qualifier]*model.ProcessDefinition),
	}
}

// Register indexes def under both its pinned Qualifier and its latest Qualifier.
// It returns:
//   - whatever [ValidateDefinition] returns — [ErrNilDefinition],
//     [ErrEmptyDefinitionID] or [ErrInvalidDefinition] — if def fails the
//     shared authoring gate.
//   - [ErrDefinitionExists] (wrapped with the pinned key) if the exact
//     Qualifier was already registered (first-registration-wins on the
//     versioned key).
//
// The gate runs before the lock and before any indexing, so a rejected
// definition claims neither key. It is the same [ValidateDefinition] the
// durable store's PublishDefinition runs, so both doors admit exactly the same
// definitions.
//
// On success the latest key is moved to def only when def.Version is greater
// than or equal to the version currently holding that key, so a Lookup with a
// Latest Qualifier resolves the highest registered version. Registering an
// older version after a newer one therefore does not demote "latest".
//
// This matches [MapDefinitionRegistry] and the durable
// DefinitionStore.Lookup, both of which already resolve Latest by highest
// version: "latest" means the same thing on every registry.
func (r *MemDefinitionRegistry) Register(def *model.ProcessDefinition) error {
	// The authoring gate: engine.Step's contract assumes model.Validate has run,
	// and a struct literal is the one route that would otherwise skip it.
	// Checked outside the lock — the gate reads def only.
	if err := ValidateDefinition(def); err != nil {
		return err
	}

	pinned := def.Qualifier()
	latest := model.Latest(def.ID)

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.m[pinned]; exists {
		return fmt.Errorf("%w: %q", ErrDefinitionExists, pinned)
	}

	r.m[pinned] = def
	// Highest-version-wins for the latest key, mirroring MapDefinitionRegistry.
	//
	// ">=" and ">" are behaviourally identical here: equality would require the
	// same ID and the same version, i.e. the same pinned qualifier, and the
	// duplicate check above has already returned ErrDefinitionExists for that.
	// The form is kept only to match MapDefinitionRegistry's line, which has no
	// such preceding check and where the equality case IS reachable.
	if cur, ok := r.m[latest]; !ok || def.Version >= cur.Version {
		r.m[latest] = def
	}

	return nil
}

// MustRegister calls Register and panics if it returns an error. Intended for
// init-time wiring where a registration failure is a programming error.
func (r *MemDefinitionRegistry) MustRegister(def *model.ProcessDefinition) {
	if err := r.Register(def); err != nil {
		panic(fmt.Sprintf("kernel.MemDefinitionRegistry.MustRegister: %v", err))
	}
}

// Lookup implements [DefinitionRegistry]. It returns the ProcessDefinition
// registered under q, or ([ErrDefinitionNotFound], nil) when no definition
// matches. ctx is ignored — the lookup is entirely in-memory.
//
// q may be either:
//   - model.Latest(id)        — resolves the highest registered version.
//   - model.Version(id, v)    — resolves the exact versioned registration.
func (r *MemDefinitionRegistry) Lookup(_ context.Context, q model.Qualifier) (*model.ProcessDefinition, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	def, ok := r.m[q]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrDefinitionNotFound, q)
	}
	return def, nil
}

// ListDefinitions implements [DefinitionLister]. It returns each registered
// definition exactly once, even though Register indexes every definition
// under two Qualifier keys (pinned and latest) — dedupe is by concrete
// *model.ProcessDefinition pointer, not by map key. ctx is ignored — the
// enumeration is entirely in-memory.
func (r *MemDefinitionRegistry) ListDefinitions(context.Context) []*model.ProcessDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return distinctDefinitions(r.m)
}

// Compile-time assertions: MemDefinitionRegistry satisfies DefinitionRegistry
// and DefinitionLister.
var _ DefinitionRegistry = (*MemDefinitionRegistry)(nil)
var _ DefinitionLister = (*MemDefinitionRegistry)(nil)
