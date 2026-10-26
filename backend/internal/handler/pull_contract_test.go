package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"

	"github.com/peasant-labs/schema"
)

//go:embed testdata/pull-contract-negotiation.yaml
var pullContractNegotiationYAML []byte

//go:embed testdata/pull-envelope-people.yaml
var pullEnvelopePeopleYAML []byte

type pullNegotiationCase struct {
	Name   string   `yaml:"name"`
	Header []string `yaml:"header"`
	Form   string   `yaml:"form"`
	Want   string   `yaml:"want"`
}

type pullPeopleCase struct {
	Name          string   `yaml:"name"`
	Header        []string `yaml:"header"`
	OwnerVisible  bool     `yaml:"owner_visible"`
	Author        string   `yaml:"author"`
	AuthorVisible *bool    `yaml:"author_visible"`
	Envelope      string   `yaml:"envelope"`
}

var requiredPullNegotiationCases = []string{
	"no_header", "repeated_header", "malformed_empty", "malformed_two_parts", "malformed_v_prefix",
	"malformed_text", "legacy_exact", "legacy_patch", "public_owner_exact", "public_owner_patch",
	"newer_minor", "below_minimum_minor", "below_minimum_zero", "malformed_below_minimum", "newer_major",
	"malformed_list", "malformed_prerelease", "malformed_four_parts", "public_owner_leading_zeros",
	"newer_beyond_integer_range", "below_minimum_long_value",
}

var requiredPullPeopleCases = []string{
	"legacy_without_header_hides_owner", "legacy_requested_hides_owner", "legacy_malformed_header_hides_owner",
	"public_owner_hides_owner", "public_owner_patch_hides_owner", "legacy_shows_visible_owner",
	"public_owner_shows_visible_owner", "legacy_visible_author_on_hidden_owner",
	"public_owner_visible_author_on_hidden_owner", "legacy_hidden_author_on_visible_owner",
	"public_owner_hidden_author_on_visible_owner",
}

func requireFixtureNames(t *testing.T, fixture string, present map[string]bool, required []string) {
	t.Helper()
	for _, name := range required {
		if !present[name] {
			t.Fatalf("%s no longer contains %q; restore it", fixture, name)
		}
	}
}

