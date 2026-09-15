//go:build integration

package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/projectname"
	"github.com/peasant-labs/village/backend/internal/sessionorigin"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/collective_helper_group_listing.yaml
var collectiveHelperListingYAML []byte

type collectiveHelperRow struct {
	Name            string                `yaml:"name"`
	LocalID         string                `yaml:"local_id"`
	Title           string                `yaml:"title"`
	Start           int64                 `yaml:"start"`
	Purpose         schema.SessionPurpose `yaml:"purpose"`
	Parent          string                `yaml:"parent"`
	Project         string                `yaml:"project"`
	OtherOwner      bool                  `yaml:"other_owner"`
	OtherCollective bool                  `yaml:"other_collective"`
}
type collectiveHelperCase struct {
	Name           string            `yaml:"name"`
	Route          string            `yaml:"route"`
	Denied         bool              `yaml:"denied"`
	Search         string            `yaml:"search"`
	Status         int               `yaml:"status"`
	Ordinary       int               `yaml:"ordinary"`
	Helpers        int               `yaml:"helpers"`
	Members        []string          `yaml:"members"`
	Mutation       string            `yaml:"mutation"`
	MemberStatus   int               `yaml:"member_status"`
	AfterMembers   []string          `yaml:"after_members"`
	Legacy         bool              `yaml:"legacy"`
	LegacyIDs      []string          `yaml:"legacy_ids"`
	SelectedIDs    []string          `yaml:"selected_ids"`
	ExpectedStates map[string]string `yaml:"expected_states"`
}
type collectiveHelperFixtures struct {
	Rows  []collectiveHelperRow  `yaml:"rows"`
	Cases []collectiveHelperCase `yaml:"cases"`
}

func loadCollectiveHelperFixtures(t *testing.T) collectiveHelperFixtures {
	t.Helper()
	var f collectiveHelperFixtures
	d := yaml.NewDecoder(bytes.NewReader(collectiveHelperListingYAML))
	d.KnownFields(true)
	if err := d.Decode(&f); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		t.Fatalf("expected one fixture document: %v", err)
	}
	names := map[string]bool{}
	rows := map[string]bool{}
	localIDs := map[string]string{}
	for _, row := range f.Rows {
		if row.Name == "" || rows[row.Name] || !row.Purpose.IsValid() {
			t.Fatal("invalid or duplicate row")
		}
		if _, err := schema.NewSessionID(row.LocalID); err != nil {
			t.Fatalf("row %q carries local id %q outside the contract session ID grammar: %v", row.Name, row.LocalID, err)
		}
		if localIDs[row.LocalID] != "" {
			t.Fatalf("duplicate local id %q", row.LocalID)
		}
		rows[row.Name] = true
		localIDs[row.LocalID] = row.Name
	}
	for _, row := range f.Rows {
		if row.Parent != "" && !rows[row.Parent] {
			t.Fatalf("row %q names unknown parent %q", row.Name, row.Parent)
		}
	}
	for _, name := range []string{"P", "G1", "G2", "B", "X"} {
		if !rows[name] {
			t.Fatalf("required scope-control row %s missing", name)
		}
	}
	for _, c := range f.Cases {
		if c.Name == "" || names[c.Name] {
			t.Fatal("invalid or duplicate case")
		}
		names[c.Name] = true
		for _, name := range c.Members {
			if !rows[name] {
				t.Fatalf("%s expects unknown member %s", c.Name, name)
			}
		}
		if c.Status == 0 || c.Members == nil {
			t.Fatalf("%s omits expected status or exact member set", c.Name)
		}
		if c.Mutation != "" && c.MemberStatus == 0 {
			t.Fatalf("%s omits replay status", c.Name)
		}
		if c.Mutation == "contribute-selected" || c.Mutation == "review-selected" {
			if !reflect.DeepEqual(c.SelectedIDs, []string{"G2"}) {
				t.Fatalf("%s must prove exact G2-only selection", c.Name)
			}
			for name := range rows {
				if _, ok := c.ExpectedStates[name]; !ok {
					t.Fatalf("%s omits ledger state for %s", c.Name, name)
				}
			}
			for name := range c.ExpectedStates {
				if !rows[name] {
					t.Fatalf("%s expects unknown ledger row %s", c.Name, name)
				}
			}
		}
	}
	for _, name := range []string{
		"collective-valid", "contributable-valid", "pending-valid", "my-shares-valid",
		"collective-denied", "contributable-denied", "pending-denied", "my-shares-denied",
		"collective-filter-replay", "contributable-filter-replay", "pending-filter-replay", "my-shares-filter-replay",
		"collective-expired", "contributable-expired", "pending-expired", "my-shares-expired",
		"collective-current-authorization", "contributable-current-authorization", "pending-current-authorization", "my-shares-current-authorization",
		"contributable-current-eligibility", "pending-current-eligibility",
		"collective-legacy-flat", "contributable-legacy-flat", "pending-legacy-flat", "my-shares-legacy-flat",
		"contributable-select-second-only", "pending-select-second-only",
	} {
		if !names[name] {
			t.Fatalf("required route case %s missing", name)
		}
	}
	return f
}

