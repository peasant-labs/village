package handler

import (
	"bytes"
	_ "embed"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"testing"
)

//go:embed testdata/paged_read_parity.yaml
var pagedReadParityYAML []byte

//go:embed testdata/transcript_row_states.yaml
var transcriptRowStatesYAML []byte

//go:embed testdata/transcript_audience_kinds.yaml
var transcriptAudienceKindsYAML []byte

type pagedReadParityFixture struct {
	Names      []string `yaml:"names"`
	Detections []struct {
		Name         string `yaml:"name"`
		Perturbation string `yaml:"perturbation"`
		Kind         string `yaml:"kind"`
	} `yaml:"detections"`
	Corpus      []string          `yaml:"corpus"`
	StateReads  map[string]string `yaml:"state_reads"`
	Unreachable map[string]string `yaml:"unreachable"`
	Content     string            `yaml:"content"`
}

func loadPagedReadParityFixture(t *testing.T) pagedReadParityFixture {
	t.Helper()
	fixture, err := decodeFixtureDocument[pagedReadParityFixture](pagedReadParityYAML)
	if err != nil {
		t.Fatalf("testdata/paged_read_parity.yaml: %v", err)
	}
	seen := map[string]bool{}
	for _, name := range append(append([]string(nil), fixture.Names...), fixture.Corpus...) {
		if name == "" || seen[name] {
			t.Fatalf("testdata/paged_read_parity.yaml repeats or blanks the name %q", name)
		}
		seen[name] = true
	}
	return fixture
}

// requireFamilyName fails unless name is in the family's manifest, so every
// executed name is a manifest name and a renamed case cannot drift silently.
func requireFamilyName(t *testing.T, fixture pagedReadParityFixture, name string) {
	t.Helper()
	if !slices.Contains(fixture.Names, name) {
		t.Fatalf("executed name %q is not in the paged_read_parity.yaml manifest", name)
	}
}

func shapeCaseName(shape transcriptReadShape) string { return "parity-shape-" + shape.String() }

// TestPagedReadParityCorpusCoversClosedShapeSet generates the corpus names
// from the two closed sets and requires exactly the manifest: a member added
// to either set generates a name the manifest lacks, and a removed member
// leaves a manifest name nothing generates.
func TestPagedReadParityCorpusCoversClosedShapeSet(t *testing.T) {
	fixture := loadPagedReadParityFixture(t)
	t.Run("parity-corpus-covers-closed-shape-set", func(t *testing.T) {
		requireFamilyName(t, fixture, "parity-corpus-covers-closed-shape-set")
		var generated []string
		for _, shape := range allTranscriptReadShapes() {
			generated = append(generated, shapeCaseName(shape))
		}
		want := slices.Clone(fixture.Corpus)
		slices.Sort(generated)
		slices.Sort(want)
		if !slices.Equal(generated, want) {
			t.Fatalf("the closed shape set generates %v, the corpus manifest names %v; list one parity-shape case per (row state, audience kind) pair", generated, want)
		}
		if len(generated) != len(allTranscriptRowStates())*len(allTranscriptAudienceKinds()) {
			t.Fatalf("the generated corpus is not the full product of the two closed sets")
		}
		var states []string
		for _, state := range allTranscriptRowStates() {
			states = append(states, state.String())
			if outcome := fixture.StateReads[state.String()]; outcome != "served" && outcome != "refused" && outcome != "unreachable" {
				t.Fatalf("state_reads gives row state %s the outcome %q; give served or refused", state, outcome)
			}
		}
		if len(fixture.StateReads) != len(states) {
			t.Fatalf("state_reads names %d states, the closed set has %v", len(fixture.StateReads), states)
		}
		if len(fixture.Unreachable) != 1 || fixture.Unreachable["l1d"] != "transcripts_content_revision_identity" {
			t.Fatalf("unexpected unreachable state constraints: %v", fixture.Unreachable)
		}
		for state := range fixture.StateReads {
			_, unreachable := fixture.Unreachable[state]
			if unreachable != (fixture.StateReads[state] == "unreachable") {
				t.Fatalf("unreachable state membership differs for %s", state)
			}
		}
	})
}

