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

//go:embed testdata/pull-content-withholding.yaml
var pullContentWithholdingYAML []byte

type pullContentCase struct {
	Name   string `yaml:"name"`
	Stored string `yaml:"stored"`
	Caller string `yaml:"caller"`
	Want   string `yaml:"want"`
}

var requiredPullContentCases = []string{
	"member_pulls_bare_detail", "member_pulls_raw_jsonl", "member_pulls_unreadable_content", "owner_pulls_bare_detail",
}

func storedPullContent(t *testing.T, kind string) []byte {
	t.Helper()
	switch kind {
	case "envelope_with_repository":
		return contentWithRepositoryJSON()
	case "raw_jsonl_with_repository":
		return []byte(`[{"role":"user","content":"hi","cwd":"/home/withheld-person/withheld-project-name","gitBranch":"withheld-branch"},` +
			`{"role":"assistant","content":"yo","cwd":"/home/withheld-person/withheld-project-name","gitBranch":"withheld-branch"}]`)
	case "unreadable":
		return []byte(`not a transcript at all`)
	}
	t.Fatalf("unknown stored content %q", kind)
	return nil
}

func TestPullContentWithholdsAnAnonymousOwnersRepository(t *testing.T) {
	cases, err := decodeFixtureRows[pullContentCase](pullContentWithholdingYAML)
	if err != nil {
		t.Fatalf("load the pull-content corpus: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		present[c.Name] = true
	}
	for _, name := range requiredPullContentCases {
		if !present[name] {
			t.Fatalf("the pull-content corpus no longer contains %q; restore it", name)
		}
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			owner, member, tid, group := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			stored := storedPullContent(t, c.Stored)
			q := &mockQuerier{
				getTranscriptByID: func(context.Context, pgtype.UUID) (sqlc.Transcript, error) {
					row := pullTestTranscript(tid, owner, dbVisibilityShared)
					row.BlobSizeBytes = pgtype.Int8{}
					return row, nil
				},
				listApprovedTranscriptShareGroups: func(context.Context, pgtype.UUID) ([]pgtype.UUID, error) {
					return []pgtype.UUID{pgUUIDFrom(group)}, nil
				},
				getGroupMember: func(_ context.Context, arg sqlc.GetGroupMemberParams) (sqlc.GroupMember, error) {
					return sqlc.GroupMember{GroupID: arg.GroupID, UserID: arg.UserID, Role: "member"}, nil
				},
				getUserByID: func(_ context.Context, id pgtype.UUID) (sqlc.User, error) {
					return sqlc.User{ID: id, GithubUsername: "withheld-person", IsDiscoverable: false}, nil
				},
			}
			h := newTestHandler(q, &fixedTranscriptBlobStore{body: string(stored)})
			caller := member
			if c.Caller == "owner" {
				caller = owner
			}
			r := httptest.NewRequest(http.MethodGet, "/api/v1/pull/transcripts/"+tid.String()+"/content", nil)
			r = r.WithContext(withUserID(r.Context(), caller))
			r = withChiURLParam(r, "id", tid.String())
			w := httptest.NewRecorder()
			h.GetPullTranscriptContent(w, r)

			switch c.Want {
			case "unreadable":
				var refusal struct {
					Code  string `json:"code"`
					Error string `json:"error"`
				}
				if w.Code != http.StatusUnprocessableEntity || json.Unmarshal(w.Body.Bytes(), &refusal) != nil || refusal.Code != "content_unreadable_for_anonymous_owner" {
					t.Fatalf("unreadable content answered %d %s; want 422 content_unreadable_for_anonymous_owner", w.Code, w.Body.String())
				}
				if bytes.Contains(w.Body.Bytes(), []byte("retry the")) {
					t.Errorf("a permanent refusal tells the caller to retry: %s", w.Body.String())
				}
			case "served_withheld":
				if w.Code != http.StatusOK {
					t.Fatalf("status %d: %s", w.Code, w.Body.String())
				}
				for _, value := range withheldContentValues {
					if bytes.Contains(w.Body.Bytes(), []byte(value)) {
						t.Errorf("pulled content names the anonymous owner's repository value %q: %s", value, w.Body.String())
					}
				}
				if w.Header().Get("ETag") != "" {
					t.Errorf("withheld content carries the stored blob's ETag")
				}
			case "served_unchanged":
				if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), stored) {
					t.Fatalf("the owner's own pull answered %d with altered bytes: %s", w.Code, w.Body.String())
				}
			default:
				t.Fatalf("case %q wants %q; the outcomes are served_withheld, served_unchanged and unreadable", c.Name, c.Want)
			}
		})
	}
}
