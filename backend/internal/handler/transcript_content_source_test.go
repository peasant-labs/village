package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/peasant-labs/schema"
)

//go:embed testdata/content_source_cook.yaml
var contentSourceCookYAML []byte

type contentSourceRowExpect struct {
	Stored   int    `yaml:"stored"`
	Number   string `yaml:"number"`
	Recorded string `yaml:"recorded"`
}

type contentSourcePartitionExpect struct {
	StoredTotal int                      `yaml:"stored_total"`
	Rows        []contentSourceRowExpect `yaml:"rows"`
	Refuse      string                   `yaml:"refuse"`
}

type contentSourceExpect struct {
	Main      *contentSourcePartitionExpect  `yaml:"main"`
	History   []contentSourcePartitionExpect `yaml:"history"`
	Construct string                         `yaml:"construct"`
}

type contentSourceAbsentKey struct {
	Kind    schema.TranscriptSegmentPartitionKind `yaml:"kind"`
	Section int                                   `yaml:"section"`
}

type contentSourceRange struct {
	Start int64 `yaml:"start"`
	End   int64 `yaml:"end"`
	OK    bool  `yaml:"ok"`
}

type contentSourceCase struct {
	Name      string                   `yaml:"name"`
	Why       string                   `yaml:"why"`
	Harness   string                   `yaml:"harness"`
	Main      string                   `yaml:"main"`
	OmitMain  bool                     `yaml:"omit_main"`
	History   []string                 `yaml:"history"`
	Indent    bool                     `yaml:"indent"`
	Document  string                   `yaml:"document"`
	Reads     []string                 `yaml:"reads"`
	CookCalls *int                     `yaml:"cook_calls"`
	Absent    []contentSourceAbsentKey `yaml:"absent"`
	Ranges    []contentSourceRange     `yaml:"ranges"`
	Expect    contentSourceExpect      `yaml:"expect"`
}

// requiredContentSourceCases is the exact set of cases the fixture holds: an
// unlisted case and a deleted case both fail.
var requiredContentSourceCases = []string{
	"source-drops-cooked-out-rows-before-position",
	"source-numbering-follows-tasks",
	"source-history-section-restarts-numbering",
	"source-main-and-history-never-dedupe-across",
	"source-duplicate-with-tool-call-replaces-earlier",
	"source-pi-keeps-every-row",
	"source-pi-skips-evidence-validation",
	"source-absent-and-null-turns-are-empty-partitions",
	"source-indented-document-offsets-exact",
	"source-keeps-recorded-index-apart-from-position",
	"source-cooks-once-per-partition",
	"source-uncookable-evidence-refuses",
	"source-uncookable-duplicate-without-content-refuses",
	"source-uncookable-leaves-no-partial-partition",
	"source-uncookable-main-leaves-history-servable",
	"source-absent-partition-keys-refused",
	"source-reads-turn-bytes-only-within-the-document",
	"source-refuses-document-without-session-detail",
	"source-refuses-duplicate-turns-member",
	"source-refuses-turns-that-are-not-an-array",
	"source-refuses-content-after-the-envelope",
}

func loadContentSourceCases(t *testing.T) []contentSourceCase {
	t.Helper()
	cases, err := decodeFixtureRows[contentSourceCase](contentSourceCookYAML)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if c.Name == "" || c.Why == "" || c.Harness == "" || seen[c.Name] {
			t.Fatalf("content source case must have a unique name, a reason and a harness: %+v", c.Name)
		}
		seen[c.Name] = true
	}
	for _, name := range requiredContentSourceCases {
		if !seen[name] {
			t.Fatalf("required content source case %q is missing from the fixture", name)
		}
		delete(seen, name)
	}
	for name := range seen {
		t.Fatalf("content source case %q is not in the required-name manifest; add it", name)
	}
	return cases
}

// document builds the canonical envelope the case describes.
func (c contentSourceCase) built(t *testing.T) []byte {
	t.Helper()
	if c.Document != "" {
		return []byte(c.Document)
	}
	var b strings.Builder
	harness, err := json.Marshal(c.Harness)
	if err != nil {
		t.Fatal(err)
	}
	b.WriteString(`{"contractVersion":"0.1.0","kind":"session_detail","sessionDetail":{"id":"fixture","harness":`)
	b.Write(harness)
	if !c.OmitMain {
		main := c.Main
		if main == "" {
			main = "[]"
		}
		b.WriteString(`,"turns":` + main)
	}
	if len(c.History) > 0 {
		b.WriteString(`,"earlierHistory":[`)
		for i, turns := range c.History {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`{"state":"uncertain_migrated","turns":` + turns + `}`)
		}
		b.WriteByte(']')
	}
	b.WriteString(`}}`)
	document := []byte(b.String())
	if c.Indent {
		var indented bytes.Buffer
		if err := json.Indent(&indented, document, "", "  "); err != nil {
			t.Fatalf("indent the fixture document: %v", err)
		}
		document = indented.Bytes()
	}
	return document
}

