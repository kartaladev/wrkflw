// Package wrkflw_test pins the consumer-facing documentation the root package
// carries. It is an external test package on purpose: the root package exports
// nothing, and keeping the test out of it preserves that property.
package wrkflw_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	// Blank-imported for its registration side effect: NodeKind.String() returns
	// the stable wire name only after the node-family leaf packages have called
	// model.RegisterKind. Without this import String() falls back to
	// "NodeKind(<n>)" and every assertion below fails naming that fallback.
	_ "github.com/kartaladev/wrkflw/definition/kinds"
	"github.com/kartaladev/wrkflw/definition/model"
)

const (
	// docGoPath is the file whose rendered godoc this test pins. A package test
	// runs with the package directory as its working directory, so the root
	// package's test sees the repository root.
	docGoPath = "doc.go"

	// bpmnHeading opens the section under test; headingPrefix closes it. Both are
	// godoc heading syntax, so the section boundaries are the same ones
	// pkg.go.dev renders.
	bpmnHeading   = "// # BPMN 2.0"
	headingPrefix = "// # "

	// rowIndent is the godoc preformatted-block indent that carries the
	// divergence table. A row's key is the first word after the indent;
	// continuation lines add spaces after it and so carry no key.
	rowIndent = "//\t"

	// matchesLead opens the "matches BPMN" list. Every registered kind must be
	// either a divergence row key or named in this list.
	matchesLead = "// Matches BPMN"

	// contextPath and contextBPMNHeading pin the glossary's pointer at the
	// doc.go section rather than a second copy of the table.
	contextPath        = "CONTEXT.md"
	contextBPMNHeading = "## BPMN"
)

// The matching domain, stated here because it decides the answer before any
// logic below runs:
//
//   - WHOLE TOKEN, never substring. Both regexps capture a maximal
//     [A-Za-z][A-Za-z0-9]* run, and classification is map lookup by exact
//     equality. Measured on the way: no one of the 17 registered names is a
//     substring of another, so substring matching would not have produced a
//     wrong answer today — but it would have gone wrong silently the first time
//     a kind was appended whose name contains an existing one.
//   - CASE-SENSITIVE on the exact NodeKind.String() value. Kind names are
//     lowerCamelCase; matching case-insensitively would let prose like
//     "sub-process" or "Subprocess" classify subProcess.
//   - PLACEMENT, not presence. A kind counts as classified only where it is a
//     row's subject or a member of the list. Kinds are named inside other
//     rows' prose — the boundary-host row names serviceTask, businessRuleTask,
//     receiveTask and userTask — so a test asking merely "does the section
//     contain this name" would report receiveTask and userTask classified even
//     with the list omitting them entirely, and would pass over a doc that
//     classifies fifteen kinds out of seventeen.
//
// rowKeyRe matches a divergence row's key: the first word of a preformatted
// line. Anchored immediately after rowIndent, so a continuation line — which
// starts with spaces to align under the key — cannot match.
var rowKeyRe = regexp.MustCompile(`^` + regexp.QuoteMeta(rowIndent) + `([A-Za-z][A-Za-z0-9]*)`)

// identRe extracts candidate kind names from the prose "matches BPMN" list.
var identRe = regexp.MustCompile(`[A-Za-z][A-Za-z0-9]*`)

// bpmnSection returns the lines of doc.go's "# BPMN 2.0" section, from the
// heading up to (not including) the next godoc heading.
//
// Extracting the section is the point: a tree-wide search for a kind name
// verifies presence, never placement, and doc.go already names TaskRoutes,
// TaskService and MemTaskStore outside this section.
func bpmnSection(t *testing.T) []string {
	t.Helper()

	raw, err := os.ReadFile(docGoPath)
	if err != nil {
		t.Fatalf("read %s: %v", docGoPath, err)
	}
	lines := strings.Split(string(raw), "\n")

	start := -1
	for i, line := range lines {
		if strings.TrimRight(line, " \t") == bpmnHeading {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("%s has no %q heading: the BPMN divergence note is missing entirely", docGoPath, bpmnHeading)
	}

	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], headingPrefix) {
			return lines[start:i]
		}
	}
	return lines[start:]
}

