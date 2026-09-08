package parity_test

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// transportRoot holds the HTTP adapter packages, relative to this package's own
// directory — which is the working directory `go test` runs in.
const transportRoot = ".."

// tableAdapters and tableEntryPoints are what TestSecurityNoteParity asserts
// about. TestSecurityNoteCoverage asserts that these two lists are not merely A
// set of things worth checking but THE set that exists, so a new adapter or a
// new entry point cannot arrive unnoticed.
//
// The order of tableAdapters is iteration order and nothing more. There is NO
// reference adapter: every adapter is compared against the authored note
// literal, and that shared literal IS the cross-adapter equality check,
// expressed through a common reference rather than pairwise.
//
// Earlier pairwise stdlib-vs-gin and stdlib-vs-fiber assertions were removed
// because they added no detection — mutating all three adapters identically
// fires the authored-note assertion and no pairwise one — while actively
// misleading: testify binds the first argument to "expected", so a mutation in
// stdlib rendered an INVERTED diff presenting the corrupted stdlib text as
// correct and the untouched gin and fiber as the deviation. A maintainer
// reading top-down was told, in machine-readable diff form, to propagate the
// corruption.
var (
	tableAdapters = []string{"stdlib", "gin", "fiber"}

	tableEntryPoints = []string{
		"InstanceRoutes", "MessageRoutes", "TaskRoutes", "AdminRoutes",
		"HealthRoutes", "Mount", "MountHealth",
	}
)