func (c contentSourceCase) partitionTurns(key partitionKey) string {
	if key.Kind == schema.TranscriptPartitionMain {
		return c.Main
	}
	return c.History[key.Section]
}

func contentSourceKey(t *testing.T, read string) partitionKey {
	t.Helper()
	if read == string(schema.TranscriptPartitionMain) {
		return mainPartitionKey()
	}
	section, found := strings.CutPrefix(read, "history-")
	n, err := strconv.Atoi(section)
	if !found || err != nil {
		t.Fatalf("fixture read %q is neither main nor history-N", read)
	}
	return historyPartitionKey(n)
}

func contentSourceExpectFor(c contentSourceCase, key partitionKey) (contentSourcePartitionExpect, bool) {
	if key.Kind == schema.TranscriptPartitionMain {
		if c.Expect.Main == nil {
			return contentSourcePartitionExpect{}, false
		}
		return *c.Expect.Main, true
	}
	if key.Section >= len(c.Expect.History) {
		return contentSourcePartitionExpect{}, false
	}
	return c.Expect.History[key.Section], true
}

func recordedString(state schema.TranscriptRecordedIndexState, index *int) string {
	if state == schema.TranscriptRecordedIndexPresent && index != nil {
		return fmt.Sprintf("%s:%d", state, *index)
	}
	return string(state)
}

func TestCookNumberContentSource(t *testing.T) {
	for _, c := range loadContentSourceCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			cookCalls := 0
			index, err := newTranscriptIndex(transcriptIndexInput{
				Revision: 3,
				Bytes:    c.built(t),
				Cook: func(turns []schema.TranscriptCookTurn, harness schema.Harness) ([]schema.CookedTurn, error) {
					cookCalls++
					return schema.CookTranscriptRows(turns, harness)
				},
			})
			if c.Expect.Construct != "" {
				if !errors.Is(err, errUncookable) || index != nil {
					t.Fatalf("construction = (%v, %v), want the uncookable refusal and no index", index, err)
				}
				if cookCalls != 0 {
					t.Fatalf("a refused construction cooked %d partitions", cookCalls)
				}
				return
			}
			if err != nil {
				t.Fatalf("construct the content source: %v", err)
			}
			if index.Revision.String() != "3" || index.Harness != canonicalHarness(c.Harness) {
				t.Fatalf("index identifies revision %s harness %q, want 3 and %q", index.Revision, index.Harness, c.Harness)
			}
			if cookCalls != 0 {
				t.Fatalf("construction cooked %d partitions; partitions are indexed on first use", cookCalls)
			}

			reads := c.Reads
			if len(reads) == 0 {
				reads = []string{"main"}
				for i := range c.History {
					reads = append(reads, fmt.Sprintf("history-%d", i))
				}
			}
			for _, read := range reads {
				key := contentSourceKey(t, read)
				want, ok := contentSourceExpectFor(c, key)
				if !ok {
					t.Fatalf("fixture reads %s but expects nothing of it", read)
				}
				partition, err := index.Partition(key, noTranscriptBuildReserve)
				if want.Refuse != "" {
					if want.Refuse != "uncookable" || !errors.Is(err, errUncookable) || partition != nil {
						t.Fatalf("%s = (%v, %v), want the uncookable refusal and no partial partition", read, partition, err)
					}
					if _, held := index.Partitions[key]; held {
						t.Fatalf("%s: a refused partition was kept in the index", read)
					}
					continue
				}
				if err != nil {
					t.Fatalf("%s: %v", read, err)
				}
				assertContentSourcePartition(t, index, c, key, partition, want)
			}
			if c.CookCalls != nil && cookCalls != *c.CookCalls {
				t.Fatalf("the cooking pass ran %d times, want %d", cookCalls, *c.CookCalls)
			}
			for _, absent := range c.Absent {
				if _, err := index.Partition(partitionKey{Kind: absent.Kind, Section: absent.Section}, noTranscriptBuildReserve); !errors.Is(err, errPartitionAbsent) {
					t.Fatalf("partition %+v = %v, want errPartitionAbsent", absent, err)
				}
			}
			for _, r := range c.Ranges {
				got, err := index.ReadTurnBytes(context.Background(), r.Start, r.End)
				if (err == nil) != r.OK {
					t.Fatalf("ReadTurnBytes(%d, %d) error = %v, want ok=%v", r.Start, r.End, err, r.OK)
				}
				if r.OK && !bytes.Equal(got, index.Bytes[r.Start:r.End]) {
					t.Fatalf("ReadTurnBytes(%d, %d) returned bytes other than the held range", r.Start, r.End)
				}
			}
		})
	}
}

