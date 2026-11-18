//go:build integration

package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/peasant-labs/schema"
)

//go:embed testdata/deeplink_request_set.yaml
var deeplinkRequestSetYAML []byte

// The backend names both runs; the mounted reader's request log is a separate
// assertion. Production handlers and encrypted storage are exercised here.
func TestDeepLinkRequestSet(t *testing.T) {
	f, err := decodeFixtureDocument[struct {
		Name          string `yaml:"name"`
		Turns         int    `yaml:"turns"`
		ContentBytes  int    `yaml:"content_bytes"`
		TargetPercent int    `yaml:"target_percent"`
	}](deeplinkRequestSetYAML)
	if err != nil {
		t.Fatal(err)
	}
	if f.Name != "deeplink-requests-default-run-and-target-segment" || f.Turns < 2 || f.TargetPercent < 95 || f.TargetPercent >= 100 || f.ContentBytes < 1 {
		t.Fatal("required deep-link fixture is missing or invalid")
	}
	var doc bytes.Buffer
	doc.WriteString(`{"contractVersion":"0.1.0","kind":"session_detail","sessionDetail":{"id":"synthetic-session","harness":"claude-code","startTime":"2026-01-01T00:00:00Z","endTime":"2026-01-01T00:05:00Z","durationMins":5,"totalTokens":100,"tokensIn":60,"tokensOut":40,"turnCount":96,"toolCallCount":0,"turns":[`)
	text := strings.Repeat("x", f.ContentBytes)
	details := make([]schema.TurnDetail, 0, f.Turns)
	windowTurns := make([]schema.PositionedWindowTurn, 0, f.Turns)
	for i := 0; i < f.Turns; i++ {
		role := schema.RoleUser
		if i%2 != 0 {
			role = schema.RoleAssistant
		}
		content := fmt.Sprintf("%d%s", i, text)
		if i != 0 {
			doc.WriteByte(',')
		}
		fmt.Fprintf(&doc, `{"index":%d,"role":%q,"content":%q,"timestamp":"2026-01-01T00:00:01Z","depth":0}`, i, role, content)
		details = append(details, schema.TurnDetail{Index: i, Role: role, Content: content})
		index := i
		windowTurns = append(windowTurns, schema.PositionedWindowTurn{Position: i, Role: role, RecordedIndex: &index})
	}
	doc.WriteString(`]}}`)
	pool := publishLockPool(t, 8)
	t.Cleanup(pool.Close)
	world := newOrderingWorld(t, pool, publishEvidenceObjects(t), 54001, doc.Bytes())
	world.handler.contentCache = newTranscriptContentCache(transcriptContentCacheConfig{})
	mux := chi.NewRouter()
	mux.Get("/transcripts/{id}/segments", world.handler.GetTranscriptSegments)
	mux.Get("/transcripts/{id}/window", world.handler.GetTranscriptWindow)
	base := "/transcripts/" + uuidFromPg(world.id).String()
	get := func(path string, out any) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, base+path, nil)
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, world.owner))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", path, w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatal(err)
		}
	}
	var directory schema.PositionedDirectory
	get("/segments?v=2", &directory)
	cuts := schema.CutTranscriptSegments(details, schema.TranscriptSegmentBudgetBytes)
	if len(cuts) < 5 || len(cuts) != len(directory.Segments) {
		t.Fatalf("synthetic subject produced %d contract cuts and %d directory refs", len(cuts), len(directory.Segments))
	}
	refs := make([]schema.PositionedSegmentRef, len(cuts))
	for i, cut := range cuts {
		refs[i] = schema.PositionedSegmentRef{Index: cut.Index, SourceFirstPosition: cut.FirstTurn, SourceLastPosition: cut.LastTurn, ByteLength: cut.ByteLength, ConversationTurns: cut.ConversationTurns}
		if !reflect.DeepEqual(refs[i], directory.Segments[i]) {
			// The directory additionally carries the recorded endpoint values.
			first, last := cut.FirstTurn, cut.LastTurn
			refs[i].FirstTurn, refs[i].LastTurn = &first, &last
			if !reflect.DeepEqual(refs[i], directory.Segments[i]) {
				t.Fatalf("directory ref %d disagrees with contract cut", i)
			}
		}
	}
	target := f.Turns * f.TargetPercent / 100
	var plain, far schema.PositionedWindow
	get("/window?v=2", &plain)
	get(fmt.Sprintf("/window?v=2&anchor=%d", target), &far)
	input := schema.PositionedWindowInput{Context: directory.Context, Partition: directory.Partition, Turns: windowTurns, Refs: refs}
	expectedPlain, err := schema.ResolvePositionedWindow(input)
	if err != nil {
		t.Fatal(err)
	}
	input.RequestedAnchor = &target
	expectedFar, err := schema.ResolvePositionedWindow(input)
	if err != nil {
		t.Fatal(err)
	}
	// Model the reader's union: overlapping runs are fetched once. A duplicate
	// inside either response is an error, not silently repaired by the union.
	union := func(windows ...schema.PositionedWindow) []int {
		t.Helper()
		set := map[int]bool{}
		for _, window := range windows {
			seen := map[int]bool{}
			for _, ordinal := range window.Segments {
				if seen[ordinal] || ordinal < 0 || ordinal >= len(refs) {
					t.Fatalf("duplicate or outside ordinal %d", ordinal)
				}
				seen[ordinal] = true
				set[ordinal] = true
			}
		}
		out := make([]int, 0, len(set))
		for ordinal := range set {
			out = append(out, ordinal)
		}
		sort.Ints(out)
		return out
	}
	if got, want := union(plain), union(expectedPlain); !reflect.DeepEqual(got, want) {
		t.Fatalf("plain set %v, want %v", got, want)
	}
	if got, want := union(plain, far), union(expectedPlain, expectedFar); !reflect.DeepEqual(got, want) {
		t.Fatalf("deep-link set %v, want %v", got, want)
	}
}