// TestPagedReadComparerDetectsPerturbations runs the check mode's own
// self-test and requires that each perturbation is detected as its kind and
// that the unperturbed synthetic read is clean.
func TestPagedReadComparerDetectsPerturbations(t *testing.T) {
	fixture := loadPagedReadParityFixture(t)
	var listed []string
	for _, d := range fixture.Detections {
		requireFamilyName(t, fixture, d.Name)
		listed = append(listed, d.Perturbation)
	}
	var injected []string
	for _, p := range allPagedReadPerturbations() {
		injected = append(injected, p.String())
	}
	slices.Sort(listed)
	slices.Sort(injected)
	if !slices.Equal(listed, injected) {
		t.Fatalf("the self-test injects %v and the fixture lists detections for %v; keep them the same set", injected, listed)
	}
	results, err := runPagedReadSelfTest()
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]pagedReadSelfTestResult{}
	for _, result := range results {
		if result.Perturbation == 0 {
			if len(result.Findings) != 0 {
				t.Fatalf("an unperturbed read of the synthetic subject has findings %+v", result.Findings)
			}
			continue
		}
		byName[result.Perturbation.String()] = result
	}
	for _, d := range fixture.Detections {
		t.Run(d.Name, func(t *testing.T) {
			result := byName[d.Perturbation]
			counts := summarizeFindings(result.Findings).Counts
			var kinds []string
			for _, kind := range allPagedReadFindingKinds() {
				if counts[kind] != 0 {
					kinds = append(kinds, kind.String())
				}
			}
			if !slices.Equal(kinds, []string{d.Kind}) {
				t.Fatalf("%s was detected as %v, want exactly [%s]", d.Perturbation, kinds, d.Kind)
			}
		})
	}
	if err := selfTestPagedReadComparer(); err != nil {
		t.Fatalf("the check mode's self-test gate refused a working comparer: %v", err)
	}
}

// reportLineGrammar is every line shape the check report may write. Values are
// numbers, closed-set words, a UUID in the sample list, or the operator's
// authorization reference; nothing else can reach the report.
var reportLineGrammar = regexp.MustCompile(`^(` +
	`paged_read_check mode=(census|sample|transcripts)( authorization_ref=[A-Za-z0-9._:/#-]+)?` +
	`|self_test=(passed perturbations=\d+|failed)` +
	`|census shape=[a-z0-9-]+ rows=\d+` +
	`|census unexplained=\d+ unavailable=\d+` +
	`|sample subject=\d+ shape=[a-z0-9-]+ transcript=[0-9a-f-]{36}` +
	`|skipped subject=\d+ reason=no_viewer` +
	`|result subject=\d+ outcome=(clean|broken)( (rows|ref_span|page|refusal|number)=\d+)+` +
	`|finding subject=\d+ kind=(rows|ref_span|page|refusal|number) partition=(main|history:\d+) ordinal=-?\d+ position=-?\d+` +
	`|summary subjects=\d+ clean=\d+ broken=\d+ skipped=\d+` +
	`)$`)

// assertContentFreeReport fails when a report or log holds any byte of the
// self-test subject's turn content or a line outside the report grammar.
func assertContentFreeReport(t *testing.T, report, logs string, markers ...string) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(report), "\n") {
		if line != "" && !reportLineGrammar.MatchString(line) {
			t.Fatalf("report line %q is outside the content-free report grammar", line)
		}
	}
	for _, marker := range append(markers, selfTestContentMarker, strings.Repeat("x", 64)) {
		if strings.Contains(report, marker) || strings.Contains(logs, marker) {
			t.Fatalf("the report or log holds transcript content (%q)", marker)
		}
	}
}

// TestPagedReadPrivacyNoContentLogged runs the self-test and writes a report of
// every perturbed read's findings while capturing the process log; neither
// may hold content.
func TestPagedReadPrivacyNoContentLogged(t *testing.T) {
	fixture := loadPagedReadParityFixture(t)
	t.Run("parity-privacy-no-content-logged", func(t *testing.T) {
		requireFamilyName(t, fixture, "parity-privacy-no-content-logged")
		var logs bytes.Buffer
		previous := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
		t.Cleanup(func() { slog.SetDefault(previous) })
		results, err := runPagedReadSelfTest()
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		report := &pagedReadReport{out: &out}
		report.line("paged_read_check mode=%s authorization_ref=%s", PagedReadCheckTranscripts, "ops/synthetic")
		report.census(pagedReadCensus{Shapes: map[transcriptReadShape]int64{}})
		broken := 0
		for i, result := range results {
			report.result(i+1, result.Findings)
			if len(result.Findings) != 0 {
				broken++
			}
		}
		if broken != len(allPagedReadPerturbations()) || !strings.Contains(out.String(), "finding subject=") {
			t.Fatalf("the report did not record the perturbed reads, so it proves nothing: %s", out.String())
		}
		assertContentFreeReport(t, out.String(), logs.String())
	})
}

