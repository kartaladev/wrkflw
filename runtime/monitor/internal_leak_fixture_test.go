package monitor_test

// The hermetic half of the internal-leak guard.
//
// TestNoExportedSignatureNamesAnInternalType runs scanInternalLeaks over the
// real module, which proves the module is clean but says almost nothing about
// what the scanner would CATCH — every assertion there is "found nothing", and
// a scanner that found nothing ever would pass it identically. The two defects
// #148 fixed were both invisible to it for exactly that reason.
//
// So this file drives the same scanner over a synthetic module in t.TempDir()
// and asserts, declaration by declaration, both directions: the shapes that
// must be reported AND the shapes that must not. The refuse half alone is not
// enough — a key that exempts nothing satisfies "the method is reported" just
// as well as a correct one, so every refuse row here has an accept row beside
// it.
//
// The fixture is 100% synthetic and depends on no symbol from this module. It
// is not testdata/ (the walker skips that directory by design, and adding an
// exception would widen the production walk's domain to buy a test), and it
// names no real package, so it cannot be broken by another package's API
// changing. Nothing is compiled: scanInternalLeaks only parses, so the imported
// internal package never has to exist.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	fixtureRel        = "pkg/api.go"
	fixtureInternal   = "example.com/fixture/internal/secret"
	fixtureKnownKey   = "pkg/api.go:Foo"
	fixtureSealKey    = "pkg/api.go:(Baz).Qux"
	fixtureSealedPath = fixtureInternal
)

// fixtureSource carries one declaration per row of the table below. Every
// exported shape the widened walk understands appears here, alongside the
// near-misses that must stay silent.
const fixtureSource = `package api

import "` + fixtureInternal + `"

// Foo is allowlisted by key "pkg/api.go:Foo".
func Foo(s secret.T) {}

type Bar struct{}

// (Bar).Foo shares Foo's name. Under the old receiver-less key it inherited
// Foo's exemption; it must now be reported.
func (b *Bar) Foo(s secret.T) {}

type Baz struct{}

// (Baz).Qux is sealed by key "pkg/api.go:(Baz).Qux" for this exact import path.
func (b *Baz) Qux(s secret.T) {}

// Reported: exported interface method set.
type Iface interface {
	M(s secret.T) error
	hidden(s secret.T)
}

// Reported: exported struct field of func type. The unexported sibling must
// NOT be reported -- it is how a package holds internal state.
type Holder struct {
	F      func(secret.T)
	hidden secret.T
}

// Reported: exported type definition whose underlying type names an internal
// type. This is the casbinauthz.DBOption shape, and the only one that exists
// for real in this module.
type Option func(*secret.Config)

// Reported: exported var with a declared internal type.
var Declared secret.T

// NOT reported: a value re-export. The exported symbol is fully usable through
// errors.Is and the consumer never names the internal type.
var ReExported = secret.Sentinel

// NOT reported: an exported struct whose ONLY internal-naming field is
// unexported. This is the accept half for narrowing the domain to
// consumer-reachable parts -- without it, widening the walk reports 12 correct
// designs in this module (Authorizer.inner, OutboxStatsCollector.tel, ...).
type HiddenFieldOnly struct {
	hidden secret.T
}

// NOT reported: an exported interface whose only internal-naming method is
// unexported, so no consumer can call it.
type IfaceHiddenOnly interface {
	hidden(s secret.T)
}

// NOT reported: unexported func.
func hidden(s secret.T) {}

// NOT reported: unexported type.
type hiddenType func(secret.T)
`

// writeFixture plants the synthetic module and returns its root.
func writeFixture(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	path := filepath.Join(root, filepath.FromSlash(fixtureRel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(fixtureSource), 0o600))
	return root
}

func fixtureAllowlists() (known, seals map[string]string) {
	return map[string]string{fixtureKnownKey: "the plain func, tolerated"},
		map[string]string{fixtureSealKey: fixtureSealedPath}
}

// reportedSymbols returns the set of symbols the scan reported, spelled the way
// an offender line renders them ("Foo", "(Bar).Foo").
//
// An offender line is "<rel>:<line> [<kind>] <symbol> names <pkg>.<Type> (<path>)",
// so the symbol is field 2.
func reportedSymbols(t *testing.T, offenders []string) map[string]bool {
	t.Helper()

	reported := make(map[string]bool, len(offenders))
	for _, o := range offenders {
		fields := strings.Fields(o)
		require.GreaterOrEqual(t, len(fields), 3, "unrecognised offender line %q", o)
		reported[fields[2]] = true
	}
	return reported
}

