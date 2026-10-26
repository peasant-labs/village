package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/content-withholding.yaml
var contentWithholdingYAML []byte

type contentWithholdingCase struct {
	Name              string `yaml:"name"`
	Why               string `yaml:"why"`
	OwnerDiscoverable bool   `yaml:"owner_discoverable"`
	Viewer            string `yaml:"viewer"`
	Withheld          bool   `yaml:"withheld"`
}

var requiredContentWithholdingCases = []string{
	"discoverable_owner_to_signed_out_viewer",
	"hidden_owner_to_signed_out_viewer",
	"hidden_owner_to_other_viewer",
	"hidden_owner_to_themselves",
}

// contentWithRepositoryJSON is a stored session whose detail names its
// repository in every field the content route withholds.
func contentWithRepositoryJSON() []byte {
	return []byte(`{
      "id": "sess-repository",
      "provider": "claude",
      "startTime": "2026-01-01T00:00:00Z",
      "endTime": "2026-01-01T00:01:00Z",
      "durationMins": 1,
      "tokensIn": 60,
      "tokensOut": 40,
      "totalTokens": 100,
      "toolCallCount": 0,
      "turnCount": 2,
      "project": "withheld-project-name",
      "workingDirectory": "/home/withheld-person/withheld-project-name",
      "gitBranch": "withheld-branch",
      "gitRemote": "https://github.com/withheld-person/withheld-project-name.git",
      "childSessions": [{"id": "child-1", "startTime": "2026-01-01T00:00:30Z", "project": "withheld-child-project"}],
      "turns": [
        {"index": 0, "role": "user", "content": "hi", "timestamp": "2026-01-01T00:00:01Z", "depth": 0},
        {"index": 1, "role": "assistant", "content": "yo", "timestamp": "2026-01-01T00:00:02Z", "depth": 0}
      ]
    }`)
}

var withheldContentValues = []string{"withheld-project-name", "withheld-person", "withheld-branch", "withheld-child-project"}

// TestWithholdContentRepositoryRemovesHarnessMetadata pins that the harness
// metadata records, which can carry the working directory and branch, leave
// with the repository fields while the conversation stays.
func TestWithholdContentRepositoryRemovesHarnessMetadata(t *testing.T) {
	envelope := []byte(`{"contractVersion":"0.1.1","kind":"session_detail","sessionDetail":{"id":"s","turns":[{"index":0,"content":"keep me"}],` +
		`"nativeMetadata":[{"id":"n1","data":{"cwd":"/home/withheld-native-cwd"}}],` +
		`"retainedUnknown":[{"value":{"gitBranch":"withheld-retained-branch"}}],` +
		`"earlierHistory":[{"state":"uncertain_migrated","turns":[{"index":0,"content":"keep earlier"}],` +
		`"nativeMetadata":[{"id":"n2","data":{"cwd":"/home/withheld-earlier-cwd"}}],` +
		`"retainedUnknown":[{"value":{"gitBranch":"withheld-earlier-branch"}}]}]}}`)
	out, err := withholdContentRepository(envelope)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"withheld-native-cwd", "withheld-retained-branch", "withheld-earlier-cwd", "withheld-earlier-branch", "nativeMetadata", "retainedUnknown"} {
		if bytes.Contains(out, []byte(value)) {
			t.Errorf("withheld content still carries %q: %s", value, out)
		}
	}
	if !bytes.Contains(out, []byte("keep me")) || !bytes.Contains(out, []byte("keep earlier")) {
		t.Errorf("withholding removed the conversation: %s", out)
	}
}

func TestContentRouteWithholdsAnAnonymousOwnersRepository(t *testing.T) {
	cases, err := decodeFixtureRows[contentWithholdingCase](contentWithholdingYAML)
	if err != nil {
		t.Fatalf("load the content-withholding corpus: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		if c.Name == "" || c.Why == "" || present[c.Name] {
			t.Fatalf("content-withholding case %+v must carry a unique name and a reason", c)
		}
		present[c.Name] = true
	}
	for _, name := range requiredContentWithholdingCases {
		if !present[name] {
			t.Fatalf("the content-withholding corpus no longer contains %q; restore it", name)
		}
	}

	const key = "transcripts/10000000-0000-4000-8000-000000000001.bin"
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			owner, other, id := uuid.New(), uuid.New(), uuid.New()
			store := newFakeBlobStore()
			store.put(key, contentWithRepositoryJSON())
			q := publicTranscriptQuerier(key)
			base := q.getTranscriptByID
			q.getTranscriptByID = func(ctx context.Context, tid pgtype.UUID) (sqlc.Transcript, error) {
				row, err := base(ctx, tid)
				row.OwnerID = pgUUIDFrom(owner)
				return row, err
			}
			q.getUserByID = func(_ context.Context, uid pgtype.UUID) (sqlc.User, error) {
				return sqlc.User{ID: uid, GithubUsername: "withheld-person", IsDiscoverable: c.OwnerDiscoverable}, nil
			}
			h := newTestHandler(q, store)

			r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+id.String()+"/content", nil)
			r = withChiURLParam(r, "id", id.String())
			switch c.Viewer {
			case "owner":
				r = r.WithContext(withUserID(r.Context(), owner))
			case "other":
				r = r.WithContext(withUserID(r.Context(), other))
			}
			w := httptest.NewRecorder()
			h.GetTranscriptContent(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("status %d, want 200 (%s): %s", w.Code, c.Why, w.Body.String())
			}
			var envelope struct {
				SessionDetail map[string]json.RawMessage `json:"sessionDetail"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode content: %v", err)
			}
			var children []map[string]json.RawMessage
			if raw, ok := envelope.SessionDetail["childSessions"]; ok {
				if err := json.Unmarshal(raw, &children); err != nil {
					t.Fatal(err)
				}
			}
			if len(children) != 1 {
				t.Fatalf("the child session reference was dropped (%s): %s", c.Why, w.Body.String())
			}
			_, childProject := children[0]["project"]
			// The stored fixture names its repository in these four fields; the
			// harness metadata records are covered by the unit test below.
			for _, field := range []string{"project", "workingDirectory", "gitBranch", "gitRemote"} {
				if _, served := envelope.SessionDetail[field]; served == c.Withheld {
					t.Errorf("sessionDetail.%s served=%v, want withheld=%v (%s)", field, served, c.Withheld, c.Why)
				}
			}
			if childProject == c.Withheld {
				t.Errorf("childSessions[0].project served=%v, want withheld=%v (%s)", childProject, c.Withheld, c.Why)
			}
			if c.Withheld {
				for _, value := range withheldContentValues {
					if bytes.Contains(w.Body.Bytes(), []byte(value)) {
						t.Errorf("the content names the anonymous owner's repository value %q (%s)", value, c.Why)
					}
				}
			}
		})
	}
}
