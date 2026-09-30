//go:build integration

package handler

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/peasant-labs/schema"
)

//go:embed testdata/personal-stats.yaml
var personalStatsYAML []byte

// requiredPersonalStatsCases is the name manifest for
// testdata/personal-stats.yaml. Exact membership, never a count.
var requiredPersonalStatsCases = []string{
	"totals-cover-every-transcript-the-caller-published",
	"another-person-sees-only-their-own",
	"nothing-published-is-all-zeros",
}

type personalStatsFixture struct {
	World prWorld `yaml:"world"`
	Cases []struct {
		Name   string                `yaml:"name"`
		Viewer string                `yaml:"viewer"`
		Expect personalStatsExpected `yaml:"expect"`
	} `yaml:"cases"`
}

type personalStatsExpected struct {
	TotalTranscripts int32 `yaml:"total_transcripts"`
	TotalTurns       int64 `yaml:"total_turns"`
	TotalDurationMs  int64 `yaml:"total_duration_ms"`
	TotalTokens      int64 `yaml:"total_tokens"`
	PullRequestCount int32 `yaml:"pull_request_count"`
}

func TestPersonalStats_RealPostgres(t *testing.T) {
	fixture, err := decodeFixtureDocument[personalStatsFixture](personalStatsYAML)
	if err != nil {
		t.Fatalf("load testdata/personal-stats.yaml: %v", err)
	}
	present := map[string]struct{}{}
	for _, c := range fixture.Cases {
		if _, repeated := present[c.Name]; repeated {
			t.Fatalf("testdata/personal-stats.yaml repeats %q", c.Name)
		}
		present[c.Name] = struct{}{}
	}
	assertExactTitleFixtureNames(t, "personal-stats", present, requiredPersonalStatsCases)

	pool := govTestPool(t)
	t.Cleanup(pool.Close)
	world := buildPRWorld(t, pool, fixture.World, "personal-stats")
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			h := world.handler(t, pool)
			rec := world.get(t, h, prWorldRouter(h), c.Viewer, "/api/v1/users/me/stats")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d (%s), want 200", rec.Code, rec.Body.String())
			}
			var got schema.VillageUserStats
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode the response: %v", err)
			}
			want := schema.VillageUserStats{TotalTranscripts: c.Expect.TotalTranscripts, TotalTurns: c.Expect.TotalTurns,
				TotalDurationMs: c.Expect.TotalDurationMs, TotalTokens: c.Expect.TotalTokens, PullRequestCount: c.Expect.PullRequestCount}
			if got != want {
				t.Fatalf("stats = %+v, want %+v", got, want)
			}
		})
	}
}