// TestSecurityNoteParity asserts that every mountable entry point in every HTTP
// adapter carries a SECURITY: block in its godoc, and that the block is
// byte-identical across the three adapters.
//
// Why this belongs in the parity package rather than in each adapter's own
// tests: "the same warning reaches every consumer" is precisely a cross-adapter
// claim. A per-adapter test would let one adapter's note drift — or vanish —
// without failing anything, and the drift is invisible in review because the
// three files are never read side by side.
//
// Why the doc comment and not the code: the trust boundary this repository draws
// is that authentication is the CONSUMER's job. There is no code to assert on,
// because the deliberate design is that no code runs. The godoc is the whole of
// the artifact, so the godoc is what gets pinned.
//
// The comparison is on the SECURITY: block ALONE, never the whole doc comment.
// The three adapters' surrounding prose differs on purpose — fiber lists its
// routes as an indented block, gin carries section separators, stdlib writes
// sentences — and whole-comment equality would go red on prose this test has no
// mandate to change.
//
// Honest framing of what is red and what is a pin: four of the five route groups
// carried no SECURITY: block on any adapter before this test, so those twelve
// (type, adapter) pairs are a genuine red-to-green fix. The AdminRoutes arm
// passed from its very first run — its note already existed and was already
// byte-identical on all three adapters — so that arm is a PIN against
// regression, not a fix. It is also what kept the identical-across-adapters
// assertion non-vacuous on day one. The same distinction is drawn in
// engine/terminal_sites_test.go:14-18 about itself.
//
// No context modifier and no cancellation case: the subject is a parse of source
// files on disk. Nothing here takes a context.Context, so there is no lifecycle
// path to exercise.
func TestSecurityNoteParity(t *testing.T) {
	t.Parallel()

	adapters := discoverAdapters(t)

	type testCase struct {
		name   string
		decl   string
		assert func(t *testing.T, blocks map[string]string)
	}

	cases := []testCase{
		{
			name: "InstanceRoutes",
			decl: "InstanceRoutes",
			assert: func(t *testing.T, blocks map[string]string) {
				assertIdenticalNote(t, blocks, `SECURITY: these routes have NO built-in authentication and NO per-instance
access control. Any caller that reaches them may start, read and signal ANY
instance whose ID it can name, and instance IDs are enumerable by design
rather than secret.

Identity LIFTS the redaction on these reads rather than gating them. A caller
the transport cannot identify receives a structural projection -- IDs, status,
timestamps, history, token and task state. An identified caller receives
everything: variables, start variables, scopes, incidents, compensation
records, and each task's claim, completion and candidates -- for ANY instance
ID. Mounting these onto an authenticated group, which you should still do,
therefore WIDENS what every logged-in caller can read. No owner or tenant
check exists below this seam; add one yourself.`)
			},
		},
		{
			name: "MessageRoutes",
			decl: "MessageRoutes",
			assert: func(t *testing.T, blocks map[string]string) {
				assertIdenticalNote(t, blocks, `SECURITY: this route has NO built-in authentication and NO authorization.
Any caller that reaches it may deliver a message to ANY instance waiting on
one, and so may drive an instance forward without ever naming itself. A
message is routed by name and correlation key, neither of which is a secret
or a credential. Mount MessageRoutes only onto a router group your own auth
middleware already protects; nothing below this seam checks who the caller
is.`)
			},
		},
		{
			name: "TaskRoutes",
			decl: "TaskRoutes",
			assert: func(t *testing.T, blocks map[string]string) {
				assertIdenticalNote(t, blocks, `SECURITY: these routes have NO built-in authentication. They require an
actor identity, which they take from the configured RequestActor, and they
authorize that actor against the task's eligibility rule ONLY.

"Requires an identity" is weaker than it sounds. Only the wholly zero actor
is refused, so the kiosk claimant -- roles but no ID -- is admitted here and
may claim and complete tasks, while the instance reads treat that same caller
as UNIDENTIFIED and hand it the redacted projection. An actor can act without
being able to see. Reassign authorizes the reassigner, never the person
reassigned to, and nothing here establishes that an identity is genuine.
Mount TaskRoutes only onto a router group your own auth middleware already
protects, which is what makes the actor trustworthy.`)
			},
		},
		{
			// The one arm that was green on the first run. Its text is the
			// existing note, reproduced byte for byte and deliberately not
			// reworded: this arm exists to hold the line, and rewriting it
			// would forfeit the only cross-adapter evidence the test had
			// before the other notes were written.
			name: "AdminRoutes",
			decl: "AdminRoutes",
			assert: func(t *testing.T, blocks map[string]string) {
				assertIdenticalNote(t, blocks, `SECURITY: these routes have NO built-in authentication. Mount AdminRoutes only
onto a router group already protected by your auth middleware (admin-by-
composition); otherwise the admin endpoints are exposed unauthenticated.`)
			},
		},
		{
			name: "HealthRoutes",
			decl: "HealthRoutes",
			assert: func(t *testing.T, blocks map[string]string) {
				assertIdenticalNote(t, blocks, `SECURITY: these routes have NO built-in authentication. The /readyz body
names every configured check and reports which of them is unavailable, so it
discloses your dependency topology to any caller that reaches it. Mount
HealthRoutes on an internal listener, or onto a protected group, whenever
that disclosure matters.`)
			},
		},
		{
			name: "Mount",
			decl: "Mount",
			assert: func(t *testing.T, blocks map[string]string) {
				assertIdenticalNote(t, blocks, `SECURITY: Mount registers InstanceRoutes, TaskRoutes and MessageRoutes in
one call, so every warning on those three types applies here -- read them. In
particular it mounts unauthenticated start, read and signal routes reaching
ANY instance, and identity lifts rather than gates the redaction on those
reads. Mount only onto a router group your own auth middleware already
protects.`)
			},
		},
		{
			name: "MountHealth",
			decl: "MountHealth",
			assert: func(t *testing.T, blocks map[string]string) {
				assertIdenticalNote(t, blocks, `SECURITY: MountHealth registers the liveness and readiness probes with NO
authentication. The /readyz body names every configured check and reports
which of them is unavailable, so it discloses your dependency topology to any
caller that reaches it. Mount it on an internal listener, or onto a protected
group, whenever that disclosure matters.`)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			blocks := make(map[string]string, len(tableAdapters))

			var missing []string

			for _, name := range tableAdapters {
				ep, ok := adapters[name][tc.decl]
				if !ok {
					// Accumulated, not fatal: aborting on the first adapter
					// hides that the others are equally broken, and turns a
					// rename sweep into one round trip per adapter.
					missing = append(missing, name)
					continue
				}

				assert.Truef(t, noteStartsParagraph(ep.doc),
					"%s: the SECURITY: block must be preceded by a blank comment line, or godoc "+
						"renders the warning glued to the end of the paragraph above it", name)

				blocks[name] = securityBlock(ep.doc)
			}

			require.Emptyf(t, missing, "these adapters export no %s: %v", tc.decl, missing)

			tc.assert(t, blocks)
		})
	}
}