func assertContentSourcePartition(t *testing.T, index *transcriptIndex, c contentSourceCase, key partitionKey, got *partitionIndex, want contentSourcePartitionExpect) {
	t.Helper()
	if got.TotalTurns != len(want.Rows) || len(got.Rows) != len(want.Rows) {
		t.Fatalf("%+v serves %d rows (total %d), want %d", key, len(got.Rows), got.TotalTurns, len(want.Rows))
	}
	if len(got.Verdict) != want.StoredTotal {
		t.Fatalf("%+v holds %d verdicts, want one per stored row (%d)", key, len(got.Verdict), want.StoredTotal)
	}
	kept := map[int]bool{}
	for served, row := range want.Rows {
		have := got.Rows[served]
		if have.Stored != row.Stored || have.DisplayNumber != row.Number || have.ServedPosition != served {
			t.Fatalf("%+v row %d = stored %d served %d number %q, want stored %d served %d number %q",
				key, served, have.Stored, have.ServedPosition, have.DisplayNumber, row.Stored, served, row.Number)
		}
		if row.Recorded != "" && recordedString(have.RecordedIndexState, have.RecordedIndex) != row.Recorded {
			t.Fatalf("%+v row %d recorded index = %s, want %s", key, served, recordedString(have.RecordedIndexState, have.RecordedIndex), row.Recorded)
		}
		kept[row.Stored] = true
	}
	for stored, verdict := range got.Verdict {
		if verdict.Position != stored || verdict.Keep != kept[stored] {
			t.Fatalf("%+v verdict %d = %+v, want position %d keep %v", key, stored, verdict, stored, kept[stored])
		}
		if !verdict.Keep && (verdict.ServedPosition != -1 || verdict.DisplayNumber != "") {
			t.Fatalf("%+v removed stored row %d carries served position %d number %q", key, stored, verdict.ServedPosition, verdict.DisplayNumber)
		}
	}

	var stored []json.RawMessage
	if turns := c.partitionTurns(key); turns != "" {
		// An absent or null array decodes to no rows.
		if err := json.Unmarshal([]byte(turns), &stored); err != nil {
			t.Fatalf("decode the fixture partition: %v", err)
		}
	}
	for served, have := range got.Rows {
		raw, err := index.ReadTurnBytes(context.Background(), have.Start, have.End)
		if err != nil {
			t.Fatalf("%+v row %d: %v", key, served, err)
		}
		var compact, wantCompact bytes.Buffer
		if err := json.Compact(&compact, raw); err != nil {
			t.Fatalf("%+v row %d bytes are not one JSON value: %v", key, served, err)
		}
		if err := json.Compact(&wantCompact, stored[have.Stored]); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(compact.Bytes(), wantCompact.Bytes()) {
			t.Fatalf("%+v row %d (stored %d) bytes = %s, want the stored turn %s", key, served, have.Stored, compact.Bytes(), wantCompact.Bytes())
		}
		if !bytes.HasPrefix(index.Bytes[have.Start:], raw) || index.Bytes[have.Start] != '{' || index.Bytes[have.End-1] != '}' {
			t.Fatalf("%+v row %d range [%d, %d) is not one turn object of the held document", key, served, have.Start, have.End)
		}
	}
	rangeOfPartition := partitionByteRange{}
	for _, r := range index.Ranges {
		if r.Key == key {
			rangeOfPartition = r
		}
	}
	for _, have := range got.Rows {
		if have.Start < rangeOfPartition.Start || have.End > rangeOfPartition.End {
			t.Fatalf("%+v row range [%d, %d) leaves its partition's array [%d, %d)", key, have.Start, have.End, rangeOfPartition.Start, rangeOfPartition.End)
		}
	}
}