func TestScanInternalLeaksReportsEveryConsumerReachableShape(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		symbol string
		assert func(t *testing.T, reported bool, offenders []string)
	}

	cases := []testCase{
		{
			name:   "a method inherits no exemption from a same-named func",
			symbol: "(Bar).Foo",
			assert: func(t *testing.T, reported bool, offenders []string) {
				assert.True(t, reported,
					"(Bar).Foo must be reported: it is a different declaration from the "+
						"allowlisted func Foo, and keying without the receiver is what let it "+
						"inherit that exemption (#148). offenders=%v", offenders)
			},
		},
		{
			name:   "the allowlisted plain func is still exempt",
			symbol: "Foo",
			assert: func(t *testing.T, reported bool, offenders []string) {
				assert.False(t, reported,
					"func Foo must stay exempt. This is the at-limit ACCEPT beside the refuse "+
						"above: without it, a key that exempts nothing passes the (Bar).Foo "+
						"assertion just as well as a correct key. offenders=%v", offenders)
			},
		},
		{
			name:   "a method whose receiver matches its own seal is exempt",
			symbol: "(Baz).Qux",
			assert: func(t *testing.T, reported bool, offenders []string) {
				assert.False(t, reported,
					"(Baz).Qux is sealed under its own receiver-qualified key and must be "+
						"exempt. offenders=%v", offenders)
			},
		},
		{
			name:   "an exported interface method set is reported",
			symbol: "Iface",
			assert: func(t *testing.T, reported bool, offenders []string) {
				assert.True(t, reported, "offenders=%v", offenders)
			},
		},
		{
			name:   "an exported struct field of func type is reported",
			symbol: "Holder",
			assert: func(t *testing.T, reported bool, offenders []string) {
				assert.True(t, reported, "offenders=%v", offenders)
			},
		},
		{
			name:   "an exported type definition is reported",
			symbol: "Option",
			assert: func(t *testing.T, reported bool, offenders []string) {
				assert.True(t, reported,
					"the casbinauthz.DBOption shape -- an exported named type whose underlying "+
						"type names an internal type. It is the only widened shape that exists "+
						"for real in this module. offenders=%v", offenders)
			},
		},
		{
			name:   "an exported var with a declared internal type is reported",
			symbol: "Declared",
			assert: func(t *testing.T, reported bool, offenders []string) {
				assert.True(t, reported, "offenders=%v", offenders)
			},
		},
		{
			name:   "a value re-export is NOT reported",
			symbol: "ReExported",
			assert: func(t *testing.T, reported bool, offenders []string) {
				assert.False(t, reported,
					"`var X = internalpkg.X` is not a leak: the symbol is fully usable through "+
						"errors.Is and the consumer never names the internal type. Inspecting the "+
						"initialiser instead of the declared type reports three correct sentinel "+
						"re-exports in this module as offenders. offenders=%v", offenders)
			},
		},
		{
			name:   "a struct whose only internal-naming field is unexported is NOT reported",
			symbol: "HiddenFieldOnly",
			assert: func(t *testing.T, reported bool, offenders []string) {
				assert.False(t, reported,
					"an unexported field is how a package holds internal state and is not "+
						"reachable by a consumer. Reading every field reports 12 correct designs "+
						"in this module as offenders -- too large a domain is a measurement error "+
						"exactly as too small a one is. offenders=%v", offenders)
			},
		},
		{
			name:   "an interface whose only internal-naming method is unexported is NOT reported",
			symbol: "IfaceHiddenOnly",
			assert: func(t *testing.T, reported bool, offenders []string) {
				assert.False(t, reported, "offenders=%v", offenders)
			},
		},
		{
			name:   "an unexported func is NOT reported",
			symbol: "hidden",
			assert: func(t *testing.T, reported bool, offenders []string) {
				assert.False(t, reported, "offenders=%v", offenders)
			},
		},
		{
			name:   "an unexported type is NOT reported",
			symbol: "hiddenType",
			assert: func(t *testing.T, reported bool, offenders []string) {
				assert.False(t, reported, "offenders=%v", offenders)
			},
		},
	}

	root := writeFixture(t)
	known, seals := fixtureAllowlists()

	scan, err := scanInternalLeaks(root, known, seals)
	require.NoError(t, err)
	reported := reportedSymbols(t, scan.offenders)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, reported[tc.symbol], scan.offenders)
		})
	}
}

// TestScanInternalLeaksMarksBothAllowlistsSeen pins the self-cleaning half: an
// entry that matches no offender must be observable as unmatched, which is what
// stops a stale exemption sheltering the next leak.
func TestScanInternalLeaksMarksBothAllowlistsSeen(t *testing.T) {
	t.Parallel()

	known, seals := fixtureAllowlists()

	scan, err := scanInternalLeaks(writeFixture(t), known, seals)
	require.NoError(t, err)

	assert.True(t, scan.seenKnown[fixtureKnownKey],
		"the knownOpenInternalLeaks entry matched no offender, so the self-cleaning "+
			"assertion in the module-wide test could never fire")
	assert.True(t, scan.seenSeal[fixtureSealKey],
		"the intentionalCapabilitySeals entry matched no offender")

	// An entry for a symbol that does not exist must stay unseen.
	absent := map[string]string{"pkg/api.go:NoSuchSymbol": "stale"}
	staleScan, err := scanInternalLeaks(writeFixture(t), absent, seals)
	require.NoError(t, err)
	assert.False(t, staleScan.seenKnown["pkg/api.go:NoSuchSymbol"],
		"a stale allowlist entry must read as unmatched, or it would shelter the next leak")
}

// TestScanInternalLeaksSealRequiresTheSanctionedImportPath pins the value check:
// a seal is exempt only for the internal package it was sanctioned for.
func TestScanInternalLeaksSealRequiresTheSanctionedImportPath(t *testing.T) {
	t.Parallel()

	known, _ := fixtureAllowlists()
	wrongPath := map[string]string{fixtureSealKey: "example.com/fixture/internal/somethingelse"}

	scan, err := scanInternalLeaks(writeFixture(t), known, wrongPath)
	require.NoError(t, err)

	assert.True(t, reportedSymbols(t, scan.offenders)["(Baz).Qux"],
		"a seal sanctioned for a different internal package must not exempt this one; "+
			"otherwise any future internal type at the same symbol inherits the exemption. "+
			"offenders=%v", scan.offenders)
}