// TestSecurityNoteCoverage asserts that the parity table names every adapter and
// every mountable entry point that actually exists — not merely some of them.
//
// This is the half a green parity run cannot prove on its own, and the reason it
// is asserted separately: correct comparisons pointed at an incomplete list pass
// exactly as cheerfully as correct comparisons pointed at a complete one. A
// sixth route group added to gin alone, or a fourth adapter directory, would sit
// outside a hardcoded table and this package would stay green while a consumer
// met an entry point carrying no warning at all.
//
// scripts/check-doc-refs.sh makes the same argument about itself in
// assert_covers_tree, for the same reason, having been burned by the same shape.
func TestSecurityNoteCoverage(t *testing.T) {
	t.Parallel()

	adapters := discoverAdapters(t)

	discovered := make([]string, 0, len(adapters))
	for name := range adapters {
		discovered = append(discovered, name)
	}
	slices.Sort(discovered)

	wantAdapters := slices.Clone(tableAdapters)
	slices.Sort(wantAdapters)
	assert.Equalf(t, wantAdapters, discovered,
		"the adapter directories under %s are not the ones this table names; a new adapter "+
			"belongs in tableAdapters, not left uncovered", transportRoot)

	wantEntryPoints := slices.Clone(tableEntryPoints)
	slices.Sort(wantEntryPoints)

	for _, adapter := range discovered {
		got := make([]string, 0, len(adapters[adapter]))
		for name := range adapters[adapter] {
			got = append(got, name)
		}
		slices.Sort(got)

		assert.Equalf(t, wantEntryPoints, got,
			"%s exports a different set of mountable entry points than this table names; a new "+
				"route group or Mount function belongs in tableEntryPoints together with its "+
				"SECURITY note", adapter)
	}
}

// entryPoint is one exported surface a consumer mounts routes through: a
// route-group type, or a Mount* function.
type entryPoint struct {
	name string
	doc  *ast.CommentGroup
}

// discoverAdapters finds every HTTP adapter package under transportRoot and the
// mountable entry points each one exports.
//
// An adapter is a directory declaring at least one EXPORTED route-group type — a
// type with a Customize method — discovered rather than hardcoded, which is what
// makes a new adapter directory visible to this test instead of invisible to it.
// The rule also excludes httpcore, which declares the RouteCustomizer interface
// but no route group of its own, and this package, which is tests only.
//
// The walk is recursive, so a versioned adapter at echo/v4 is found; adapters are
// named by their path relative to transportRoot.
//
// STATED LIMIT — the only one. A package that exported mount functions while
// declaring no route-group type of its own would not be recognised as an adapter.
// Nothing shaped like that exists — a route group is typed on its framework's
// router, so it lives with its adapter — and widening the rule to "any package
// with an exported Mount* function" would sweep in httpcore.MountGroups, which is
// owed no note. Revisit if such a package is ever written; do not assume this
// already covers it.
//
// Four other limits once existed here and were CLOSED rather than documented:
// build constraints were ignored, generic route-group receivers were dropped, the
// walk was one directory deep, and an unexported Customize type was collected
// into a state with no legal resolution. The rule that decided each: a limit that
// fails CLOSED may be stated, because its worst case is a spurious red a human
// resolves; a limit that fails OPEN must be closed, because silent
// under-collection is the same failure this guard exists to prevent and is
// invisible on a green run. Do not let this list grow back.
func discoverAdapters(t *testing.T) map[string]map[string]entryPoint {
	t.Helper()

	adapters := make(map[string]map[string]entryPoint)

	err := filepath.WalkDir(transportRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		if eps, ok := discoverAdapter(t, path); ok {
			name, relErr := filepath.Rel(transportRoot, path)
			require.NoError(t, relErr)
			adapters[filepath.ToSlash(name)] = eps
		}
		return nil
	})
	require.NoErrorf(t, err, "walking %s", transportRoot)

	require.NotEmptyf(t, adapters,
		"no adapter packages found under %s; the scan would pass vacuously", transportRoot)

	return adapters
}