// divergenceRowKeys returns the key of every divergence-table row in the
// section. A kind is classified as diverging only by being a row key — not by
// being mentioned inside another row's text, which is why the boundary-host row
// naming serviceTask does not classify serviceTask.
func divergenceRowKeys(section []string) map[string]bool {
	keys := make(map[string]bool)
	for _, line := range section {
		if m := rowKeyRe.FindStringSubmatch(line); m != nil {
			keys[m[1]] = true
		}
	}
	return keys
}

// matchesBPMNNames returns the kind names listed in the "matches BPMN" list:
// the matchesLead line plus the comment lines that continue its paragraph.
func matchesBPMNNames(t *testing.T, section []string) map[string]bool {
	t.Helper()

	start := -1
	for i, line := range section {
		if strings.HasPrefix(line, matchesLead) {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("%s's %q section has no %q list: nothing states which kinds are undiverged", docGoPath, bpmnHeading, matchesLead)
	}

	names := make(map[string]bool)
	for _, line := range section[start:] {
		body, ok := strings.CutPrefix(line, "// ")
		if !ok {
			break // a blank "//" or a preformatted line ends the paragraph
		}
		for _, ident := range identRe.FindAllString(body, -1) {
			names[ident] = true
		}
	}
	return names
}

// TestDocGoClassifiesEveryKind asserts doc.go's "# BPMN 2.0" section classifies
// every registered node kind exactly once: either as a divergence row key or as
// a name in the "matches BPMN" list. Adding a kind without classifying it fails
// the build, which is what keeps the prose from drifting away from the registry.
func TestDocGoClassifiesEveryKind(t *testing.T) {
	section := bpmnSection(t)
	rows := divergenceRowKeys(section)
	matches := matchesBPMNNames(t, section)

	// Bound to the LAST kind constant (KindCompensationThrowEvent), matching
	// TestAllKindsRegistered, so a newly-appended kind is always checked. A
	// hardcoded list of names would reintroduce exactly the gap that test's own
	// comment records.
	for k := model.KindStartEvent; k <= model.KindCompensationThrowEvent; k++ {
		name := k.String()
		if strings.Contains(name, "NodeKind(") {
			t.Fatalf("kind %d has no registered name (%q): the kinds bundle is not imported, so nothing below can be trusted", int(k), name)
		}

		inTable, inMatches := rows[name], matches[name]
		switch {
		case !inTable && !inMatches:
			t.Errorf("kind %d %q is UNCLASSIFIED in %s's %q section: it is neither a divergence row key nor named in the %q list",
				int(k), name, docGoPath, bpmnHeading, matchesLead)
		case inTable && inMatches:
			t.Errorf("kind %d %q is classified TWICE in %s's %q section: it is a divergence row key and is also named in the %q list; a kind either diverges or matches",
				int(k), name, docGoPath, bpmnHeading, matchesLead)
		}
	}
}

// TestContextMDPointsAtDocGo asserts the glossary exists and points at the
// doc.go section instead of copying the table.
//
// What this can and cannot check: the file's existence and the pointer are
// asserted here; the correctness of the glossary's term definitions is not
// testable and is reviewed instead.
func TestContextMDPointsAtDocGo(t *testing.T) {
	raw, err := os.ReadFile(contextPath)
	if err != nil {
		t.Fatalf("read %s: %v", contextPath, err)
	}

	var bpmn string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, contextBPMNHeading) {
			bpmn = line
			break
		}
	}
	if bpmn == "" {
		t.Fatalf("%s has no %q heading: the glossary does not point at the divergence note", contextPath, contextBPMNHeading)
	}

	body := strings.Join(strings.Split(string(raw), contextBPMNHeading)[1:], contextBPMNHeading)
	if !strings.Contains(body, docGoPath) {
		t.Errorf("%s's %q section does not name %s: the glossary must point at the divergence table, not fork it", contextPath, contextBPMNHeading, docGoPath)
	}
}
