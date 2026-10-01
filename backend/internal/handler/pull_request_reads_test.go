package handler

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/pull-request-visibility.yaml
var pullRequestVisibilityYAML []byte

// requiredPullRequestVisibilityCases is the name manifest for
// testdata/pull-request-visibility.yaml. Exact membership, never a count.
var requiredPullRequestVisibilityCases = []string{
	"public-repository-attached-is-listed-to-anyone",
	"public-repository-detached-is-listed-to-anyone",
	"private-repository-is-omitted-for-an-anonymous-reader",
	"private-repository-attached-is-listed-to-its-author",
	"private-repository-detached-is-listed-to-its-author",
	"private-repository-attached-is-listed-to-a-collective-member",
	"private-repository-detached-is-listed-to-a-collective-member",
	"private-repository-is-omitted-for-a-repository-reader-outside-the-collective",
	"private-repository-is-omitted-for-a-signed-in-stranger",
}

type pullRequestVisibilityCase struct {
	Name    string `yaml:"name"`
	Viewer  string `yaml:"viewer"`
	Private bool   `yaml:"private"`
	State   string `yaml:"state"`
	Listed  bool   `yaml:"listed"`
}

func loadPullRequestVisibilityCases(t *testing.T) []pullRequestVisibilityCase {
	t.Helper()
	file, err := decodeFixtureDocument[struct {
		Cases []pullRequestVisibilityCase `yaml:"cases"`
	}](pullRequestVisibilityYAML)
	if err != nil {
		t.Fatalf("load the pull request visibility fixture: %v", err)
	}
	present := map[string]struct{}{}
	for _, c := range file.Cases {
		if _, repeated := present[c.Name]; repeated {
			t.Fatalf("the pull request visibility fixture repeats %q", c.Name)
		}
		switch c.Viewer {
		case "anonymous", "author", "member", "reader", "stranger":
		default:
			t.Fatalf("case %q names viewer %q, which is not one the fixture defines", c.Name, c.Viewer)
		}
		if c.State != "attached" && c.State != "detached" {
			t.Fatalf("case %q uses state %q; the read lists only attached and detached attachments, and the database proof covers the rest", c.Name, c.State)
		}
		present[c.Name] = struct{}{}
	}
	assertExactTitleFixtureNames(t, "pull-request-visibility", present, requiredPullRequestVisibilityCases)
	return file.Cases
}