// discoverAdapter parses every buildable non-test Go file in dir, reporting the
// entry points it exports and whether it is an adapter at all.
//
// Parsing the whole directory rather than a named groups.go is deliberate, and
// load-bearing now that Mount and MountHealth live in mount.go: a guard pointed
// at hardcoded file paths cannot see an entry point in another file, so its note
// could be written and then silently deleted with this package still green.
//
// Files excluded from the build are skipped. go/parser does not honour build
// constraints on its own, so without build.Default.MatchFile a file behind
// `//go:build never` would be collected and a note demanded on code nobody
// compiles. Test files are skipped too: a Customize method on a test double
// would otherwise promote its package to an adapter.
func discoverAdapter(t *testing.T, dir string) (map[string]entryPoint, bool) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	require.NoErrorf(t, err, "reading %s", dir)

	fset := token.NewFileSet()

	var (
		files     []*ast.File
		parseErrs []string
	)

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		if build, err := build.Default.MatchFile(dir, name); err != nil || !build {
			continue
		}

		path := filepath.Join(dir, name)

		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			// Accumulated rather than fatal, for the same reason the parity
			// subtest accumulates: one unparseable file must not hide the rest.
			parseErrs = append(parseErrs, fmt.Sprintf("%s: %v", path, err))
			continue
		}

		files = append(files, file)
	}

	require.Emptyf(t, parseErrs, "parsing %s: %s", dir, strings.Join(parseErrs, "; "))

	typeDocs := make(map[string]*ast.CommentGroup)
	routeGroups := make(map[string]bool)

	for _, file := range files {
		collectTypes(file, typeDocs, routeGroups)
	}

	if len(routeGroups) == 0 {
		return nil, false
	}

	found := make(map[string]entryPoint, len(routeGroups))
	for name := range routeGroups {
		found[name] = entryPoint{name: name, doc: typeDocs[name]}
	}

	collectMountFuncs(files, routeGroups, found)

	return found, true
}

// collectTypes records the doc comment of every type declaration and every
// exported type carrying a Customize method — this package's definition of a
// route group.
func collectTypes(file *ast.File, typeDocs map[string]*ast.CommentGroup, routeGroups map[string]bool) {
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			for _, spec := range d.Specs {
				// The doc lives on the GenDecl, not the TypeSpec, for the
				// single-spec `type X struct` form every route group uses.
				if ts, ok := spec.(*ast.TypeSpec); ok {
					typeDocs[ts.Name.Name] = d.Doc
				}
			}

		case *ast.FuncDecl:
			if d.Recv == nil || d.Name.Name != "Customize" {
				continue
			}
			// Exported only. An unexported type with a Customize method has no
			// legal resolution: the coverage test would demand it in
			// tableEntryPoints, and that list is compared for equality against
			// every adapter, so naming it would red the other two. It is also
			// not a consumer-facing entry point, which is what a note is for.
			if recv := receiverTypeName(d.Recv); recv != "" && ast.IsExported(recv) {
				routeGroups[recv] = true
			}
		}
	}
}

// collectMountFuncs records every exported top-level function that mounts route
// groups, whether it constructs them itself or delegates to another such
// function.
//
// Constructing a route group, rather than a Mount* name prefix, is the rule,
// because it is the property the note is owed for: such a function CONCEALS which
// route groups a consumer just mounted. Mount(mux, svc) registers InstanceRoutes,
// TaskRoutes and MessageRoutes without any of the three appearing in the caller's
// source, so the caller never meets their notes — that concealment is the gap. A
// name-prefix rule would instead sweep in httpcore.MountGroups, which is generic
// over RouteCustomizer[R], conceals nothing, and whose caller must already hold a
// route-group value and has therefore already met that type's note. It would also
// miss a mount helper not called Mount-something.
//
// Concealment is TRANSITIVE, so this runs to a fixpoint: a wrapper that only
// calls Mount and MountHealth constructs no route group of its own, yet conceals
// strictly more than either. The adapters' other exported functions —
// WithBasePath, WithRequestActor, NosniffMiddleware — neither construct a route
// group nor call anything that does.
func collectMountFuncs(files []*ast.File, routeGroups map[string]bool, found map[string]entryPoint) {
	funcs := make(map[string]*ast.FuncDecl)

	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv == nil && fn.Name.IsExported() && fn.Body != nil {
				funcs[fn.Name.Name] = fn
			}
		}
	}

	for changed := true; changed; {
		changed = false

		for name, fn := range funcs {
			if _, already := found[name]; already {
				continue
			}
			if !mountsRouteGroup(fn.Body, routeGroups, found) {
				continue
			}
			found[name] = entryPoint{name: name, doc: fn.Doc}
			changed = true
		}
	}
}

