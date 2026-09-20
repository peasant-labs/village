//go:build integration

package handler

// The published grouping oracle: publish one owner session plus two saved
// helper sessions through the registered publish route, then read the grouped
// projection back through the registered browse routes against real PostgreSQL
// and real encrypted object storage.
//
// This is the receiver-side arm that was missing: the helper arm previously
// existed only for in-process seeded rows (helper_group_listing.yaml,
// collective_helper_group_listing.yaml), which never prove that the publish path
// produces the durable relationships and measures the grouping projection reads.
// A seeded row can be written into any shape; a published one has to have been
// accepted, decoded, mirrored, stored, and projected by the production path
// first.
//
// The corpus is fixture-driven (testdata/published_grouped_shape_oracles.yaml,
// with a required-NAME manifest and a typed strict loader) and every case reuses
// the canonical durable payload from session_graph_publication.yaml, so the
// grouping corpus pins only the identity, purpose, measures and relationship
// edges that define the shape instead of forking the content contract.

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/storage"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/published_grouped_shape_oracles.yaml
var publishedGroupedShapeYAML []byte

type publishedShapeSession struct {
	Name      string                `yaml:"name"`
	SessionID string                `yaml:"session_id"`
	Purpose   schema.SessionPurpose `yaml:"purpose"`
	Input     *int64                `yaml:"input"`
	Turns     int32                 `yaml:"turns"`
	StartedBy string                `yaml:"started_by"`
}

type publishedShapeItem struct {
	Session string   `yaml:"session"`
	Members []string `yaml:"members"`
}

type publishedShapeOwnerFacts struct {
	Input *int64 `yaml:"input"`
	Turns int32  `yaml:"turns"`
}

type publishedShapeCase struct {
	Name     string                   `yaml:"name"`
	Publish  []string                 `yaml:"publish"`
	Ordinary int                      `yaml:"ordinary"`
	Helpers  int                      `yaml:"helpers"`
	TopLevel int                      `yaml:"top_level"`
	Items    []publishedShapeItem     `yaml:"items"`
	Owner    publishedShapeOwnerFacts `yaml:"owner"`
}

type publishedShapeFixture struct {
	RequiredNames []string                `yaml:"requiredNames"`
	Sessions      []publishedShapeSession `yaml:"sessions"`
	Cases         []publishedShapeCase    `yaml:"cases"`
}