type rowStateCase struct {
	Name  string `yaml:"name"`
	Facts struct {
		Hash          string `yaml:"hash"`
		Size          string `yaml:"size"`
		SegmentedHash bool   `yaml:"segmented_hash"`
		SegmentedSize bool   `yaml:"segmented_size"`
		Revision      int64  `yaml:"revision"`
		Metadata      bool   `yaml:"metadata"`
	} `yaml:"facts"`
	State string `yaml:"state"`
}

// TestClassifyTranscriptRowState pins the column-facts rule the census and the
// corpus share, and requires every member of the closed set to be reachable.
func TestClassifyTranscriptRowState(t *testing.T) {
	cases, err := decodeFixtureRows[rowStateCase](transcriptRowStatesYAML)
	if err != nil {
		t.Fatalf("testdata/transcript_row_states.yaml: %v", err)
	}
	reached := map[string]bool{}
	seen := map[string]bool{}
	for _, c := range cases {
		if seen[c.Name] {
			t.Fatalf("case %q repeats", c.Name)
		}
		seen[c.Name] = true
		reached[c.State] = true
		t.Run(c.Name, func(t *testing.T) {
			sizes := map[string]identitySizeClass{"null": identitySizeNull, "negative": identitySizeNegative, "usable": identitySizeUsable}
			size, ok := sizes[c.Facts.Size]
			if !ok || (c.Facts.Hash != "null" && c.Facts.Hash != "canonical" && c.Facts.Hash != "malformed") {
				t.Fatalf("case facts use an unknown hash or size word")
			}
			facts := rowIdentityFacts{HashNull: c.Facts.Hash == "null", HashCanonical: c.Facts.Hash == "canonical", Size: size,
				SegmentedHash: c.Facts.SegmentedHash, SegmentedSize: c.Facts.SegmentedSize, Revision: c.Facts.Revision, Metadata: c.Facts.Metadata}
			state, ok := classifyTranscriptRowState(facts)
			got := state.String()
			if !ok {
				got = "unexplained"
			}
			if got != c.State {
				t.Fatalf("classified %s, want %s", got, c.State)
			}
		})
	}
	for _, state := range allTranscriptRowStates() {
		if !reached[state.String()] {
			t.Fatalf("no case classifies to row state %s; add one", state)
		}
	}
}

type audienceKindCase struct {
	Name         string   `yaml:"name"`
	Public       bool     `yaml:"public"`
	Shared       bool     `yaml:"shared"`
	MemberRoles  []string `yaml:"member_roles"`
	PreviewOwner bool     `yaml:"preview_owner"`
	Kinds        []string `yaml:"kinds"`
}

// TestAudienceKindsOfRow pins which audience kinds the census counts a row
// under, and requires every member of the closed set to be reachable.
func TestAudienceKindsOfRow(t *testing.T) {
	cases, err := decodeFixtureRows[audienceKindCase](transcriptAudienceKindsYAML)
	if err != nil {
		t.Fatalf("testdata/transcript_audience_kinds.yaml: %v", err)
	}
	reached := map[string]bool{}
	for _, c := range cases {
		for _, kind := range c.Kinds {
			reached[kind] = true
		}
		t.Run(c.Name, func(t *testing.T) {
			facts := rowShapeFacts{Present: true, Public: c.Public, Shared: c.Shared, MemberRoles: c.MemberRoles, PreviewOwner: c.PreviewOwner}
			var got []string
			for _, kind := range facts.audienceKinds() {
				got = append(got, kind.String())
			}
			if !slices.Equal(got, c.Kinds) {
				t.Fatalf("audience kinds %v, want %v", got, c.Kinds)
			}
		})
	}
	for _, kind := range allTranscriptAudienceKinds() {
		if !reached[kind.String()] {
			t.Fatalf("no case reaches audience kind %s; add one", kind)
		}
	}
}