func TestCollectiveGroupedRegisteredRoutesRealSQL(t *testing.T) {
	f := loadCollectiveHelperFixtures(t)
	pool := govTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	for index, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			ownerName := fmt.Sprintf("collective-helper-owner-%d", index)
			owner := pullInsertUser(t, ctx, pool, int64(982101+index*2), ownerName)
			strangerName := fmt.Sprintf("collective-helper-stranger-%d", index)
			stranger := pullInsertUser(t, ctx, pool, int64(982102+index*2), strangerName)
			defer cleanupOwners(t, ctx, pool, owner, stranger)
			h := &Handler{cfg: minimalConfig(), pool: pool, queries: sqlc.New(pool), contributableRowLimit: 10000, projectNames: projectname.Resolver{Label: schema.RemoteLabel}}
			var groupID pgtype.UUID
			if err := pool.QueryRow(ctx, `INSERT INTO groups(name, created_by, acceptance_mode, data_access) VALUES($1,$2,'curated','members_only') RETURNING id`, c.Name, owner).Scan(&groupID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO group_members(group_id,user_id,role) VALUES($1,$2,'owner')`, groupID, owner); err != nil {
				t.Fatal(err)
			}
			var otherGroupID pgtype.UUID
			if err := pool.QueryRow(ctx, `INSERT INTO groups(name, created_by, acceptance_mode, data_access) VALUES($1,$2,'curated','members_only') RETURNING id`, c.Name+"-other", stranger).Scan(&otherGroupID); err != nil {
				t.Fatal(err)
			}
			ids := map[string]pgtype.UUID{}
			names := map[string]string{}
			for _, row := range f.Rows {
				rowOwner := owner
				if row.OtherOwner {
					rowOwner = stranger
				}
				stored := govStoreWithOrigin(t, ctx, h, rowOwner, row.LocalID, sessionorigin.Origin("agent"), dbVisibilityShared)
				ids[row.Name] = stored.ID
				names[uuid.UUID(stored.ID.Bytes).String()] = row.Name
				parentLocalID := ""
				if row.Parent != "" {
					for _, candidate := range f.Rows {
						if candidate.Name == row.Parent {
							parentLocalID = candidate.LocalID
						}
					}
				}
				relations := []schema.SessionRelationship{}
				if parentLocalID != "" {
					target := schema.SessionID(parentLocalID)
					relations = append(relations, schema.SessionRelationship{Kind: schema.SessionRelationshipStartedBy, TargetState: schema.RelationshipTargetKnown, TargetLocalID: &target, Evidence: schema.EvidenceNativeTyped})
				}
				encoded, err := json.Marshal(relations)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, `UPDATE transcripts SET title=$2, session_start=$3, project_hash=$4, session_purpose=$5, session_relationships=$6, parent_session_id=NULLIF($7,''), input_submission_count=1, turn_count=5 WHERE id=$1`, stored.ID, row.Title, time.Unix(row.Start, 0), strings.Repeat(row.Project, 64), string(row.Purpose), encoded, parentLocalID); err != nil {
					t.Fatal(err)
				}
				if c.Route != "contributable" {
					status := "approved"
					if c.Route == "pending" {
						status = "pending"
					}
					shareGroupID := groupID
					if row.OtherCollective {
						shareGroupID = otherGroupID
					}
					if err := h.queries.ShareTranscriptWithStatus(ctx, sqlc.ShareTranscriptWithStatusParams{TranscriptID: stored.ID, GroupID: shareGroupID, Status: status}); err != nil {
						t.Fatal(err)
					}
				}
			}
			routes := chi.NewRouter()
			routes.Route("/api/v1", func(r chi.Router) {
				h.RegisterTranscriptBrowseRoutes(r)
				h.RegisterCollectiveBrowseRoutes(r)
				r.With(h.AuthRequired).Post("/groups/{id}/shares", h.BatchShareProject)
				r.With(h.AuthRequired).Patch("/groups/{id}/shares", h.BatchReviewShares)
			})
			ownerToken, err := auth.CreateToken(h.cfg.JWTSecret, uuid.UUID(owner.Bytes), ownerName)
			if err != nil {
				t.Fatal(err)
			}
			strangerToken, err := auth.CreateToken(h.cfg.JWTSecret, uuid.UUID(stranger.Bytes), strangerName)
			if err != nil {
				t.Fatal(err)
			}
			token := ownerToken
			if c.Denied {
				token = strangerToken
			}
			get := func(path, token string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodGet, path, nil)
				r.Header.Set("Authorization", "Bearer "+token)
				w := httptest.NewRecorder()
				routes.ServeHTTP(w, r)
				return w
			}
			path := "/api/v1/groups/" + uuid.UUID(groupID.Bytes).String()
			if c.Route != "collective" {
				path += "/" + c.Route
			}
			query := url.Values{"view": {"grouped"}, "q": {c.Search}, "project_hash": {strings.Repeat("a", 64)}}
			if c.Legacy {
				query = url.Values{}
			}
			w := get(path+"?"+query.Encode(), token)
			if w.Code != c.Status {
				t.Fatalf("status=%d want=%d: %s", w.Code, c.Status, w.Body)
			}
			if w.Code != 200 {
				return
			}
			if c.Legacy {
				body := w.Body.Bytes()
				if c.Route == "collective" || c.Route == "contributable" {
					var wrapper map[string]json.RawMessage
					if err := json.Unmarshal(body, &wrapper); err != nil {
						t.Fatal(err)
					}
					if wrapper["transcriptList"] != nil || wrapper["transcripts"] == nil {
						t.Fatal("legacy wrapper changed")
					}
					body = wrapper["transcripts"]
				}
				var flat []struct {
					ID           string `json:"id"`
					TranscriptID string `json:"transcript_id"`
				}
				if err := json.Unmarshal(body, &flat); err != nil {
					t.Fatal(err)
				}
				got := []string{}
				for _, row := range flat {
					id := row.ID
					if c.Route == "pending" {
						id = row.TranscriptID
					}
					got = append(got, names[id])
				}
				sort.Strings(got)
				if !reflect.DeepEqual(got, c.LegacyIDs) {
					t.Fatalf("legacy IDs=%v want=%v", got, c.LegacyIDs)
				}
				return
			}
			var payload schema.VillageSessionListPayload
			switch c.Route {
			case "collective":
				var response schema.VillageGroupedGroupDetailResponse
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if err := response.Validate(); err != nil {
					t.Fatal(err)
				}
				payload = response.TranscriptList
				if response.Group.ID != schema.VillageUUID(uuid.UUID(groupID.Bytes).String()) || response.Group.Name != c.Name || response.CanRead == c.Denied {
					t.Fatal("collective metadata/access lost")
				}
			case "contributable":
				var response schema.VillageGroupedContributableResponse
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				payload = response.TranscriptList
				if response.GroupID != schema.VillageUUID(uuid.UUID(groupID.Bytes).String()) {
					t.Fatal("collective identity lost")
				}
			default:
				if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
					t.Fatal(err)
				}
			}
			if payload.OrdinarySessionTotal != c.Ordinary || payload.HelperThreadTotal != c.Helpers {
				t.Fatalf("counts ordinary=%d helpers=%d", payload.OrdinarySessionTotal, payload.HelperThreadTotal)
			}
			if c.Helpers == 0 {
				if len(payload.Items) != 0 || payload.TotalItems != 0 {
					t.Fatal("denied/empty set leaked items")
				}
				return
			}
			if len(payload.Items) != 1 || payload.TotalItems != 1 || len(payload.Items[0].HelperGroups) != 1 {
				t.Fatalf("wrong top-level projection: %+v", payload)
			}
			item := payload.Items[0]
			if c.Ordinary == 0 {
				if item.Context == nil || item.Transcript != nil {
					t.Fatal("helper-only result selected a parent")
				}
			} else if item.Transcript == nil || names[string(item.Transcript.Session.ID)] != "P" {
				t.Fatal("ordinary parent lost")
			}
			group := item.HelperGroups[0]
			memberPath := "/api/v1/transcript-groups/" + group.GroupID + "/members?scope=" + group.MemberScope
			assertMembers := func(w *httptest.ResponseRecorder, expected []string) {
				t.Helper()
				if w.Code != 200 {
					t.Fatalf("members status %d: %s", w.Code, w.Body)
				}
				var response schema.VillageHelperMembersPayload
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				got := []string{}
				for _, item := range response.Members {
					if err := item.Validate(); err != nil {
						t.Fatal(err)
					}
					if item.Transcript == nil || item.Context != nil || item.Kind != schema.SessionListItemTranscript {
						t.Fatal("member is not a transcript-only display item")
					}
					member := *item.Transcript
					got = append(got, names[string(member.Session.ID)])
					if member.Session.InputSubmissionCount == nil || *member.Session.InputSubmissionCount != 1 || member.Session.TurnCount == nil || *member.Session.TurnCount != 5 {
						t.Fatal("independent counts lost")
					}
					row := &member
					if (row.Collective != nil) != (c.Route == "collective") || (row.Pending != nil) != (c.Route == "pending") || (row.MyShare != nil) != (c.Route == "my-shares") || (row.Contributable != nil) != (c.Route == "contributable") {
						t.Fatal("member changed route variant")
					}
				}
				if !reflect.DeepEqual(got, expected) || response.Total != len(expected) {
					t.Fatalf("member IDs=%v want=%v total=%d", got, expected, response.Total)
				}
			}
			assertMembers(get(memberPath, token), c.Members)
			switch c.Mutation {
			case "":
				return
			case "expire":
				h.groupedScopes.mu.Lock()
				entry := h.groupedScopes.entries[group.MemberScope]
				entry.expires = time.Now().Add(-time.Second)
				h.groupedScopes.entries[group.MemberScope] = entry
				h.groupedScopes.mu.Unlock()
			case "remove-role":
				if _, err := pool.Exec(ctx, `DELETE FROM group_members WHERE group_id=$1 AND user_id=$2`, groupID, owner); err != nil {
					t.Fatal(err)
				}
			case "change-viewer":
				token = strangerToken
			case "contribute-selected", "review-selected":
				selected := make([]string, 0, len(c.SelectedIDs))
				for _, name := range c.SelectedIDs {
					selected = append(selected, uuid.UUID(ids[name].Bytes).String())
				}
				var body []byte
				method := http.MethodPost
				if c.Mutation == "contribute-selected" {
					body, err = json.Marshal(batchShareRequest{ProjectHash: strings.Repeat("a", 64), TranscriptIDs: selected, VisibilityConfirmed: true})
				} else {
					method = http.MethodPatch
					body, err = json.Marshal(batchReviewRequest{TranscriptIDs: selected, Status: "approved"})
				}
				if err != nil {
					t.Fatal(err)
				}
				r := httptest.NewRequest(method, "/api/v1/groups/"+uuid.UUID(groupID.Bytes).String()+"/shares", bytes.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+token)
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				routes.ServeHTTP(w, r)
				if w.Code != 200 {
					t.Fatalf("explicit mutation status=%d: %s", w.Code, w.Body)
				}
				for name, expected := range c.ExpectedStates {
					var state string
					if err := pool.QueryRow(ctx, `SELECT COALESCE((SELECT status FROM transcript_share_attempts WHERE transcript_id=$1 AND group_id=$2 ORDER BY event_num DESC LIMIT 1),'')`, ids[name], groupID).Scan(&state); err != nil {
						t.Fatal(err)
					}
					if state != expected {
						t.Fatalf("explicit selection changed %s to %s, want %s", name, state, expected)
					}
				}
			case "submit-second":
				if err := h.queries.ShareTranscriptWithStatus(ctx, sqlc.ShareTranscriptWithStatusParams{TranscriptID: ids["G2"], GroupID: groupID, Status: "pending"}); err != nil {
					t.Fatal(err)
				}
			case "approve-second":
				if err := h.queries.UpdateShareStatus(ctx, sqlc.UpdateShareStatusParams{TranscriptID: ids["G2"], GroupID: groupID, Status: "approved", DecidedBy: owner}); err != nil {
					t.Fatal(err)
				}
			default:
				t.Fatalf("unsupported mutation %s", c.Mutation)
			}
			changed := get(memberPath, token)
			if changed.Code != c.MemberStatus {
				t.Fatalf("changed member status=%d want=%d: %s", changed.Code, c.MemberStatus, changed.Body)
			}
			if c.MemberStatus == 200 {
				assertMembers(changed, c.AfterMembers)
			} else if c.MemberStatus == 409 && !strings.Contains(changed.Body.String(), "group_scope_expired") {
				t.Fatal("expiry omitted safe refresh error")
			}
		})
	}
}