func TestCookNumberContentSourceSessionDetailIsAViewOfTheHeldBytes(t *testing.T) {
	cases := loadContentSourceCases(t)
	var c contentSourceCase
	for _, candidate := range cases {
		if candidate.Name == "source-indented-document-offsets-exact" {
			c = candidate
		}
	}
	index, err := newTranscriptIndex(transcriptIndexInput{Bytes: c.built(t)})
	if err != nil {
		t.Fatal(err)
	}
	detail := index.SessionDetail()
	if len(detail) == 0 || detail[0] != '{' || detail[len(detail)-1] != '}' {
		t.Fatalf("session detail %q is not the served object", detail)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(detail, &members); err != nil {
		t.Fatalf("session detail is not one JSON object: %v", err)
	}
	if _, ok := members["turns"]; !ok || members["id"] == nil {
		t.Fatalf("session detail members = %v, want the served detail's own members", members)
	}
	if &detail[0] != &index.Bytes[index.layout.Detail.Start] {
		t.Fatal("session detail is a copy; it must be a view of the held bytes")
	}
	if index.HistorySections() != 1 {
		t.Fatalf("history sections = %d, want 1", index.HistorySections())
	}
}

//go:embed testdata/content_source_normalize.yaml
var contentSourceNormalizeYAML []byte

type contentSourceNormalizeCase struct {
	Name            string `yaml:"name"`
	Why             string `yaml:"why"`
	Stored          string `yaml:"stored"`
	SameBytes       bool   `yaml:"same_bytes"`
	Harness         string `yaml:"harness"`
	ContractVersion string `yaml:"contract_version"`
	TurnCount       int    `yaml:"turn_count"`
}

var requiredContentSourceNormalizeCases = []string{
	"normalize-canonical-envelope-is-held-as-stored",
	"normalize-bare-payload-is-wrapped-in-the-current-envelope",
	"normalize-older-contract-envelope-is-restamped",
}

func TestCookNumberNormalizesStoredShapes(t *testing.T) {
	cases, err := decodeFixtureRows[contentSourceNormalizeCase](contentSourceNormalizeYAML)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if c.Name == "" || c.Why == "" || c.Stored == "" || seen[c.Name] {
			t.Fatalf("normalization case must have a unique name, a reason and stored bytes: %q", c.Name)
		}
		seen[c.Name] = true
	}
	for _, name := range requiredContentSourceNormalizeCases {
		if !seen[name] {
			t.Fatalf("required normalization case %q is missing from the fixture", name)
		}
	}
	if len(seen) != len(requiredContentSourceNormalizeCases) {
		t.Fatalf("the fixture holds cases outside the required-name manifest: %v", seen)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			stored := []byte(c.Stored)
			normalized, err := canonicalContentBytes(context.Background(), stored)
			if err != nil {
				t.Fatalf("normalize: %v", err)
			}
			if string(stored) != c.Stored {
				t.Fatal("normalization changed the stored bytes it was given")
			}
			if c.SameBytes != bytes.Equal(normalized, stored) {
				t.Fatalf("normalized bytes equal the stored bytes = %v, want %v", !c.SameBytes, c.SameBytes)
			}
			var envelope struct {
				ContractVersion string `json:"contractVersion"`
				Kind            string `json:"kind"`
				SessionDetail   struct {
					Harness string            `json:"harness"`
					Turns   []json.RawMessage `json:"turns"`
				} `json:"sessionDetail"`
			}
			if err := json.Unmarshal(normalized, &envelope); err != nil {
				t.Fatalf("normalized bytes are not an envelope: %v", err)
			}
			if envelope.Kind != string(schema.ContentKindSessionDetail) || len(envelope.SessionDetail.Turns) != c.TurnCount {
				t.Fatalf("normalized envelope kind %q holds %d turns, want %q and %d", envelope.Kind, len(envelope.SessionDetail.Turns), schema.ContentKindSessionDetail, c.TurnCount)
			}
			if c.Harness != "" && envelope.SessionDetail.Harness != c.Harness {
				t.Fatalf("normalized harness %q, want %q", envelope.SessionDetail.Harness, c.Harness)
			}
			if c.ContractVersion != "" && envelope.ContractVersion != c.ContractVersion {
				t.Fatalf("normalized contract version %q, want %q", envelope.ContractVersion, c.ContractVersion)
			}
			index, err := newTranscriptIndex(transcriptIndexInput{Bytes: normalized})
			if err != nil {
				t.Fatalf("index the normalized document: %v", err)
			}
			main, err := index.Partition(mainPartitionKey(), noTranscriptBuildReserve)
			if err != nil || len(main.Rows) != c.TurnCount {
				t.Fatalf("main partition = (%v, %v), want %d served rows", main, err, c.TurnCount)
			}
		})
	}
}

// TestCookNumberRefusesInvalidUTF8InsideTurns keeps the header projection's
// refusal of a session detail that is not valid UTF-8 when the invalid byte is
// inside a turn, which the header itself is not given.
func TestCookNumberRefusesInvalidUTF8InsideTurns(t *testing.T) {
	document := []byte("{\"contractVersion\":\"0.1.0\",\"kind\":\"session_detail\",\"sessionDetail\":{\"harness\":\"codex\",\"turns\":[{\"role\":\"user\",\"content\":\"bad \xff byte\",\"depth\":0}]}}")
	if _, err := newTranscriptIndex(transcriptIndexInput{Bytes: document}); !errors.Is(err, errUncookable) {
		t.Fatalf("an invalid UTF-8 byte inside a turn was indexed: %v", err)
	}
}
