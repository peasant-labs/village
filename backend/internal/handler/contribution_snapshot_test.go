package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestContributionSnapshotCapacityDefault(t *testing.T) {
	cases := []struct {
		configured int
		poolMax    int32
		want       int
	}{
		{0, 10, 2},
		{0, 2, 1},
		{0, 1, 1},
		{0, 16, 4},
		{0, 400, 4},
		{-3, 16, 4},
		{7, 10, 7},
		{1, 400, 1},
	}
	for _, c := range cases {
		if got := contributionSnapshotCapacity(c.configured, c.poolMax); got != c.want {
			t.Errorf("capacity(configured %d, pool %d) = %d, want %d", c.configured, c.poolMax, got, c.want)
		}
	}
}

func TestContributionSnapshotDeadlineDefault(t *testing.T) {
	if got := contributionSnapshotDeadline(0); got != 15*time.Second {
		t.Errorf("unset deadline = %v, want 15s", got)
	}
	if got := contributionSnapshotDeadline(-time.Second); got != 15*time.Second {
		t.Errorf("negative deadline = %v, want the 15s default", got)
	}
	if got := contributionSnapshotDeadline(3 * time.Second); got != 3*time.Second {
		t.Errorf("configured deadline = %v, want 3s", got)
	}
}

func TestContributionSnapshotGateBoundsViewerAndInstance(t *testing.T) {
	var gate contributionSnapshotGate
	ctx := context.Background()
	alice, bob, carol := uuid.New(), uuid.New(), uuid.New()

	releaseAlice, ok := gate.acquire(ctx, alice, 2, time.Second)
	if !ok {
		t.Fatal("the first snapshot was refused")
	}
	if _, ok := gate.acquire(ctx, alice, 2, 20*time.Millisecond); ok {
		t.Fatal("a second concurrent snapshot for one viewer was admitted")
	}
	releaseBob, ok := gate.acquire(ctx, bob, 2, time.Second)
	if !ok {
		t.Fatal("another viewer was refused while the instance had a free slot")
	}
	if _, ok := gate.acquire(ctx, carol, 2, 20*time.Millisecond); ok {
		t.Fatal("a snapshot beyond the instance bound was admitted")
	}
	// A refused attempt must not leave the viewer's own slot taken.
	releaseAlice()
	releaseCarol, ok := gate.acquire(ctx, carol, 2, time.Second)
	if !ok {
		t.Fatal("a freed instance slot was not reusable; the refused attempt leaked state")
	}
	releaseBob()
	releaseCarol()
	again, ok := gate.acquire(ctx, alice, 2, time.Second)
	if !ok {
		t.Fatal("the viewer could not snapshot again after releasing")
	}
	again()

	gate.mu.Lock()
	defer gate.mu.Unlock()
	if len(gate.viewers) != 0 {
		t.Errorf("%d viewer slots outlive their snapshots; the map must not grow with every viewer ever seen", len(gate.viewers))
	}
}

func TestContributionSnapshotGateRefusesACancelledWait(t *testing.T) {
	var gate contributionSnapshotGate
	viewer := uuid.New()
	release, ok := gate.acquire(context.Background(), viewer, 1, time.Second)
	if !ok {
		t.Fatal("setup acquire refused")
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := gate.acquire(ctx, uuid.New(), 1, time.Minute); ok {
		t.Fatal("a cancelled request was admitted")
	}
}

func TestContributionSnapshotGateReleaseIsIdempotent(t *testing.T) {
	var gate contributionSnapshotGate
	viewer := uuid.New()
	release, ok := gate.acquire(context.Background(), viewer, 1, time.Second)
	if !ok {
		t.Fatal("setup acquire refused")
	}
	release()
	release()
	second, ok := gate.acquire(context.Background(), viewer, 1, time.Second)
	if !ok {
		t.Fatal("a double release broke the gate")
	}
	if _, ok := gate.acquire(context.Background(), uuid.New(), 1, 20*time.Millisecond); ok {
		t.Fatal("a double release freed a slot that is still held; the instance bound is now void")
	}
	second()
}

func TestContributionSnapshotGateNeverExceedsTheBoundUnderLoad(t *testing.T) {
	var gate contributionSnapshotGate
	const capacity = 3
	var inside, peak, admitted, refused atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, ok := gate.acquire(context.Background(), uuid.New(), capacity, 200*time.Millisecond)
			if !ok {
				refused.Add(1)
				return
			}
			admitted.Add(1)
			now := inside.Add(1)
			for {
				seen := peak.Load()
				if now <= seen || peak.CompareAndSwap(seen, now) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond)
			inside.Add(-1)
			release()
		}()
	}
	wg.Wait()
	if peak.Load() > capacity {
		t.Fatalf("%d snapshots ran at once, bound is %d", peak.Load(), capacity)
	}
	if admitted.Load() == 0 {
		t.Fatal("nothing was ever admitted")
	}
}

