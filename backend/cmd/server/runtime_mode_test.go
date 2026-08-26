package main

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/village/backend/internal/backfill"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/storage"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/boot_modes/cases.yaml
var runtimeModeCasesYAML []byte

type runtimeModeCase struct {
	Name          string   `yaml:"name"`
	Args          []string `yaml:"args"`
	Valid         bool     `yaml:"valid"`
	Mode          string   `yaml:"mode"`
	Authority     string   `yaml:"authority"`
	RewrapLimit   int      `yaml:"rewrap_limit"`
	TitleMode     string   `yaml:"title_mode"`
	OriginMode    string   `yaml:"origin_mode"`
	ErrorContains string   `yaml:"error_contains"`
}

func loadRuntimeModeCases(t *testing.T) []runtimeModeCase {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(runtimeModeCasesYAML))
	decoder.KnownFields(true)
	var cases []runtimeModeCase
	if err := decoder.Decode(&cases); err != nil {
		t.Fatalf("decode runtime mode fixture: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("runtime mode fixture has a trailing document: %v", err)
	}
	if len(cases) != 20 {
		t.Fatalf("runtime mode fixture rows=%d, want 20", len(cases))
	}
	return cases
}

func TestParseRuntimeSelection(t *testing.T) {
	for _, tc := range loadRuntimeModeCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			selection, err := parseRuntimeSelection(tc.Args)
			if !tc.Valid {
				if err == nil || !strings.Contains(err.Error(), tc.ErrorContains) {
					t.Fatalf("error=%v, want substring %q", err, tc.ErrorContains)
				}
				if tc.Name == "title backfill invalid value" && strings.Contains(err.Error(), "repair-everything") {
					t.Fatalf("invalid title mode was echoed: %v", err)
				}
				if tc.Name == "origin backfill invalid value" && strings.Contains(err.Error(), "reclassify-everything") {
					t.Fatalf("invalid origin mode was echoed: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := runtimeModeName(selection.Mode()); got != tc.Mode {
				t.Fatalf("mode=%q, want %q", got, tc.Mode)
			}
			if got := authorityName(selection.AuthorityRequirements()); got != tc.Authority {
				t.Fatalf("authority=%q, want %q", got, tc.Authority)
			}
			if selection.RewrapLimit() != tc.RewrapLimit {
				t.Fatalf("rewrap limit=%d, want %d", selection.RewrapLimit(), tc.RewrapLimit)
			}
			if tc.TitleMode != "" && selection.TitleBackfillMode().String() != tc.TitleMode {
				t.Fatalf("title mode=%q, want %q", selection.TitleBackfillMode(), tc.TitleMode)
			}
			if tc.OriginMode != "" && selection.OriginBackfillMode().String() != tc.OriginMode {
				t.Fatalf("origin mode=%q, want %q", selection.OriginBackfillMode(), tc.OriginMode)
			}
		})
	}
}

func runtimeModeName(mode runtimeMode) string {
	switch mode {
	case runtimeModeServe:
		return "serve"
	case runtimeModeMigrateOnly:
		return "migrate-only"
	case runtimeModeContentIdentityBackfill:
		return "content-identity-backfill"
	case runtimeModeTitleBackfill:
		return "title-backfill"
	case runtimeModeOriginBackfill:
		return "origin-backfill"
	case runtimeModeRewrap:
		return "rewrap"
	case runtimeModeSeedCore:
		return "core-seed"
	case runtimeModeSeedPrivacy:
		return "privacy-seed"
	case runtimeModeShareStateCheck:
		return "share-state-check"
	default:
		return "unknown"
	}
}

func TestDispatchRuntimeTitleBackfillUsesStartupPipeline(t *testing.T) {
	titles, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatal(err)
	}
	original := executeTitleBackfill
	t.Cleanup(func() { executeTitleBackfill = original })
	called := false
	executeTitleBackfill = func(_ context.Context, _ *pgxpool.Pool, _ storage.TranscriptBlobStore, got *redact.TitlePipeline, mode backfill.TitleBackfillMode) (backfill.TitleBackfillResult, error) {
		called = true
		if got != titles {
			t.Fatal("dispatch did not reuse the startup-created title pipeline")
		}
		if mode != backfill.TitleBackfillModeApply {
			t.Fatalf("mode=%s, want apply", mode)
		}
		return backfill.TitleBackfillResult{Scanned: 7, Unchanged: 1, WouldUpdate: 6, Updated: 4, Derived: 2, Sanitized: 3, Failed: 2}, errors.New("fixture aggregate failure")
	}
	err = dispatchRuntime(context.Background(), runtimeSelection{mode: runtimeModeTitleBackfill, titleMode: backfill.TitleBackfillModeApply}, nil, nil, nil, nil, titles)
	if !called || err == nil || !strings.Contains(err.Error(), "fixture aggregate failure") {
		t.Fatalf("called=%v error=%v", called, err)
	}
}

func TestDispatchRuntimeOriginBackfillReportsAggregateFailure(t *testing.T) {
	titles, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatal(err)
	}
	original := executeOriginBackfill
	t.Cleanup(func() { executeOriginBackfill = original })
	called := false
	executeOriginBackfill = func(_ context.Context, _ *pgxpool.Pool, _ storage.TranscriptBlobStore, mode backfill.OriginBackfillMode) (backfill.OriginBackfillResult, error) {
		called = true
		if mode != backfill.OriginBackfillModeApply {
			t.Fatalf("mode=%s, want apply", mode)
		}
		return backfill.OriginBackfillResult{Scanned: 9, Unchanged: 4, WouldUpdate: 5, Updated: 3, Failed: 2}, errors.New("fixture origin aggregate failure")
	}
	err = dispatchRuntime(context.Background(), runtimeSelection{mode: runtimeModeOriginBackfill, originMode: backfill.OriginBackfillModeApply}, nil, nil, nil, nil, titles)
	if !called || err == nil || !strings.Contains(err.Error(), "fixture origin aggregate failure") {
		t.Fatalf("called=%v error=%v", called, err)
	}
}

func authorityName(requirements config.AuthorityRequirements) string {
	switch requirements {
	case config.PostgreSQLAuthority:
		return "postgresql"
	case config.ServingAuthority:
		return "serving"
	case config.BlobProcessingAuthority:
		return "blob-processing"
	default:
		return "unknown"
	}
}
