package backfill

// The zero-diff check for the title expectations committed under
// testdata/title_backfill/.
//
// The backfill re-derives a stored transcript's title from its recorded user
// turns, and every derived title is produced by the pinned redaction module,
// never by this package. The committed expectations are therefore a COPY of that
// module's output, and a copy goes stale silently: re-pinning the module to a
// release that changes the canonical redacted form leaves the fixture asserting
// a shape nothing produces any more, and a hand-edited expectation makes a real
// regression look intentional.
//
// This check regenerates every committed expectation through the pinned module
// along the SAME derivation the backfill runs, and requires a zero diff against
// what is committed. Any difference is reported by row name with both strings.

import (
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/schema"
)

func TestTitleFixturesMatchModule(t *testing.T) {
	cases, err := loadTitleBackfillFixtures(titleBackfillCasesYAML)
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatalf("construct the real title pipeline from the pinned redaction module: %v", err)
	}

	type diff struct{ row, committed, module string }
	var diffs []diff
	regenerated := 0
	for _, tc := range cases {
		if tc.Expected == "" {
			continue
		}
		if len(tc.FirstUserTurns) == 0 {
			t.Fatalf("title backfill row %q expects the title %q but records no user turns to derive it from, so "+
				"nothing can regenerate that expectation and it would drift unchecked. Give the row its "+
				"first_user_turns, or drop the expectation.", tc.Name, tc.Expected)
		}
		regenerated++
		payload := buildUserTurnsPayload(tc.LeadingNonUserTurns, tc.FirstUserTurns)
		ctx := redact.TitleContext{Harness: schema.HarnessClaudeCode, ProjectPath: tc.ProjectPath}
		title, err := deriveTitleFromPayload(payload, pipeline, ctx, nil)
		if err != nil {
			t.Fatalf("regenerating the expectation for row %q through the pinned module failed: %v. The row expects "+
				"%q, so the derivation that produces it must succeed.", tc.Name, err, tc.Expected)
		}
		if title != tc.Expected {
			diffs = append(diffs, diff{row: tc.Name, committed: tc.Expected, module: title})
		}
	}

	if regenerated == 0 {
		t.Fatal("no title expectation was regenerated, so this check proved nothing. It must drive every row of " +
			"testdata/title_backfill/cases.yaml that carries an expected title through the pinned module; a " +
			"fixture with no such row cannot hold the committed expectations to the module's output.")
	}
	if len(diffs) == 0 {
		return
	}

	sort.Slice(diffs, func(i, j int) bool { return diffs[i].row < diffs[j].row })
	var b strings.Builder
	for _, d := range diffs {
		b.WriteString("\n  " + d.row +
			"\n    committed: " + strconv.Quote(d.committed) +
			"\n    module:    " + strconv.Quote(d.module))
	}
	t.Fatalf("%d committed title expectation(s) in internal/backfill/testdata/title_backfill/cases.yaml do not "+
		"match what the pinned redaction module produces:%s\n\n"+
		"What this means: the committed YAML no longer describes the titles the backfill actually derives, so every "+
		"test reading it is asserting a shape nothing produces. It is detected here, in a check that regenerates "+
		"the expectations rather than reading them, so no stored transcript is affected yet.\n"+
		"How to fix it: if the redaction module pin in backend/go.mod moved, replace each committed expectation "+
		"with the module value printed above; if the pin did not move, an expectation was edited by hand and the "+
		"edit should be reverted.", len(diffs), b.String())
}