// loadPublishedShapeFixtures decodes the corpus with unknown fields refused and
// refuses a corpus that drops a required NAME, repeats one, or names a session
// the case cannot resolve. The required-NAME manifest, never a row count, is the
// deletion guard, so removing a case fails by name.
func loadPublishedShapeFixtures(t *testing.T) (map[string]publishedShapeSession, []publishedShapeCase) {
	t.Helper()
	var fixture publishedShapeFixture
	decoder := yaml.NewDecoder(bytes.NewReader(publishedGroupedShapeYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("unexpected trailing YAML: %v", err)
	}
	sessions := map[string]publishedShapeSession{}
	for _, session := range fixture.Sessions {
		if session.Name == "" || sessions[session.Name].Name != "" {
			t.Fatalf("duplicate/empty published shape session %q", session.Name)
		}
		if _, err := schema.NewSessionID(session.SessionID); err != nil {
			t.Fatalf("published shape session %s has an unusable session_id: %v", session.Name, err)
		}
		if !session.Purpose.IsValid() {
			t.Fatalf("published shape session %s has purpose %q outside the closed set", session.Name, session.Purpose)
		}
		if session.Purpose == schema.SessionPurposeHelperReview {
			if session.StartedBy == "" {
				t.Fatalf("published shape helper %s must declare the owner it was started by", session.Name)
			}
		} else {
			if session.StartedBy != "" {
				t.Fatalf("published shape owner %s must not declare a started_by edge", session.Name)
			}
			if session.Input == nil || session.Turns <= 0 {
				t.Fatalf("published shape owner %s must declare its input count and turn total", session.Name)
			}
		}
		sessions[session.Name] = session
	}
	for _, session := range sessions {
		if session.StartedBy == "" {
			continue
		}
		owner, ok := sessions[session.StartedBy]
		if !ok || owner.Purpose == schema.SessionPurposeHelperReview {
			t.Fatalf("published shape helper %s names an owner that is not a resolvable ordinary session", session.Name)
		}
	}
	required := map[string]bool{}
	for _, name := range fixture.RequiredNames {
		if name == "" || required[name] {
			t.Fatalf("duplicate/empty required published shape case %q", name)
		}
		required[name] = true
	}
	names := map[string]bool{}
	referenced := map[string]bool{}
	for _, c := range fixture.Cases {
		if c.Name == "" || names[c.Name] {
			t.Fatalf("duplicate/empty published shape case %q", c.Name)
		}
		names[c.Name] = true
		if !required[c.Name] {
			t.Fatalf("published shape case %q is not named by the required manifest", c.Name)
		}
		if len(c.Publish) == 0 {
			t.Fatalf("published shape case %s publishes nothing", c.Name)
		}
		for _, name := range c.Publish {
			if sessions[name].Name == "" {
				t.Fatalf("published shape case %s names unpublished session %s", c.Name, name)
			}
			referenced[name] = true
		}
		if len(c.Items) != c.TopLevel {
			t.Fatalf("published shape case %s declares %d top-level items but %d rows", c.Name, c.TopLevel, len(c.Items))
		}
		if c.Owner.Input == nil || c.Owner.Turns <= 0 {
			t.Fatalf("published shape case %s must pin the owner's input count and turn total", c.Name)
		}
		ordinary, helpers := 0, 0
		for _, name := range c.Publish {
			if sessions[name].Purpose == schema.SessionPurposeHelperReview {
				helpers++
			} else {
				ordinary++
			}
		}
		if ordinary != c.Ordinary || helpers != c.Helpers {
			t.Fatalf("published shape case %s declares ordinary=%d helpers=%d but publishes %d/%d", c.Name, c.Ordinary, c.Helpers, ordinary, helpers)
		}
		for _, item := range c.Items {
			if sessions[item.Session].Name == "" || sessions[item.Session].Purpose == schema.SessionPurposeHelperReview {
				t.Fatalf("published shape case %s item %s is not a published ordinary session", c.Name, item.Session)
			}
			if len(item.Members) == 0 {
				t.Fatalf("published shape case %s item %s declares no members", c.Name, item.Session)
			}
			for _, member := range item.Members {
				if sessions[member].Purpose != schema.SessionPurposeHelperReview {
					t.Fatalf("published shape case %s member %s is not a published helper", c.Name, member)
				}
				if sessions[member].StartedBy != item.Session {
					t.Fatalf("published shape case %s member %s does not attach to %s", c.Name, member, item.Session)
				}
			}
		}
	}
	for _, name := range fixture.RequiredNames {
		if !names[name] {
			t.Fatalf("required published shape case %q has no case", name)
		}
	}
	for name := range sessions {
		if !referenced[name] {
			t.Fatalf("published shape session %s is never published by a case", name)
		}
	}
	return sessions, fixture.Cases
}

// publishedShapeRead is the stable set of grouped facts a case pins. The member
// scope token is deliberately absent: it is minted per list response, so keeping
// it out of the comparison leaves the group IDENTITY and count under test.
type publishedShapeRead struct {
	Ordinary    int
	Helpers     int
	TopLevel    int
	Items       []string
	OwnerInput  *int64
	OwnerTurns  *int32
	GroupID     string
	GroupCount  int
	Members     []string
	MemberEdges []string
}

// publishedShapeContent builds the canonical durable content for one published
// session from the shared graph publication payload, overriding only the
// identity, purpose, measures and started_by edge the grouping shape defines.
func publishedShapeContent(t *testing.T, base graphPublicationFixture, session publishedShapeSession, ownerLocalID string) []byte {
	t.Helper()
	var detail map[string]json.RawMessage
	if err := json.Unmarshal([]byte(base.Detail), &detail); err != nil {
		t.Fatal(err)
	}
	detail["id"] = json.RawMessage(strconv.Quote(session.SessionID))
	detail["purpose"] = json.RawMessage(strconv.Quote(string(session.Purpose)))
	if session.Turns > 0 {
		detail["turnCount"] = json.RawMessage(strconv.Itoa(int(session.Turns)))
	}
	if session.Input != nil {
		detail["inputSubmissionCount"] = json.RawMessage(strconv.FormatInt(*session.Input, 10))
	} else {
		delete(detail, "inputSubmissionCount")
	}
	if session.StartedBy == "" {
		// An owner carries no durable parent edge; leaving the shared payload's
		// placeholder relationships in place would describe a graph the shape
		// does not have.
		delete(detail, "relationships")
		delete(detail, "rootSessionId")
	} else {
		owner := schema.SessionID(ownerLocalID)
		relationship := schema.SessionRelationship{
			Kind:          schema.SessionRelationshipStartedBy,
			TargetState:   schema.RelationshipTargetKnown,
			TargetLocalID: &owner,
			Evidence:      schema.EvidenceNativeTyped,
		}
		if err := relationship.Validate(); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal([]schema.SessionRelationship{relationship})
		if err != nil {
			t.Fatal(err)
		}
		detail["relationships"] = encoded
		detail["rootSessionId"] = json.RawMessage(strconv.Quote(ownerLocalID))
	}
	encoded, err := json.Marshal(map[string]any{"contractVersion": "0.1.0", "kind": "session_detail", "sessionDetail": detail})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// deletePublishedShapeCiphertext removes the encrypted objects the case wrote.
// It runs before cleanupOwners so no ciphertext outlives its metadata row.
func deletePublishedShapeCiphertext(t *testing.T, ctx context.Context, pool *pgxpool.Pool, blobs storage.TranscriptBlobStore, owner pgtype.UUID) {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT blob_key, wrapped_data_key, encryption_algorithm, key_version FROM transcripts WHERE owner_id = $1`, owner)
	if err != nil {
		t.Errorf("published shape ciphertext cleanup: query: %v", err)
		return
	}
	defer rows.Close()
	type descriptorRow struct {
		key       string
		wrapped   []byte
		algorithm string
		version   int32
	}
	var descriptors []descriptorRow
	for rows.Next() {
		var row descriptorRow
		if err := rows.Scan(&row.key, &row.wrapped, &row.algorithm, &row.version); err != nil {
			t.Errorf("published shape ciphertext cleanup: scan: %v", err)
			return
		}
		descriptors = append(descriptors, row)
	}
	if err := rows.Err(); err != nil {
		t.Errorf("published shape ciphertext cleanup: iterate: %v", err)
		return
	}
	for _, row := range descriptors {
		descriptor, err := descriptorFromColumns(row.key, row.wrapped, row.algorithm, row.version)
		if err != nil {
			t.Errorf("published shape ciphertext cleanup: descriptor: %v", err)
			continue
		}
		if err := blobs.Delete(ctx, descriptor); err != nil {
			t.Errorf("published shape ciphertext cleanup: delete %q: %v", row.key, err)
		}
	}
}

func TestPublishedGroupedShapeOraclesRealSQL(t *testing.T) {
	sessions, cases := loadPublishedShapeFixtures(t)
	base := loadGraphPublicationFixtures(t)
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	if err := database.RunMigrations(pool); err != nil {
		t.Fatal(err)
	}
	username := "published-grouped-shape-owner"
	owner := pullInsertUser(t, ctx, pool, 980970, username)
	defer cleanupOwners(t, ctx, pool, owner)
	blobs := &graphPublicationBlobObserver{TranscriptBlobStore: authoritativeTestBlobStore(t)}
	defer deletePublishedShapeCiphertext(t, ctx, pool, blobs, owner)

	h := New(minimalConfig(), pool, blobs)
	user := &AuthUser{ID: uuidFromPg(owner), Username: username}
	routes := chi.NewRouter()
	routes.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), UserContextKey, user)))
		})
	})
	routes.Post("/api/v1/transcripts/publish", h.PublishTranscript)
	routes.Route("/api/v1", h.RegisterTranscriptBrowseRoutes)

	publish := func(content, metadata []byte) *httptest.ResponseRecorder {
		body, boundary := multipartBody(t, map[string]string{"metadata": string(metadata)}, string(content))
		r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
		r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, r)
		return w
	}
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	readGrouped := func(nameByID map[string]string, ownerLocalID string) publishedShapeRead {
		t.Helper()
		listPath := "/api/v1/transcripts?owner=" + url.QueryEscape(username) + "&view=grouped&limit=100"
		list := get(listPath)
		if list.Code != http.StatusOK {
			t.Fatalf("grouped list status %d: %s", list.Code, list.Body)
		}
		var payload schema.VillageSessionListPayload
		if err := json.Unmarshal(list.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if err := payload.Validate(); err != nil {
			t.Fatal(err)
		}
		read := publishedShapeRead{
			Ordinary: payload.OrdinarySessionTotal,
			Helpers:  payload.HelperThreadTotal,
			TopLevel: payload.TotalItems,
		}
		for _, item := range payload.Items {
			if item.Kind != schema.SessionListItemTranscript || item.Transcript == nil || item.Context != nil {
				t.Fatalf("grouped top-level row is not an ordinary transcript row: %+v", item)
			}
			name, ok := nameByID[string(item.Transcript.Session.ID)]
			if !ok {
				t.Fatalf("grouped row names transcript %s no case published", item.Transcript.Session.ID)
			}
			read.Items = append(read.Items, name)
			if len(item.HelperGroups) != 1 {
				t.Fatalf("grouped row %s carries %d helper groups, want exactly one", name, len(item.HelperGroups))
			}
			group := item.HelperGroups[0]
			if group.Purpose != schema.SessionPurposeHelperReview {
				t.Fatalf("grouped row %s group purpose %q, want helper_review", name, group.Purpose)
			}
			if read.GroupID != "" && read.GroupID != group.GroupID {
				t.Fatalf("one owner row carried more than one helper group identity")
			}
			read.GroupID = group.GroupID
			read.GroupCount = group.HelperThreadCount
			read.OwnerInput = item.Transcript.Session.InputSubmissionCount
			read.OwnerTurns = item.Transcript.Session.TurnCount
			memberPath := "/api/v1/transcript-groups/" + group.GroupID + "/members?scope=" + url.QueryEscape(group.MemberScope)
			memberResponse := get(memberPath)
			if memberResponse.Code != http.StatusOK {
				t.Fatalf("member read status %d: %s", memberResponse.Code, memberResponse.Body)
			}
			var members schema.VillageHelperMembersPayload
			if err := json.Unmarshal(memberResponse.Body.Bytes(), &members); err != nil {
				t.Fatal(err)
			}
			if members.Total != group.HelperThreadCount {
				t.Fatalf("member total %d disagrees with the owner disclosure count %d", members.Total, group.HelperThreadCount)
			}
			for _, member := range members.Members {
				if member.Kind != schema.SessionListItemTranscript || member.Transcript == nil || member.Context != nil {
					t.Fatalf("helper member is not a transcript row: %+v", member)
				}
				memberName, ok := nameByID[string(member.Transcript.Session.ID)]
				if !ok {
					t.Fatalf("helper member names transcript %s no case published", member.Transcript.Session.ID)
				}
				// The member row must carry the durable evidence the publish path
				// stored, not just be grouped: its purpose, its root identity and
				// exactly one started_by edge naming the owner's LOCAL session id.
				session := member.Transcript.Session
				if session.Purpose != schema.SessionPurposeHelperReview {
					t.Fatalf("helper member %s purpose %q, want helper_review", memberName, session.Purpose)
				}
				if session.RootSessionID == nil || string(*session.RootSessionID) != ownerLocalID {
					t.Fatalf("helper member %s root identity %v, want %s", memberName, session.RootSessionID, ownerLocalID)
				}
				if len(session.Relationships) != 1 {
					t.Fatalf("helper member %s carries %d durable relationships, want exactly the started_by edge", memberName, len(session.Relationships))
				}
				edge := session.Relationships[0]
				if edge.Kind != schema.SessionRelationshipStartedBy || edge.TargetState != schema.RelationshipTargetKnown || edge.TargetLocalID == nil || string(*edge.TargetLocalID) != ownerLocalID {
					t.Fatalf("helper member %s durable edge %+v does not name the owner %s", memberName, edge, ownerLocalID)
				}
				read.Members = append(read.Members, memberName)
				read.MemberEdges = append(read.MemberEdges, memberName+"|started_by|"+string(edge.TargetState)+"|"+ownerLocalID)
			}
		}
		return read
	}

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			ownerSession := sessions[c.Items[0].Session]
			nameByID := map[string]string{}
			publishedLocalIDs := make([]pgtype.UUID, 0, len(c.Publish))
			localIDs := make([]string, 0, len(c.Publish))
			for _, name := range c.Publish {
				session := sessions[name]
				content := publishedShapeContent(t, base, session, ownerSession.SessionID)
				metadata := graphPublicationMetadata(t, content, base)
				beforeWrites := blobs.writes.Load()
				response := publish(content, metadata)
				if response.Code != http.StatusCreated {
					t.Fatalf("publish %s status %d: %s", name, response.Code, response.Body)
				}
				var published schema.AuthoritativePublishResponse
				if err := json.Unmarshal(response.Body.Bytes(), &published); err != nil {
					t.Fatal(err)
				}
				if blobs.writes.Load() != beforeWrites+1 {
					t.Fatalf("publish %s did not write exactly one ciphertext object", name)
				}
				parsed, err := uuid.Parse(string(published.TranscriptID))
				if err != nil {
					t.Fatalf("parse published transcript id %q: %v", published.TranscriptID, err)
				}
				nameByID[string(published.TranscriptID)] = name
				publishedLocalIDs = append(publishedLocalIDs, toPgUUID(parsed))
				localIDs = append(localIDs, session.SessionID)
			}

			// The first read is the projection the publish path produced.
			first := readGrouped(nameByID, ownerSession.SessionID)
			if first.Ordinary != c.Ordinary || first.Helpers != c.Helpers || first.TopLevel != c.TopLevel {
				t.Fatalf("grouped counts ordinary=%d helpers=%d top-level=%d want %d/%d/%d", first.Ordinary, first.Helpers, first.TopLevel, c.Ordinary, c.Helpers, c.TopLevel)
			}
			// Identities are compared as sets: every published session shares one
			// session_start, so the projection's tie-break is the random
			// transcript UUID and the rendered order is not a property the shape
			// pins. The COUNT is pinned exactly on both sides.
			wantItems := make([]string, 0, len(c.Items))
			for _, item := range c.Items {
				wantItems = append(wantItems, item.Session)
			}
			sort.Strings(wantItems)
			gotItems := append([]string(nil), first.Items...)
			sort.Strings(gotItems)
			if !reflect.DeepEqual(gotItems, wantItems) {
				t.Fatalf("grouped top-level identities %v want %v", first.Items, wantItems)
			}
			wantMembers := append([]string(nil), c.Items[0].Members...)
			sort.Strings(wantMembers)
			gotMembers := append([]string(nil), first.Members...)
			sort.Strings(gotMembers)
			if first.GroupCount != len(wantMembers) || !reflect.DeepEqual(gotMembers, wantMembers) {
				t.Fatalf("helper group count=%d members=%v want %d/%v", first.GroupCount, first.Members, len(wantMembers), wantMembers)
			}
			if first.OwnerInput == nil || *first.OwnerInput != *c.Owner.Input || first.OwnerTurns == nil || *first.OwnerTurns != c.Owner.Turns {
				t.Fatalf("grouped owner facts input=%s turns=%s want %d/%d", optionalInt64Text(first.OwnerInput), optionalInt32Text(first.OwnerTurns), *c.Owner.Input, c.Owner.Turns)
			}
			if first.GroupID == "" {
				t.Fatal("published shape produced no helper group identity to replay")
			}

			// Republish every session byte-for-byte: the accepted-operation
			// fingerprint must take the unchanged skip, so no ciphertext, row,
			// grouped fact, or audit event may move.
			writesAfterPublish := blobs.writes.Load()
			for _, name := range c.Publish {
				session := sessions[name]
				content := publishedShapeContent(t, base, session, ownerSession.SessionID)
				metadata := graphPublicationMetadata(t, content, base)
				retry := publish(content, metadata)
				if retry.Code != http.StatusOK {
					t.Fatalf("unchanged republish %s status %d: %s", name, retry.Code, retry.Body)
				}
				var published schema.AuthoritativePublishResponse
				if err := json.Unmarshal(retry.Body.Bytes(), &published); err != nil {
					t.Fatal(err)
				}
				if nameByID[string(published.TranscriptID)] != name {
					t.Fatalf("unchanged republish %s changed the source-keyed transcript identity", name)
				}
			}
			if blobs.writes.Load() != writesAfterPublish {
				t.Fatal("unchanged republish rewrote ciphertext instead of skipping")
			}
			second := readGrouped(nameByID, ownerSession.SessionID)
			if !reflect.DeepEqual(first, second) {
				t.Fatalf("republish changed the grouped projection: before=%+v after=%+v", first, second)
			}

			// One row per owner/local identity, and one append-only 'published'
			// event per identity with nothing else appended by the republish.
			var identities int
			if err := pool.QueryRow(ctx, `SELECT count(DISTINCT local_id) FROM transcripts WHERE owner_id = $1 AND local_id = ANY($2)`, owner, localIDs).Scan(&identities); err != nil {
				t.Fatal(err)
			}
			if identities != len(c.Publish) {
				t.Fatalf("owner/local identity count=%d want %d", identities, len(c.Publish))
			}
			for _, localID := range localIDs {
				var rows int
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM transcripts WHERE owner_id = $1 AND local_id = $2`, owner, localID).Scan(&rows); err != nil {
					t.Fatal(err)
				}
				if rows != 1 {
					t.Fatalf("session %s has %d rows for one owner/local identity, want exactly one", localID, rows)
				}
			}
			var published, other int
			if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE event_type = 'published'), count(*) FILTER (WHERE event_type <> 'published') FROM transcript_governance_events_audit WHERE transcript_id = ANY($1)`, publishedLocalIDs).Scan(&published, &other); err != nil {
				t.Fatal(err)
			}
			if published != len(c.Publish) || other != 0 {
				t.Fatalf("append-only audit has %d published and %d other events, want %d published and none other", published, other, len(c.Publish))
			}

			t.Logf("published shape %s: ordinary=%d helpers=%d top-level=%d group=%s members=%v owner input=%d turns=%d",
				c.Name, first.Ordinary, first.Helpers, first.TopLevel, first.GroupID, first.Members, *first.OwnerInput, *first.OwnerTurns)
		})
	}
}

func optionalInt64Text(value *int64) string {
	if value == nil {
		return "unmeasured"
	}
	return strconv.FormatInt(*value, 10)
}

func optionalInt32Text(value *int32) string {
	if value == nil {
		return "unmeasured"
	}
	return strconv.Itoa(int(*value))
}