// mountsRouteGroup reports whether body constructs one of this package's route
// groups, or calls an entry point already known to mount one.
func mountsRouteGroup(body *ast.BlockStmt, routeGroups map[string]bool, found map[string]entryPoint) bool {
	mounts := false

	ast.Inspect(body, func(n ast.Node) bool {
		if mounts {
			return false
		}

		switch e := n.(type) {
		case *ast.CompositeLit:
			if ident, ok := e.Type.(*ast.Ident); ok && routeGroups[ident.Name] {
				mounts = true
				return false
			}

		case *ast.CallExpr:
			if ident, ok := e.Fun.(*ast.Ident); ok {
				if _, ok := found[ident.Name]; ok {
					mounts = true
					return false
				}
			}
		}

		return true
	})

	return mounts
}

// receiverTypeName returns the base type name of a method receiver, seeing
// through a pointer receiver. The three adapters spell the receiver identifier
// differently (c, g, ir, …), so the name is read from the type, never from the
// identifier.
func receiverTypeName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}

	expr := recv.List[0].Type
	for {
		switch e := expr.(type) {
		case *ast.StarExpr: // *ProbeRoutes
			expr = e.X
		case *ast.IndexExpr: // ProbeRoutes[T]
			expr = e.X
		case *ast.IndexListExpr: // ProbeRoutes[T, U]
			expr = e.X
		case *ast.Ident:
			return e.Name
		default:
			return ""
		}
	}
}

// noteStartsParagraph reports whether the SECURITY: block begins a new paragraph
// — that is, whether a blank comment line precedes it, or it is the whole
// comment.
//
// Byte-identical blocks were still RENDERING differently: fiber carried a blank
// // before its AdminRoutes note and stdlib and gin did not, so fiber showed the
// warning as its own paragraph while the other two glued it to the end of "It
// implements httpcore.RouteCustomizer[…]". securityBlock starts extraction AT
// the SECURITY: line, so the line that decides this sits outside the compared
// region and byte-identity could never see it. Asserting the boundary directly
// is the fix; leaving it to the block comparison is what let it drift.
func noteStartsParagraph(doc *ast.CommentGroup) bool {
	if doc == nil {
		return false
	}

	lines := strings.Split(strings.TrimRight(doc.Text(), "\n"), "\n")

	for i, line := range lines {
		if !strings.HasPrefix(line, "SECURITY:") {
			continue
		}
		return i == 0 || strings.TrimSpace(lines[i-1]) == ""
	}

	return false
}

// securityBlock returns the doc comment text from the "SECURITY:" line to the
// END of the comment. Returns "" when the comment carries no such line.
//
// To the end, rather than up to the next blank line. The two are NOT equivalent:
// six of the twenty-one pairs carry a multi-paragraph note today (InstanceRoutes
// and TaskRoutes on all three adapters), and a blank-line rule would truncate
// each of them to its first paragraph. What is true, and what this relies on, is
// that the note is the LAST text in every one of the twenty-one doc comments.
//
// It also fails closed: prose appended after a note lands inside the compared
// region and goes red, rather than being silently dropped from the comparison.
// The one exception is a Deprecated: marker, which godoc treats as a trailing
// paragraph of its own and which says nothing about the security posture — it is
// stopped at rather than compared, so adding one does not produce the false
// "block differs from the authored note" that would train a maintainer to edit
// the note instead.
//
// Either way this is the note ALONE, never the whole doc comment: see the note
// on TestSecurityNoteParity about the adapters' deliberately divergent prose.
func securityBlock(doc *ast.CommentGroup) string {
	if doc == nil {
		return ""
	}

	lines := strings.Split(strings.TrimRight(doc.Text(), "\n"), "\n")

	for i, line := range lines {
		if !strings.HasPrefix(line, "SECURITY:") {
			continue
		}

		block := lines[i:]
		for j, l := range block {
			if j > 0 && strings.HasPrefix(l, "Deprecated:") {
				block = block[:j]
				break
			}
		}

		return strings.TrimRight(strings.Join(block, "\n"), "\n")
	}

	return ""
}

// assertIdenticalNote asserts that every adapter carries want as its SECURITY:
// block.
//
// Comparing all three against one authored literal is the whole of the check:
// it establishes equality with the reviewed text AND equality among the
// adapters, by transitivity, and it is what catches a note deleted from all
// three at once. See the note on tableAdapters for why the pairwise assertions
// that used to follow this loop were removed rather than reworded.
func assertIdenticalNote(t *testing.T, blocks map[string]string, want string) {
	t.Helper()

	for _, name := range tableAdapters {
		assert.NotEmptyf(t, blocks[name], "%s: no SECURITY: block on this entry point", name)
		assert.Equalf(t, want, blocks[name], "%s: SECURITY: block differs from the authored note", name)
	}
}