// TestTranscriptPullRequestsApplyTheVisibilityRule drives the production read
// with one candidate per case and asserts whether it is listed, and that GitHub
// was never asked what the viewer may read.
func TestTranscriptPullRequestsApplyTheVisibilityRule(t *testing.T) {
	for _, c := range loadPullRequestVisibilityCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			author := uuid.New()
			viewer := uuid.New()
			transcriptID := uuid.New()
			fake := newPullReadsGitHub(t)
			fake.setPull("acme/app", 42, "Tighten the ingest retry", "fix/ingest-retry")
			fake.setReader("4242", "reading-octocat", "acme/app", map[string]string{"reader": "read", "stranger": "none"}[c.Viewer])

			var user *AuthUser
			if c.Viewer != "anonymous" {
				id := viewer
				if c.Viewer == "author" {
					id = author
				}
				user = &AuthUser{ID: id, Username: "viewer"}
			}
			q := &mockQuerier{
				getTranscriptByID: func(_ context.Context, id pgtype.UUID) (sqlc.Transcript, error) {
					return sqlc.Transcript{ID: id, OwnerID: toPgUUID(author), Visibility: dbVisibilityPublic}, nil
				},
				getUserByID: func(_ context.Context, id pgtype.UUID) (sqlc.User, error) {
					return sqlc.User{ID: id, Provider: githubProvider, ProviderUserID: "4242"}, nil
				},
				listPullRequestCandidatesByTranscripts: func(_ context.Context, arg sqlc.ListPullRequestCandidatesByTranscriptsParams) ([]sqlc.ListPullRequestCandidatesByTranscriptsRow, error) {
					if len(arg.TranscriptIds) != 1 || arg.TranscriptIds[0] != toPgUUID(transcriptID) {
						t.Fatalf("the candidate read asked about %v, want the requested transcript", arg.TranscriptIds)
					}
					if strings.Join(arg.States, ",") != "attached,detached" {
						t.Fatalf("the candidate read asked for states %v; the transcript read lists attached and detached only", arg.States)
					}
					if (user == nil) == arg.ViewerID.Valid {
						t.Fatalf("the candidate read carried viewer %+v for viewer %q", arg.ViewerID, c.Viewer)
					}
					return []sqlc.ListPullRequestCandidatesByTranscriptsRow{{
						TranscriptID: toPgUUID(transcriptID), RepoOwner: "acme", RepoName: "app", Number: 42,
						State: c.State, AuthorID: toPgUUID(author), InstallationID: 4242,
						IsPrivate: c.Private, ViewerIsMember: c.Viewer == "member",
					}}, nil
				},
			}
			h := newTestHandler(q, nil)
			h.gh = fake.client(t)

			r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+transcriptID.String()+"/pulls", nil)
			r = withChiURLParam(r, "id", transcriptID.String())
			if user != nil {
				r = r.WithContext(context.WithValue(r.Context(), UserContextKey, user))
			}
			w := httptest.NewRecorder()
			h.ListTranscriptPullRequests(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d (%s), want 200", w.Code, w.Body.String())
			}
			var got schema.VillageTranscriptPullRequestsResponse
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode the response: %v", err)
			}
			if listed := len(got.PullRequests) == 1; listed != c.Listed {
				t.Fatalf("listed = %v (%s), want %v", listed, w.Body.String(), c.Listed)
			}
			if c.Listed {
				row := got.PullRequests[0]
				if row.Owner != "acme" || row.Name != "app" || row.Number != 42 || string(row.State) != c.State {
					t.Fatalf("row = %+v, want acme/app#42 in state %s", row, c.State)
				}
				if row.Title == nil || *row.Title != "Tighten the ingest retry" || row.HeadRef == nil || *row.HeadRef != "fix/ingest-retry" {
					t.Fatalf("title/head_ref = %v/%v, want GitHub's values", row.Title, row.HeadRef)
				}
				// The row names the pull request and nothing of the attachment's
				// bookkeeping (ids, commit, check run, comment, author).
				var raw struct {
					PullRequests []map[string]json.RawMessage `json:"pull_requests"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
					t.Fatal(err)
				}
				keys := make([]string, 0, len(raw.PullRequests[0]))
				for key := range raw.PullRequests[0] {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				if strings.Join(keys, ",") != "head_ref,name,number,owner,state,title" {
					t.Fatalf("row carries %v, want exactly owner, name, number, title, head_ref, state", keys)
				}
			}
			if _, asks := fake.counts(); asks != 0 {
				t.Fatalf("GitHub was asked %d permission question(s); the pull request reads never ask what a viewer may read", asks)
			}
		})
	}
}

// TestPullRequestDetailsAreRemembered pins the cost of reading titles from
// GitHub: a pull request viewed again within the cache's lifetime is not read
// again, and neither is one GitHub just refused to describe - it is served as
// unknown until the shorter failure window passes, however often it is viewed.
func TestPullRequestDetailsAreRemembered(t *testing.T) {
	fake := newPullReadsGitHub(t)
	fake.setPull("acme/app", 42, "Tighten the ingest retry", "fix/ingest-retry")
	h := newTestHandler(&mockQuerier{}, nil)
	h.gh = fake.client(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		detail := h.pullRequestDetail(ctx, 4242, "ACME", "App", 42)
		if detail.title == nil || *detail.title != "Tighten the ingest retry" || detail.headRef == nil || *detail.headRef != "fix/ingest-retry" {
			t.Fatalf("read %d served %v/%v, want GitHub's title and head branch", i, detail.title, detail.headRef)
		}
	}
	for i := 0; i < 3; i++ {
		if detail := h.pullRequestDetail(ctx, 4242, "acme", "gone", 7); detail.title != nil || detail.headRef != nil {
			t.Fatalf("a pull request GitHub does not describe served %v/%v, want both unknown", detail.title, detail.headRef)
		}
	}
	if reads, _ := fake.counts(); reads != 2 {
		t.Fatalf("GitHub was read %d time(s), want 2: one per pull request, each reused by the next two views", reads)
	}
}

// TestPullRequestDetailReadsAreShared pins that views of one pull request
// arriving while it is being read wait for that read instead of each asking
// GitHub: twenty at once cost one read.
func TestPullRequestDetailReadsAreShared(t *testing.T) {
	fake := newPullReadsGitHub(t)
	fake.setPull("acme/app", 42, "Tighten the ingest retry", "fix/ingest-retry")
	fake.delayPullReads(100 * time.Millisecond)
	h := newTestHandler(&mockQuerier{}, nil)
	h.gh = fake.client(t)

	var wg sync.WaitGroup
	titles := make([]*string, 20)
	for i := range titles {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			titles[i] = h.pullRequestDetail(context.Background(), 4242, "acme", "app", 42).title
		}(i)
	}
	wg.Wait()
	for i, title := range titles {
		if title == nil || *title != "Tighten the ingest retry" {
			t.Fatalf("view %d served title %v, want GitHub's", i, title)
		}
	}
	if reads, _ := fake.counts(); reads != 1 {
		t.Fatalf("twenty simultaneous views read GitHub %d time(s), want 1", reads)
	}
}

// TestPullRequestDetailReadsAreBoundedByTheirDeadline pins what a slow GitHub
// costs: a view waits no longer than the deadline and gets unknown titles, and
// the read that ran out of time is remembered as unknown, so the next view does
// not ask again.
func TestPullRequestDetailReadsAreBoundedByTheirDeadline(t *testing.T) {
	fake := newPullReadsGitHub(t)
	fake.setPull("acme/app", 42, "Tighten the ingest retry", "fix/ingest-retry")
	fake.delayPullReads(5 * time.Second)
	h := newTestHandler(&mockQuerier{}, nil)
	h.gh = fake.client(t)
	h.pullDetailDeadline = 50 * time.Millisecond

	started := time.Now()
	detail := h.pullRequestDetail(context.Background(), 4242, "acme", "app", 42)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("a view waited %s on a GitHub that does not answer, want about the %s deadline", elapsed, h.pullDetailDeadline)
	}
	if detail.title != nil || detail.headRef != nil {
		t.Fatalf("a read that ran out of time served %v/%v, want both unknown", detail.title, detail.headRef)
	}
	// The read may have timed out before its request reached GitHub, so the
	// first view costs at most one read; the next view must cost none.
	readsAfterFirst, _ := fake.counts()
	if again := h.pullRequestDetail(context.Background(), 4242, "acme", "app", 42); again.title != nil {
		t.Fatalf("the next view served %v, want the remembered unknown", again.title)
	}
	if reads, _ := fake.counts(); readsAfterFirst > 1 || reads != readsAfterFirst {
		t.Fatalf("GitHub was read %d time(s) by the first view and %d by the next, want at most 1 and then none: the timed-out read is remembered for the failure window", readsAfterFirst, reads-readsAfterFirst)
	}
}

// heldListWorld is forty uncached pull requests on a GitHub that holds every
// pull request read until the test ends, with the last one's answer already
// remembered behind all the others.
func heldListWorld(t *testing.T, fake *pullReadsGitHub, h *Handler) (candidates []pullRequestCandidate, remembered int) {
	t.Helper()
	candidates = make([]pullRequestCandidate, 40)
	for i := range candidates {
		fake.setPull("acme/app", i+1, "Tighten the ingest retry", "fix/ingest-retry")
		candidates[i] = pullRequestCandidate{owner: "acme", name: "app", number: i + 1, installationID: 4242}
	}
	remembered = len(candidates) - 1
	key := pullRequestKeyOf("acme", "app", remembered+1)
	_, read, _ := h.pullDetails.claim(key, time.Now())
	h.pullDetails.settle(key, read, pullRequestDetailEntry{title: "Remembered title", headRef: "fix/remembered"}, time.Now(), time.Hour)
	return candidates, remembered
}

// readsStartedBy counts the GitHub reads a list started, from the cache's own
// tables: every read it started is either still in flight or has settled into
// an entry. It is exact once the list has returned, because every read is
// claimed before the list's workers return, so it needs no waiting.
func readsStartedBy(h *Handler, alreadyRemembered int) int {
	h.pullDetails.mu.Lock()
	defer h.pullDetails.mu.Unlock()
	return len(h.pullDetails.inflight) + len(h.pullDetails.entries) - alreadyRemembered
}

// assertHeldListAnswers checks what a list on a held GitHub serves: the
// remembered pull request's title, and unknown for every other.
func assertHeldListAnswers(t *testing.T, details []pullRequestDetail, remembered int) {
	t.Helper()
	for i, detail := range details {
		if i == remembered {
			if detail.title == nil || *detail.title != "Remembered title" {
				t.Fatalf("the remembered pull request was served %v, want its remembered title", detail.title)
			}
			continue
		}
		if detail.title != nil || detail.headRef != nil {
			t.Fatalf("pull request %d served %v/%v, want both unknown", i+1, detail.title, detail.headRef)
		}
	}
}

// TestPullRequestDetailsForAListStopWhenTheirCallerLeaves pins that a list
// keeps to its slots and starts no read once its caller has gone. The caller
// leaves only after GitHub has received the list's first reads, so nothing
// here depends on how fast the machine is: exactly that many reads start,
// the remembered title is still served, and the rest are unknown.
func TestPullRequestDetailsForAListStopWhenTheirCallerLeaves(t *testing.T) {
	fake := newPullReadsGitHub(t)
	fake.holdPullReads(t)
	h := newTestHandler(&mockQuerier{}, nil)
	h.gh = fake.client(t)
	h.pullDetailDeadline = time.Minute
	candidates, remembered := heldListWorld(t, fake, h)

	ctx, leave := context.WithCancel(context.Background())
	defer leave()
	go func() {
		fake.awaitPullReads(t, pullRequestDetailFetchers)
		leave()
	}()
	details := h.pullRequestDetailsFor(ctx, candidates)
	assertHeldListAnswers(t, details, remembered)
	if started := readsStartedBy(h, 1); started != pullRequestDetailFetchers {
		t.Fatalf("the list started %d GitHub reads, want exactly its %d slots", started, pullRequestDetailFetchers)
	}
}

// TestPullRequestDetailsForAListStopAtItsDeadline pins the same bound at the
// list's own deadline: against a GitHub that holds every read, the list returns
// at its deadline, starts no more reads than it has slots - a held read frees
// its slot only by timing out, which cannot happen before the list's deadline
// has passed - serves the remembered title, and serves the rest as unknown.
func TestPullRequestDetailsForAListStopAtItsDeadline(t *testing.T) {
	fake := newPullReadsGitHub(t)
	fake.holdPullReads(t)
	h := newTestHandler(&mockQuerier{}, nil)
	h.gh = fake.client(t)
	h.pullDetailDeadline = 100 * time.Millisecond
	candidates, remembered := heldListWorld(t, fake, h)

	details := h.pullRequestDetailsFor(context.Background(), candidates)
	assertHeldListAnswers(t, details, remembered)
	if started := readsStartedBy(h, 1); started > pullRequestDetailFetchers {
		t.Fatalf("the list started %d GitHub reads, want at most its %d slots", started, pullRequestDetailFetchers)
	}
}

// TestPullRequestDetailOutlivesACallerWhoLeaves pins that a read belongs to the
// pull request, not to the caller that started it: the first caller leaving
// early does not cancel it, and the next caller gets GitHub's answer from that
// same read.
func TestPullRequestDetailOutlivesACallerWhoLeaves(t *testing.T) {
	fake := newPullReadsGitHub(t)
	fake.setPull("acme/app", 42, "Tighten the ingest retry", "fix/ingest-retry")
	fake.delayPullReads(200 * time.Millisecond)
	h := newTestHandler(&mockQuerier{}, nil)
	h.gh = fake.client(t)

	leaving, leave := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer leave()
	if first := h.pullRequestDetail(leaving, 4242, "acme", "app", 42); first.title != nil {
		t.Fatalf("a caller who left before GitHub answered was served %v, want unknown", first.title)
	}
	second := h.pullRequestDetail(context.Background(), 4242, "acme", "app", 42)
	if second.title == nil || *second.title != "Tighten the ingest retry" {
		t.Fatalf("the next caller was served %v, want GitHub's title from the read the first caller started", second.title)
	}
	if reads, _ := fake.counts(); reads != 1 {
		t.Fatalf("GitHub was read %d time(s), want 1", reads)
	}
}

// TestPullRequestDetailCacheWindows pins how long each outcome is remembered
// and that the table stays bounded, at explicit instants rather than by sleeping.
func TestPullRequestDetailCacheWindows(t *testing.T) {
	var cache pullRequestDetailCache
	t0 := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	settle := func(key string, entry pullRequestDetailEntry, ttl time.Duration) {
		_, read, start := cache.claim(key, t0)
		if !start {
			t.Fatalf("claiming %s found it remembered or in flight", key)
		}
		cache.settle(key, read, entry, t0, ttl)
	}
	remembered := func(key string, at time.Time) bool {
		_, read, start := cache.claim(key, at)
		if read == nil {
			return true
		}
		if start {
			cache.settle(key, read, pullRequestDetailEntry{}, at, 0)
		}
		return false
	}
	settle("answered", pullRequestDetailEntry{title: "t"}, pullRequestDetailTTL)
	settle("refused", pullRequestDetailEntry{}, pullRequestDetailFailureTTL)
	if !remembered("answered", t0.Add(pullRequestDetailTTL-time.Second)) || remembered("answered", t0.Add(pullRequestDetailTTL)) {
		t.Fatalf("an answer must be remembered for exactly %s", pullRequestDetailTTL)
	}
	if !remembered("refused", t0.Add(pullRequestDetailFailureTTL-time.Second)) || remembered("refused", t0.Add(pullRequestDetailFailureTTL)) {
		t.Fatalf("a refusal must be remembered for exactly %s", pullRequestDetailFailureTTL)
	}
	// peek, which a list answers remembered pull requests with, honours the
	// same window: an expired answer is not served, so a list asks again.
	settle("peeked", pullRequestDetailEntry{title: "t"}, pullRequestDetailTTL)
	if _, ok := cache.peek("peeked", t0.Add(pullRequestDetailTTL-time.Second)); !ok {
		t.Fatal("peek missed an answer still inside its window")
	}
	if _, ok := cache.peek("peeked", t0.Add(pullRequestDetailTTL)); ok {
		t.Fatalf("peek served an answer at the end of its %s window", pullRequestDetailTTL)
	}

	var full pullRequestDetailCache
	for i := 0; i < pullRequestDetailMaxEntries; i++ {
		_, read, _ := full.claim(strconv.Itoa(i), t0)
		full.settle(strconv.Itoa(i), read, pullRequestDetailEntry{}, t0, time.Hour)
	}
	_, read, _ := full.claim("one more", t0)
	full.settle("one more", read, pullRequestDetailEntry{}, t0, time.Hour)
	if len(full.entries) > pullRequestDetailMaxEntries || len(full.inflight) != 0 {
		t.Fatalf("the table holds %d entries and %d reads in flight, want at most %d and none", len(full.entries), len(full.inflight), pullRequestDetailMaxEntries)
	}
}

// TestPullRequestDetailShippedBounds pins the decided bounds themselves, which
// the timing tests read from the constants or override: a four-second
// deadline, four reads at once, an answer remembered two minutes, a refusal,
// failure, or timeout thirty seconds, and at most 4096 remembered pull
// requests. Changing one is a decision, and this is where it shows.
func TestPullRequestDetailShippedBounds(t *testing.T) {
	if pullRequestDetailDeadline != 4*time.Second || pullRequestDetailFetchers != 4 ||
		pullRequestDetailTTL != 2*time.Minute || pullRequestDetailFailureTTL != 30*time.Second ||
		pullRequestDetailMaxEntries != 4096 {
		t.Fatalf("shipped bounds = deadline %s, fetchers %d, answer %s, failure %s, entries %d; want 4s, 4, 2m, 30s, 4096",
			pullRequestDetailDeadline, pullRequestDetailFetchers, pullRequestDetailTTL, pullRequestDetailFailureTTL, pullRequestDetailMaxEntries)
	}
	if h := newTestHandler(&mockQuerier{}, nil); h.detailDeadline() != pullRequestDetailDeadline {
		t.Fatalf("a handler with no deadline set uses %s, want the shipped %s", h.detailDeadline(), pullRequestDetailDeadline)
	}
}

// TestUpdateUserSettingsRefusesAutomaticLinking pins the refusal that stands in
// for automatic pull request linking until the server implements it: turning it
// on is a 400 that writes nothing, and turning it off is the truth already.
func TestUpdateUserSettingsRefusesAutomaticLinking(t *testing.T) {
	writes := 0
	q := &mockQuerier{
		getUserByID: func(_ context.Context, id pgtype.UUID) (sqlc.User, error) {
			return sqlc.User{ID: id, PreviewBeforeAttach: true}, nil
		},
		setUserPreviewBeforeAttach: func(_ context.Context, arg sqlc.SetUserPreviewBeforeAttachParams) (sqlc.User, error) {
			writes++
			return sqlc.User{ID: arg.ID, PreviewBeforeAttach: arg.PreviewBeforeAttach}, nil
		},
	}
	h := newTestHandler(q, nil)
	patch := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPatch, "/api/v1/users/me/settings", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(withTestUser(r.Context()))
		w := httptest.NewRecorder()
		h.UpdateUserSettings(w, r)
		return w
	}

	refused := patch(`{"preview_before_attach":false,"auto_attach_pull_requests":true}`)
	if refused.Code != http.StatusBadRequest || !strings.Contains(decodeError(t, refused.Body.Bytes()), "nothing was changed") {
		t.Fatalf("turning automatic linking on = %d (%s), want 400 saying nothing was changed", refused.Code, refused.Body.String())
	}
	if writes != 0 {
		t.Fatalf("a refused PATCH wrote the settings %d time(s); it must write nothing", writes)
	}

	off := patch(`{"auto_attach_pull_requests":false}`)
	if off.Code != http.StatusOK {
		t.Fatalf("turning automatic linking off = %d (%s), want 200", off.Code, off.Body.String())
	}
	var settings schema.VillageUserSettings
	if err := json.Unmarshal(off.Body.Bytes(), &settings); err != nil {
		t.Fatal(err)
	}
	if settings.AutoAttachPullRequests || !settings.PreviewBeforeAttach {
		t.Fatalf("settings = %+v, want automatic linking off and the stored preview choice", settings)
	}
}
