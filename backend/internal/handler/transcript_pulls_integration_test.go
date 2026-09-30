//go:build integration

package handler

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/peasant-labs/schema"
)

//go:embed testdata/transcript-pulls.yaml
var transcriptPullsYAML []byte

// requiredTranscriptPullsCases is the name manifest for
// testdata/transcript-pulls.yaml. Exact membership, never a count.
var requiredTranscriptPullsCases = []string{
	"owner-sees-every-listed-pull-request",
	"collective-member-sees-private-repository-pull-requests",
	"anonymous-reader-sees-public-repository-pull-requests",
	"signed-in-stranger-sees-public-repository-pull-requests",
	"pending-join-request-is-not-membership",
	"repository-reader-outside-the-collective-sees-public-repository-pull-requests",
	"titles-are-unknown-when-github-cannot-describe-them",
	"member-reads-a-transcript-shared-with-the-team",
	"owner-reads-a-private-transcript",
	"non-reader-of-a-private-transcript-gets-404",
	"repository-reader-outside-the-collective-gets-404-on-a-transcript-shared-only-with-the-team",
	"anonymous-non-reader-of-a-shared-transcript-gets-404",
}

type transcriptPullsFixture struct {
	World prWorld              `yaml:"world"`
	Cases []transcriptPullCase `yaml:"cases"`
}

type transcriptPullCase struct {
	Name        string                   `yaml:"name"`
	Viewer      string                   `yaml:"viewer"`
	Transcript  string                   `yaml:"transcript"`
	GitHubFails []string                 `yaml:"github_fails"`
	Status      int                      `yaml:"status"`
	Expect      []transcriptPullExpected `yaml:"expect"`
}

type transcriptPullExpected struct {
	Ref     string  `yaml:"ref"`
	State   string  `yaml:"state"`
	Title   *string `yaml:"title"`
	HeadRef *string `yaml:"head_ref"`
}

func loadTranscriptPullsFixture(t *testing.T) transcriptPullsFixture {
	t.Helper()
	fixture, err := decodeFixtureDocument[transcriptPullsFixture](transcriptPullsYAML)
	if err != nil {
		t.Fatalf("load testdata/transcript-pulls.yaml: %v", err)
	}
	present := map[string]struct{}{}
	for _, c := range fixture.Cases {
		if _, repeated := present[c.Name]; repeated {
			t.Fatalf("testdata/transcript-pulls.yaml repeats %q", c.Name)
		}
		present[c.Name] = struct{}{}
		if c.Status != http.StatusOK && len(c.Expect) != 0 {
			t.Fatalf("case %q expects rows from a %d; a refusal carries none", c.Name, c.Status)
		}
		for _, failure := range c.GitHubFails {
			if failure != "pulls" {
				t.Fatalf("case %q makes GitHub fail %q; only pull request reads (pulls) are asked of it", c.Name, failure)
			}
		}
	}
	assertExactTitleFixtureNames(t, "transcript-pulls", present, requiredTranscriptPullsCases)
	return fixture
}

func TestTranscriptPullRequests_RealPostgres(t *testing.T) {
	fixture := loadTranscriptPullsFixture(t)
	pool := govTestPool(t)
	// Closed after the world's own teardown, which t.Cleanup runs first.
	t.Cleanup(pool.Close)
	world := buildPRWorld(t, pool, fixture.World, "transcript-pulls")

	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			world.github.failPullReads(len(c.GitHubFails) > 0)
			defer world.github.failPullReads(false)
			_, asksBefore := world.github.counts()

			h := world.handler(t, pool)
			transcriptID, ok := world.transcripts[c.Transcript]
			if !ok {
				t.Fatalf("case names unknown transcript %q", c.Transcript)
			}
			routes := prWorldRouter(h)
			rec := world.get(t, h, routes, c.Viewer,
				"/api/v1/transcripts/"+uuid.UUID(transcriptID.Bytes).String()+"/pulls")
			if rec.Code != c.Status {
				t.Fatalf("status = %d (%s), want %d", rec.Code, rec.Body.String(), c.Status)
			}
			if _, asks := world.github.counts(); asks != asksBefore {
				t.Fatalf("the read asked GitHub %d permission question(s); it never asks what a viewer may read", asks-asksBefore)
			}
			if c.Status == http.StatusNotFound {
				// The refusal must not tell the caller the transcript exists: it
				// is the very answer a transcript that does not exist gets.
				missing := world.get(t, h, routes, c.Viewer, "/api/v1/transcripts/"+uuid.NewString()+"/pulls")
				if missing.Code != http.StatusNotFound || missing.Body.String() != rec.Body.String() {
					t.Fatalf("refusal = %d %s, but a transcript that does not exist answers %d %s; the two must be identical",
						rec.Code, rec.Body.String(), missing.Code, missing.Body.String())
				}
			}
			if c.Status != http.StatusOK {
				return
			}
			var got schema.VillageTranscriptPullRequestsResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode the response: %v", err)
			}
			if err := got.Validate(); err != nil {
				t.Fatalf("the response does not satisfy the contract: %v", err)
			}
			if len(got.PullRequests) != len(c.Expect) {
				t.Fatalf("listed %d pull request(s) %s, want %v", len(got.PullRequests), describePullRows(got.PullRequests), c.Expect)
			}
			for i, want := range c.Expect {
				row := got.PullRequests[i]
				ref := fmt.Sprintf("%s/%s#%d", row.Owner, row.Name, row.Number)
				if ref != want.Ref || string(row.State) != want.State {
					t.Fatalf("row %d = %s %s, want %s %s (all rows: %s)", i, ref, row.State,
						want.Ref, want.State, describePullRows(got.PullRequests))
				}
				if !equalOptionalString(row.Title, want.Title) || !equalOptionalString(row.HeadRef, want.HeadRef) {
					t.Fatalf("row %d (%s) title/head_ref = %s/%s, want %s/%s", i, ref,
						describeOptional(row.Title), describeOptional(row.HeadRef), describeOptional(want.Title), describeOptional(want.HeadRef))
				}
			}
		})
	}
}

func describePullRows(rows []schema.VillageTranscriptPullRequest) string {
	out := "["
	for i, row := range rows {
		if i > 0 {
			out += " "
		}
		out += fmt.Sprintf("%s/%s#%d", row.Owner, row.Name, row.Number)
	}
	return out + "]"
}

func equalOptionalString(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func describeOptional(value *string) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprintf("%q", *value)
}
