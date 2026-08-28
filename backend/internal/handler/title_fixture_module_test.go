package handler

// The zero-diff check for the title expectations committed under testdata/.
//
// Every title this village stores is produced by the pinned redaction module,
// never by the village itself. The committed expectations are therefore a COPY
// of that module's output, and a copy goes stale silently: re-pinning the module
// to a release that changes the canonical redacted form leaves the fixture
// asserting a shape nothing produces any more, and a hand-edited expectation
// makes a real regression look intentional.
//
// This check closes both holes. It regenerates every committed expectation
// through the pinned module along the SAME production path a publish takes, and
// requires a zero diff against what is committed. Any difference — a stale
// expectation after a re-pin, or an expectation edited by hand — is reported by
// row name with both strings.

import (
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/peasant-labs/redact"
)

// titleExpectationDiff is one row whose committed expectation and freshly
// regenerated expectation disagree.
type titleExpectationDiff struct {
	Row       string
	Committed string
	Module    string
}

func TestTitleFixturesMatchModule(t *testing.T) {
	titles, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatalf("construct the real title pipeline from the pinned redaction module: %v", err)
	}

	var diffs []titleExpectationDiff
	regenerated := 0
	for _, fixture := range loadTitleWriteFixtures(t) {
		// A `fresh` row's expectation is the module's OUTPUT: the publish path
		// sanitizes the harness-generated candidate, so what the module makes of
		// that candidate is the whole expectation and is regenerated below.
		//
		// A PATCH row's expectation is a different contract and must not be
		// regenerated: an owner-supplied title that passes validation is stored
		// byte for byte, so its expectation is its candidate UNCHANGED. Silently
		// skipping those rows would let a hand-edit hide here, so the
		// byte-preservation contract is asserted instead of ignored.
		if fixture.Mode != "fresh" {
			if fixture.Expected != "" && fixture.Expected != fixture.Candidate {
				t.Errorf("title-write row %q is mode %q and expects %q from candidate %q. A title an owner PATCHes "+
					"is stored byte for byte once it passes validation, so a non-`fresh` expectation may only ever "+
					"be its own candidate; anything else is a rewrite this village does not perform. Detected in "+
					"the fixture-to-module check before any assertion ran, so no behavior changed. Restore the "+
					"expectation to the candidate, or move the row to `fresh` mode if it is meant to exercise the "+
					"publish-time sanitizer.", fixture.Name, fixture.Mode, fixture.Expected, fixture.Candidate)
			}
			continue
		}
		regenerated++
		if got, _ := regenerateFreshTitleExpectation(titles, fixture); got != fixture.Expected {
			diffs = append(diffs, titleExpectationDiff{Row: fixture.Name, Committed: fixture.Expected, Module: got})
		}
	}

	if regenerated == 0 {
		t.Fatal("no title expectation was regenerated, so this check proved nothing. It must drive every `fresh` " +
			"row of testdata/title_writes.yaml through the pinned module; a fixture with no such row cannot hold " +
			"the committed expectations to the module's output.")
	}
	reportTitleExpectationDiffs(t, "internal/handler/testdata/title_writes.yaml", diffs)
}

// reportTitleExpectationDiffs fails with the whole diff rather than the first
// row, because a module re-pin usually moves every affected expectation at once
// and a reviewer needs to see the full shape change, not one example of it.
func reportTitleExpectationDiffs(t *testing.T, fixturePath string, diffs []titleExpectationDiff) {
	t.Helper()
	if len(diffs) == 0 {
		return
	}
	sort.Slice(diffs, func(i, j int) bool { return diffs[i].Row < diffs[j].Row })
	var b strings.Builder
	for _, d := range diffs {
		b.WriteString("\n  ")
		b.WriteString(d.Row)
		b.WriteString("\n    committed: ")
		b.WriteString(strconv.Quote(d.Committed))
		b.WriteString("\n    module:    ")
		b.WriteString(strconv.Quote(d.Module))
	}
	t.Fatalf("%d committed title expectation(s) in %s do not match what the pinned redaction module produces:%s\n\n"+
		"What this means: the committed YAML no longer describes the titles this village actually stores, so every "+
		"test reading it is asserting a shape nothing produces. It is detected here, in a check that regenerates "+
		"the expectations rather than reading them, so nothing shipped is affected yet.\n"+
		"How to fix it: if the redaction module pin in backend/go.mod moved, replace each committed expectation "+
		"with the module value printed above; if the pin did not move, an expectation was edited by hand and the "+
		"edit should be reverted.",
		len(diffs), fixturePath, b.String())
}