func TestClassifyContributionSnapshotError(t *testing.T) {
	for name, err := range map[string]error{
		"deadline":          context.DeadlineExceeded,
		"wrapped deadline":  fmt.Errorf("read: %w", context.DeadlineExceeded),
		"statement timeout": &pgconn.PgError{Code: "57014", Message: "canceling statement due to statement timeout"},
		"lock timeout":      &pgconn.PgError{Code: "55P03", Message: "canceling statement due to lock timeout"},
		"wrapped pg":        fmt.Errorf("query: %w", &pgconn.PgError{Code: "57014"}),
	} {
		code, ok := classifyContributionSnapshotError(err)
		if !ok || code != "snapshot_timeout" {
			t.Errorf("%s: classified as %q, %v; want snapshot_timeout", name, code, ok)
		}
	}
	for name, err := range map[string]error{
		"nil":            nil,
		"plain":          errors.New("boom"),
		"cancelled":      context.Canceled,
		"other pg error": &pgconn.PgError{Code: "42P01", Message: "relation does not exist"},
	} {
		if code, ok := classifyContributionSnapshotError(err); ok {
			t.Errorf("%s: classified as %q; only a time bound is a snapshot refusal", name, code)
		}
	}
}

func snapshotID(b byte) [16]byte {
	var id [16]byte
	id[0] = b
	id[15] = b
	return id
}

func TestContributionSnapshotSetWritesCompactSortedJSON(t *testing.T) {
	var set contributionSnapshotSet
	project := strings.Repeat("a", 64)
	other := strings.Repeat("b", 64)
	root, helper, bare := snapshotID(0x20), snapshotID(0x10), snapshotID(0x30)
	// Added out of order on purpose: the output must be sorted by id.
	set.addRecord(root, [16]byte{}, false, []byte(project), []byte("shared"), []byte("approved"))
	set.addRecord(helper, root, true, []byte(other), []byte("public"), []byte("pending"))
	set.addRecord(bare, [16]byte{}, false, []byte(project), []byte("private"), nil)
	if set.Len() != 3 {
		t.Fatalf("Len = %d, want 3", set.Len())
	}

	var out bytes.Buffer
	group := uuid.NewString()
	if err := set.writeJSON(&out, group, project); err != nil {
		t.Fatal(err)
	}
	var got struct {
		GroupID     string `json:"group_id"`
		ProjectHash string `json:"project_hash"`
		Count       int    `json:"count"`
		Records     []map[string]any
	}
	raw := out.Bytes()
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("the output is not JSON: %v\n%s", err, raw)
	}
	if len(generic) != 4 {
		t.Errorf("envelope keys = %d, want exactly group_id, project_hash, records, count: %s", len(generic), raw)
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(generic["records"], &got.Records); err != nil {
		t.Fatal(err)
	}
	if got.GroupID != group || got.ProjectHash != project || got.Count != 3 || len(got.Records) != 3 {
		t.Fatalf("envelope = %+v", got)
	}
	wantOrder := []string{uuid.UUID(helper).String(), uuid.UUID(root).String(), uuid.UUID(bare).String()}
	for i, record := range got.Records {
		if record["id"] != wantOrder[i] {
			t.Errorf("record %d id = %v, want %s (sorted by id)", i, record["id"], wantOrder[i])
		}
	}
	h := got.Records[0]
	if h["visibility"] != "public" || h["latest_status"] != "pending" || h["project_hash"] != other ||
		h["parent_transcript_id"] != uuid.UUID(root).String() || len(h) != 5 {
		t.Errorf("helper record = %v", h)
	}
	r := got.Records[1]
	if _, has := r["parent_transcript_id"]; has || len(r) != 4 {
		t.Errorf("a root must omit parent_transcript_id and carry exactly four fields: %v", r)
	}
	b := got.Records[2]
	if _, has := b["latest_status"]; has || len(b) != 3 {
		t.Errorf("a record with no attempt must omit latest_status and carry exactly three fields: %v", b)
	}
	if bytes.Contains(raw, []byte("null")) {
		t.Errorf("the compact form never writes null: %s", raw)
	}
}

