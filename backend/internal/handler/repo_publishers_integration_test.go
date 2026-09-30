//go:build integration

package handler

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/peasant-labs/schema"
)

//go:embed testdata/repo-publishers.yaml
var repoPublishersYAML []byte

// requiredRepoPublishersCases is the name manifest for
// testdata/repo-publishers.yaml. Exact membership, never a count.
var requiredRepoPublishersCases = []string{
	"a-transcript-not-shared-with-the-collective-adds-no-count",
	"a-transcript-shared-with-the-collective-counts-its-owner",
	"a-submission-awaiting-review-adds-no-count",
	"a-transcript-shared-with-another-collective-adds-no-count",
	"one-person-with-two-shared-transcripts-counts-once",
	"two-people-count-two",
	"remote-forms-and-letter-case-name-one-repository",
	"a-remote-on-another-host-names-no-github-repository",
	"each-repository-counts-only-its-own-remotes",
}

type repoPublishersFixture struct {
	People      []string            `yaml:"people"`
	Collectives []prWorldCollective `yaml:"collectives"`
	Available   []string            `yaml:"available"`
	Cases       []struct {
		Name        string              `yaml:"name"`
		Transcripts []prWorldTranscript `yaml:"transcripts"`
		Expect      map[string]int32    `yaml:"expect"`
	} `yaml:"cases"`
}

func TestRepositoryPublisherCounts_RealPostgres(t *testing.T) {
	fixture, err := decodeFixtureDocument[repoPublishersFixture](repoPublishersYAML)
	if err != nil {
		t.Fatalf("load testdata/repo-publishers.yaml: %v", err)
	}
	present := map[string]bool{}
	for _, c := range fixture.Cases {
		if present[c.Name] {
			t.Fatalf("testdata/repo-publishers.yaml repeats %q", c.Name)
		}
		present[c.Name] = true
		if len(c.Expect) != len(fixture.Available) {
			t.Fatalf("case %q names %d count(s); it must name one for each of the %d available repositories", c.Name, len(c.Expect), len(fixture.Available))
		}
		for _, repo := range fixture.Available {
			if _, ok := c.Expect[repo]; !ok {
				t.Fatalf("case %q names no count for available repository %s", c.Name, repo)
			}
		}
	}
	assertExactCaseNames(t, "repo-publishers", present, requiredRepoPublishersCases)

	pool := govTestPool(t)
	t.Cleanup(pool.Close)
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			world := buildPRWorld(t, pool, prWorld{People: fixture.People, Collectives: fixture.Collectives, Transcripts: c.Transcripts}, "repo-publishers")

			// The App is installed on the owner's own account, so the owner
			// controls the installation whose repositories the picker offers.
			repos := make([]string, 0, len(fixture.Available))
			for _, full := range fixture.Available {
				owner, name, _ := strings.Cut(full, "/")
				repos = append(repos, fmt.Sprintf(`{"name":%q,"private":false,"owner":{"login":%q}}`, name, owner))
			}
			installation := &fakeGitHub{
				installationsBody:     fmt.Sprintf(`[{"id":99,"account":{"login":"acme","id":%d,"type":"User"}}]`, world.githubIDs["owner"]),
				installationReposBody: fmt.Sprintf(`{"total_count":%d,"repositories":[%s]}`, len(repos), strings.Join(repos, ",")),
			}
			newFakeGitHub(t, installation)
			h := world.handler(t, pool)
			h.gh = newRepoHandler(t, h.queries, installation).gh

			rec := world.get(t, h, prWorldRouter(h), "owner",
				"/api/v1/groups/"+uuid.UUID(world.collectives["team"].Bytes).String()+"/repositories/available")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
			}
			var got schema.VillageAvailableRepositoriesResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode the response: %v", err)
			}
			counts := map[string]int32{}
			for _, repo := range got.Repositories {
				counts[repo.Owner+"/"+repo.Name] = repo.PublisherCount
			}
			if len(counts) != len(c.Expect) {
				t.Fatalf("the picker offered %v, want exactly %v", counts, c.Expect)
			}
			for repo, want := range c.Expect {
				if counts[repo] != want {
					t.Fatalf("publisher_count for %s = %d, want %d (all: %v)", repo, counts[repo], want, counts)
				}
			}
		})
	}
}