// servedPullContractPattern is the pattern the served contract declares for
// the Peasant-Pull-Contract response header: the shape of a well-formed
// version.
func servedPullContractPattern(t *testing.T) *regexp.Regexp {
	t.Helper()
	var spec struct {
		Paths map[string]struct {
			Get struct {
				Responses map[string]struct {
					Headers map[string]struct {
						Schema struct {
							Pattern string `json:"pattern"`
						} `json:"schema"`
					} `json:"headers"`
				} `json:"responses"`
			} `json:"get"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(schema.VillageAPISpecJSON(), &spec); err != nil {
		t.Fatalf("decode the served contract: %v", err)
	}
	pattern := spec.Paths["/api/v1/pull/transcripts"].Get.Responses["200"].Headers[schema.PullContractHeader].Schema.Pattern
	if pattern == "" {
		t.Fatalf("the served contract declares no pattern for the %s response header", schema.PullContractHeader)
	}
	return regexp.MustCompile(pattern)
}

// pullRoute is one production pull read.
type pullRoute struct {
	name    string
	target  func(tid uuid.UUID) string
	handler func(h *Handler) http.HandlerFunc
	id      bool
}

func pullRoutes() []pullRoute {
	return []pullRoute{
		{"list", func(uuid.UUID) string { return "/api/v1/pull/transcripts" }, func(h *Handler) http.HandlerFunc { return h.ListPullableTranscripts }, false},
		{"metadata", func(tid uuid.UUID) string { return "/api/v1/pull/transcripts/" + tid.String() }, func(h *Handler) http.HandlerFunc { return h.GetPullTranscript }, true},
		{"annotations", func(tid uuid.UUID) string { return "/api/v1/pull/transcripts/" + tid.String() + "/annotations" }, func(h *Handler) http.HandlerFunc { return h.GetPullTranscriptAnnotations }, true},
		{"content", func(tid uuid.UUID) string { return "/api/v1/pull/transcripts/" + tid.String() + "/content" }, func(h *Handler) http.HandlerFunc { return h.GetPullTranscriptContent }, true},
	}
}

func servePull(h *Handler, route pullRoute, caller, tid uuid.UUID, header []string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, route.target(tid), nil)
	r = r.WithContext(withUserID(r.Context(), caller))
	for _, value := range header {
		r.Header.Add(schema.PullContractHeader, value)
	}
	if route.id {
		r = withChiURLParam(r, "id", tid.String())
	}
	w := httptest.NewRecorder()
	route.handler(h)(w, r)
	return w
}

// pullPersonKeys names the JSON keys that carry a person in each envelope.
func pullPersonKeys(route string) (legacyID, current string) {
	if route == "annotations" {
		return "authorUserId", "author"
	}
	return "ownerUserId", "owner"
}

// pullEnvelopeRows decodes the rows of a list, metadata or annotations body
// as raw objects: one row for metadata, every row otherwise.
func pullEnvelopeRows(t *testing.T, route string, body []byte) []map[string]json.RawMessage {
	t.Helper()
	switch route {
	case "list":
		var list struct {
			Transcripts []map[string]json.RawMessage `json:"transcripts"`
		}
		decodePullJSON(t, body, &list)
		return list.Transcripts
	case "metadata":
		var row map[string]json.RawMessage
		decodePullJSON(t, body, &row)
		return []map[string]json.RawMessage{row}
	case "annotations":
		var rows []map[string]json.RawMessage
		decodePullJSON(t, body, &rows)
		return rows
	}
	t.Fatalf("route %q has no JSON envelope", route)
	return nil
}

func TestPullContractNegotiation(t *testing.T) {
	cases, err := decodeFixtureRows[pullNegotiationCase](pullContractNegotiationYAML)
	if err != nil {
		t.Fatalf("load the pull negotiation corpus: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		present[c.Name] = true
	}
	requireFixtureNames(t, "the pull negotiation corpus", present, requiredPullNegotiationCases)
	pattern := servedPullContractPattern(t)

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			switch c.Form {
			case "absent":
				if len(c.Header) != 0 {
					t.Fatalf("an absent header sends %v", c.Header)
				}
			case "repeated":
				if len(c.Header) < 2 {
					t.Fatalf("a repeated header sends %v", c.Header)
				}
			case "malformed", "well_formed":
				if len(c.Header) != 1 {
					t.Fatalf("a %s header sends %v; send one value", c.Form, c.Header)
				}
				wantWellFormed := c.Form == "well_formed"
				if got := pattern.MatchString(c.Header[0]); got != wantWellFormed {
					t.Fatalf("the served contract's pattern %s says %q well-formed = %v; the row says %s", pattern, c.Header[0], got, c.Form)
				}
				if _, got := parsePullContract(c.Header[0]); got != wantWellFormed {
					t.Fatalf("parsePullContract(%q) well-formed = %v, but the served contract says %v", c.Header[0], got, wantWellFormed)
				}
			default:
				t.Fatalf("form %q; the forms are absent, repeated, malformed and well_formed", c.Form)
			}

			for _, route := range pullRoutes() {
				t.Run(route.name, func(t *testing.T) {
					owner, tid := uuid.New(), uuid.New()
					read := false
					q := &mockQuerier{
						getTranscriptByID: func(context.Context, pgtype.UUID) (sqlc.Transcript, error) {
							read = true
							return pullTestTranscript(tid, owner, dbVisibilityPrivate), nil
						},
						listPullableTranscripts: func(context.Context, sqlc.ListPullableTranscriptsParams) ([]sqlc.ListPullableTranscriptsRow, error) {
							read = true
							return []sqlc.ListPullableTranscriptsRow{{ID: pgUUIDFrom(tid), OwnerID: pgUUIDFrom(owner), Visibility: dbVisibilityPrivate}}, nil
						},
						countPullableTranscripts: func(context.Context, pgtype.UUID) (int64, error) { return 1, nil },
						listAnnotationsByTranscriptID: func(context.Context, pgtype.UUID) ([]sqlc.Annotation, error) {
							return []sqlc.Annotation{{ID: pgUUIDFrom(uuid.New()), OwnerID: pgUUIDFrom(owner), TargetKind: string(schema.TargetSession), TypeID: "quality.session_outcome", Value: "success"}}, nil
						},
						getUserByID: func(_ context.Context, id pgtype.UUID) (sqlc.User, error) {
							return sqlc.User{ID: id, GithubUsername: "negotiating-owner", IsDiscoverable: true}, nil
						},
					}
					h := newTestHandler(q, &fixedTranscriptBlobStore{body: string(contentWithRepositoryJSON())})
					w := servePull(h, route, owner, tid, c.Header)
					if vary := w.Header().Values("Vary"); len(vary) != 1 || vary[0] != schema.PullContractHeader {
						t.Errorf("Vary = %v, want exactly %s on every pull response", vary, schema.PullContractHeader)
					}

					if c.Want == "refused" {
						if w.Code != http.StatusNotAcceptable {
							t.Fatalf("status %d, want 406: %s", w.Code, w.Body.String())
						}
						var refusal schema.VillageErrorResponse
						decoder := json.NewDecoder(bytes.NewReader(w.Body.Bytes()))
						decoder.DisallowUnknownFields()
						if err := decoder.Decode(&refusal); err != nil || refusal.Error == "" {
							t.Fatalf("406 body is not a VillageErrorResponse: %s", w.Body.String())
						}
						echoed := c.Header[0]
						if len(echoed) > 64 {
							echoed = echoed[:64]
							if strings.Contains(refusal.Error, c.Header[0]) {
								t.Errorf("406 repeats the whole %d-byte requested value: %s", len(c.Header[0]), refusal.Error)
							}
						}
						if !strings.Contains(refusal.Error, echoed) {
							t.Errorf("406 does not name the requested version %q: %s", echoed, refusal.Error)
						}
						if got := w.Header().Get(schema.PullContractHeader); got != "" {
							t.Errorf("a refused read names served envelope %q", got)
						}
						if read {
							t.Errorf("a refused read still read the transcript")
						}
						return
					}

					if w.Code != http.StatusOK {
						t.Fatalf("status %d, want 200: %s", w.Code, w.Body.String())
					}
					if got := w.Header().Get(schema.PullContractHeader); got != c.Want {
						t.Fatalf("%s response header = %q, want %q", schema.PullContractHeader, got, c.Want)
					}
					if route.name == "content" {
						return
					}
					legacyKey, currentKey := pullPersonKeys(route.name)
					rows := pullEnvelopeRows(t, route.name, w.Body.Bytes())
					if len(rows) == 0 {
						t.Fatalf("no rows in %s", w.Body.String())
					}
					for _, row := range rows {
						_, legacy := row[legacyKey]
						_, current := row[currentKey]
						if served := map[bool]string{true: "0.2.0", false: "0.3.0"}[legacy]; legacy == current || served != c.Want {
							t.Errorf("row carries %s=%v and %s=%v; want only the %s envelope: %s", legacyKey, legacy, currentKey, current, c.Want, w.Body.String())
						}
					}
				})
			}
		})
	}
}

// pullPerson is one person a pull response may show.
type pullPerson struct {
	id          uuid.UUID
	username    string
	displayName string
	avatarURL   string
	visible     bool
}

func (p pullPerson) identity() map[string]string {
	return map[string]string{"id": p.id.String(), "username": p.username, "display name": p.displayName, "avatar": p.avatarURL}
}

func TestPullEnvelopesNeverLeakAHiddenOwner(t *testing.T) {
	cases, err := decodeFixtureRows[pullPeopleCase](pullEnvelopePeopleYAML)
	if err != nil {
		t.Fatalf("load the pull envelope people corpus: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		present[c.Name] = true
	}
	requireFixtureNames(t, "the pull envelope people corpus", present, requiredPullPeopleCases)

	const (
		projectName = "leaky-project"
		projectHash = "d1e2a3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
	)

	for _, c := range cases {
		owner := pullPerson{uuid.New(), "leaky-person", "Leaky Person Display", "https://avatars.example/leaky-person.png", c.OwnerVisible}
		author := owner
		switch c.Author {
		case "owner":
			if c.AuthorVisible != nil {
				t.Fatalf("case %q: author_visible applies only to author: other", c.Name)
			}
		case "other":
			if c.AuthorVisible == nil {
				t.Fatalf("case %q: author: other needs author_visible", c.Name)
			}
			author = pullPerson{uuid.New(), "leaky-author", "Leaky Author Display", "https://avatars.example/leaky-author.png", *c.AuthorVisible}
		default:
			t.Fatalf("case %q: author %q; the authors are owner and other", c.Name, c.Author)
		}
		people := map[uuid.UUID]pullPerson{owner.id: owner, author.id: author}

		for _, route := range pullRoutes() {
			t.Run(c.Name+"/"+route.name, func(t *testing.T) {
				member, tid, group := uuid.New(), uuid.New(), uuid.New()
				transcript := pullTestTranscript(tid, owner.id, dbVisibilityShared)
				transcript.ProjectName = pgText(projectName)
				transcript.BlobSizeBytes = pgtype.Int8{}
				q := &mockQuerier{
					getTranscriptByID: func(context.Context, pgtype.UUID) (sqlc.Transcript, error) { return transcript, nil },
					listPullableTranscripts: func(context.Context, sqlc.ListPullableTranscriptsParams) ([]sqlc.ListPullableTranscriptsRow, error) {
						return []sqlc.ListPullableTranscriptsRow{{ID: pgUUIDFrom(tid), OwnerID: pgUUIDFrom(owner.id), ProjectName: pgText(projectName), Visibility: dbVisibilityShared}}, nil
					},
					countPullableTranscripts: func(context.Context, pgtype.UUID) (int64, error) { return 1, nil },
					listApprovedTranscriptShareGroups: func(context.Context, pgtype.UUID) ([]pgtype.UUID, error) {
						return []pgtype.UUID{pgUUIDFrom(group)}, nil
					},
					getGroupMember: func(_ context.Context, arg sqlc.GetGroupMemberParams) (sqlc.GroupMember, error) {
						return sqlc.GroupMember{GroupID: arg.GroupID, UserID: arg.UserID, Role: "member"}, nil
					},
					listAnnotationsByTranscriptID: func(context.Context, pgtype.UUID) ([]sqlc.Annotation, error) {
						return []sqlc.Annotation{{
							ID: pgUUIDFrom(uuid.New()), OwnerID: pgUUIDFrom(author.id), TargetKind: string(schema.TargetProject),
							ProjectHash: pgText(projectHash), TypeID: "quality.session_outcome", Value: "success",
							AnnotatorKind: pgText(string(schema.AnnotatorHuman)), AnnotatorName: pgText(author.username),
						}}, nil
					},
					listPublicOwnersForViewer: func(_ context.Context, arg sqlc.ListPublicOwnersForViewerParams) ([]sqlc.ListPublicOwnersForViewerRow, error) {
						rows := make([]sqlc.ListPublicOwnersForViewerRow, 0, len(arg.PersonIds))
						for _, id := range arg.PersonIds {
							person, ok := people[uuid.UUID(id.Bytes)]
							if !ok {
								t.Fatalf("the owner read was asked about an unknown person %s", uuid.UUID(id.Bytes))
							}
							rows = append(rows, sqlc.ListPublicOwnersForViewerRow{ID: id, GithubUsername: person.username, DisplayName: pgText(person.displayName), AvatarUrl: pgText(person.avatarURL), ProfileVisible: person.visible})
						}
						return rows, nil
					},
				}
				h := newTestHandler(q, &fixedTranscriptBlobStore{body: string(contentWithRepositoryJSON())})
				w := servePull(h, route, member, tid, c.Header)
				if w.Code != http.StatusOK {
					t.Fatalf("status %d: %s", w.Code, w.Body.String())
				}
				if got := w.Header().Get(schema.PullContractHeader); got != c.Envelope {
					t.Fatalf("%s response header = %q, want %q", schema.PullContractHeader, got, c.Envelope)
				}
				body := w.Body.Bytes()

				withheld := map[string]string{}
				for _, person := range []pullPerson{owner, author} {
					if person.visible {
						continue
					}
					for label, value := range person.identity() {
						withheld[person.username+" "+label] = value
					}
				}
				if !owner.visible {
					withheld["project name"] = projectName
					withheld["project hash"] = projectHash
					for i, value := range withheldContentValues {
						withheld["content repository "+string(rune('a'+i))] = value
					}
				}
				for label, value := range withheld {
					if bytes.Contains(body, []byte(value)) {
						t.Errorf("the %s envelope leaks the hidden %s %q: %s", c.Envelope, label, value, body)
					}
				}
				if route.name == "content" {
					return
				}

				// The annotation shows its author; the repository fields
				// beside it follow the transcript's owner.
				person := owner
				if route.name == "annotations" {
					person = author
				}
				legacyKey, currentKey := pullPersonKeys(route.name)
				for _, row := range pullEnvelopeRows(t, route.name, body) {
					if c.Envelope == "0.2.0" {
						assertLegacyPullPerson(t, route.name, row, legacyKey, person)
						if _, ok := row[currentKey]; ok {
							t.Errorf("the 0.2.0 %s row carries %q: %s", route.name, currentKey, body)
						}
					} else {
						assertCurrentPullPerson(t, route.name, row[currentKey], person)
						if _, ok := row[legacyKey]; ok {
							t.Errorf("the 0.3.0 %s row carries %q: %s", route.name, legacyKey, body)
						}
					}
					if route.name == "annotations" {
						_, hasHash := row["targetProjectHash"]
						if hasHash != owner.visible {
							t.Errorf("annotation targetProjectHash present = %v beside owner_visible %v: %s", hasHash, owner.visible, body)
						}
						var annotator string
						decodePullJSON(t, row["annotatorName"], &annotator)
						wantAnnotator := anonymousAnnotatorName
						if author.visible {
							wantAnnotator = author.username
						}
						if annotator != wantAnnotator {
							t.Errorf("annotatorName = %q, want %q for a visible author = %v", annotator, wantAnnotator, author.visible)
						}
						continue
					}
					var name *string
					if raw, ok := row["projectName"]; ok {
						decodePullJSON(t, raw, &name)
					}
					if owner.visible != (name != nil && *name == projectName) {
						t.Errorf("projectName = %v beside owner_visible %v: %s", name, owner.visible, body)
					}
				}
				validatePullBody(t, c.Envelope, route.name, body, owner.visible)
			})
		}
	}
}

// TestPullAnnotationsWithholdTheTargetFilePath covers the file-version target,
// which no stored annotation row carries today, so the handler tests cannot
// reach it: a hidden owner's file path is withheld before either envelope is
// rendered, and neither envelope's bytes then name it.
func TestPullAnnotationsWithholdTheTargetFilePath(t *testing.T) {
	path, hash := "src/leaky-person/secret.go", "c0ffee"
	summary := schema.AnnotationSummary{ID: "a", TargetKind: schema.TargetFileVersion, TargetFilePath: &path, TargetContentHash: &hash, TypeID: "t", TypeName: "t", Value: "v", AnnotatorKind: schema.AnnotatorHuman}
	withholdAnnotationRepository(&summary, schema.AnonymousVillageOwner())
	current := schema.PullAnnotation{AnnotationSummary: summary, Author: schema.AnonymousVillageOwner()}
	for _, rendered := range []any{current, schema.LegacyPullAnnotationFrom(current)} {
		body, err := json.Marshal(rendered)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(body, []byte(path)) || bytes.Contains(body, []byte("targetFilePath")) {
			t.Errorf("a hidden owner's file path is served: %s", body)
		}
	}
}

// assertLegacyPullPerson checks a 0.2.0 person: the real id and username for
// a visible person, an empty id beside "anon" for a hidden one.
func assertLegacyPullPerson(t *testing.T, route string, row map[string]json.RawMessage, idKey string, person pullPerson) {
	t.Helper()
	usernameKey := strings.Replace(idKey, "UserId", "Username", 1)
	var id, name string
	raw, ok := row[idKey]
	if !ok {
		t.Fatalf("the 0.2.0 %s row has no %s", route, idKey)
	}
	decodePullJSON(t, raw, &id)
	decodePullJSON(t, row[usernameKey], &name)
	wantID, wantName := "", schema.LegacyPullAnonymousUsername
	if person.visible {
		wantID, wantName = person.id.String(), person.username
	}
	if id != wantID || name != wantName {
		t.Errorf("the 0.2.0 %s person = (%q, %q), want (%q, %q)", route, id, name, wantID, wantName)
	}
}

// assertCurrentPullPerson checks a 0.3.0 person: the exact profile, or
// exactly {"kind":"anonymous"}.
func assertCurrentPullPerson(t *testing.T, route string, raw json.RawMessage, person pullPerson) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("the 0.3.0 %s person is not an object: %s", route, raw)
	}
	want := map[string]any{"kind": "anonymous"}
	if person.visible {
		want = map[string]any{"kind": "profile", "id": person.id.String(), "username": person.username, "display_name": person.displayName, "avatar_url": person.avatarURL}
	}
	if len(got) != len(want) {
		t.Errorf("the 0.3.0 %s person = %v, want %v", route, got, want)
		return
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("the 0.3.0 %s person = %v, want %v", route, got, want)
			return
		}
	}
}

// validatePullBody runs the schema module's own validation over the served
// body, so a response the contract refuses fails here too.
//
// An annotation's target is validated only beside a visible owner: the
// project arm of AnnotationSummary.Validate requires targetProjectHash, which
// Village withholds beside a hidden owner, so the module refuses that row in
// both envelopes. The person on it is validated either way.
func validatePullBody(t *testing.T, envelope, route string, body []byte, ownerVisible bool) {
	t.Helper()
	var errs []error
	switch {
	case envelope == "0.2.0" && route == "list":
		var v schema.LegacyPullListResponse
		decodePullJSON(t, body, &v)
		errs = append(errs, v.Validate())
	case envelope == "0.2.0" && route == "metadata":
		var v schema.LegacyPullTranscriptInfo
		decodePullJSON(t, body, &v)
		errs = append(errs, v.Validate())
	case envelope == "0.2.0" && route == "annotations":
		var rows []schema.LegacyPullAnnotation
		decodePullJSON(t, body, &rows)
		for _, row := range rows {
			errs = append(errs, row.Validate())
			if ownerVisible {
				errs = append(errs, row.AnnotationSummary.Validate())
			}
		}
	case route == "list":
		var v schema.PullListResponse
		decodePullJSON(t, body, &v)
		errs = append(errs, v.Validate())
	case route == "metadata":
		var v schema.PullTranscriptInfo
		decodePullJSON(t, body, &v)
		errs = append(errs, v.Validate())
	case route == "annotations":
		var rows []schema.PullAnnotation
		decodePullJSON(t, body, &rows)
		for _, row := range rows {
			errs = append(errs, row.Author.Validate())
			if ownerVisible {
				errs = append(errs, row.AnnotationSummary.Validate())
			}
		}
	}
	for _, err := range errs {
		if err != nil {
			t.Errorf("the schema module refuses the %s %s body: %v", envelope, route, err)
		}
	}
}

func decodePullJSON(t *testing.T, body []byte, into any) {
	t.Helper()
	if err := json.Unmarshal(body, into); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
}