func TestContributionSnapshotSetEmptyAndEscaping(t *testing.T) {
	var empty contributionSnapshotSet
	var out bytes.Buffer
	if err := empty.writeJSON(&out, "g", strings.Repeat("c", 64)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"records":[]`) || !strings.Contains(out.String(), `"count":0`) {
		t.Errorf("an empty set must be an empty array and a zero count: %s", out.String())
	}

	var odd contributionSnapshotSet
	odd.addRecord(snapshotID(1), [16]byte{}, false, []byte(`p"q`), []byte("sh\\ared"), []byte("ap\"proved\n"))
	out.Reset()
	if err := odd.writeJSON(&out, "g", "p"); err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Records []struct {
			Visibility   string `json:"visibility"`
			LatestStatus string `json:"latest_status"`
			ProjectHash  string `json:"project_hash"`
		} `json:"records"`
	}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("unsafe text broke the JSON: %v\n%s", err, out.String())
	}
	if r := decoded.Records[0]; r.Visibility != "sh\\ared" || r.LatestStatus != "ap\"proved\n" || r.ProjectHash != `p"q` {
		t.Errorf("round trip = %+v", r)
	}
}

func TestContributionSnapshotSetInternsRepeatedText(t *testing.T) {
	var set contributionSnapshotSet
	project := []byte(strings.Repeat("d", 64))
	for i := 0; i < 1000; i++ {
		id := [16]byte{byte(i), byte(i >> 8)}
		set.addRecord(id, [16]byte{}, false, project, []byte("shared"), []byte("approved"))
	}
	if n := len(set.projects.values); n != 1 {
		t.Errorf("1000 records of one project hold %d distinct project strings, want 1", n)
	}
	if n := len(set.visibilities.values); n != 1 {
		t.Errorf("visibility strings = %d, want 1", n)
	}
}

func TestEscapeLikePattern(t *testing.T) {
	for in, want := range map[string]string{
		"plain":    "plain",
		"100%":     `100\%`,
		"a_b":      `a\_b`,
		`a\b`:      `a\\b`,
		`%_\`:      `\%\_\\`,
		"":         "",
		"Mixed Ca": "Mixed Ca",
	} {
		if got := escapeLikePattern(in); got != want {
			t.Errorf("escapeLikePattern(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProjectHashesNamed(t *testing.T) {
	names := map[string]string{
		"h1": "Orchard Alpha",
		"h2": "Zeta Service",
		"h3": "orchard beta",
	}
	if got := projectHashesNamed("ORCHARD", names); strings.Join(got, ",") != "h1,h3" {
		t.Errorf("case-insensitive substring = %v, want [h1 h3] sorted", got)
	}
	if got := projectHashesNamed("service", names); strings.Join(got, ",") != "h2" {
		t.Errorf("= %v, want [h2]", got)
	}
	if got := projectHashesNamed("%", names); len(got) != 0 {
		t.Errorf("a percent sign is literal text, not a wildcard: %v", got)
	}
	if got := projectHashesNamed("", names); len(got) != 0 {
		t.Errorf("no query matches no project name: %v", got)
	}
}
