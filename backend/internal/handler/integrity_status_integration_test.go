//go:build integration

package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/integrity-served-status.yaml
var integrityServedStatusYAML []byte

type integrityServedResult struct {
	Hash    string `yaml:"hash"`
	Version string `yaml:"version"`
	Verdict string `yaml:"verdict"`
	Turns   []int  `yaml:"turns"`
}

type integrityServedCase struct {
	Name           string                  `yaml:"name"`
	Why            string                  `yaml:"why"`
	Visibility     string                  `yaml:"visibility"`
	Share          string                  `yaml:"share"`
	Results        []integrityServedResult `yaml:"results"`
	Inherited      bool                    `yaml:"inherited"`
	OwnerAnonymous bool                    `yaml:"owner_anonymous"`
	Viewer         string                  `yaml:"viewer"`
	Want           string                  `yaml:"want"`
	WantTurns      []int                   `yaml:"want_turns"`
}

var requiredIntegrityServedCases = []string{
	"public_flagged_has_turns",
	"public_flagged_signed_out_identical",
	"public_clear_absent",
	"public_unchecked_absent",
	"public_no_result_absent",
	"private_shared_flagged_present_for_owner",
	"shared_flagged_present_for_member",
	"pending_reviewer_sees_flag",
	"rejected_reviewer_sees_flag",
	"owner_sees_flag_after_withdrawal",
	"stranger_404_reveals_nothing",
	"sticky_flag_on_clean_copy_has_no_turns",
	"newest_version_turns_win",
	"later_unchecked_keeps_turns",
	"voided_flag_absent",
	"inherited_flag_has_no_turns",
	"integrity_has_only_flagged_turns",
	"no_scry_version_in_body",
	"scry_unset_still_serves_stored_flag",
	"list_and_pull_never_carry_integrity",
	"anonymous_owner_public_flagged_validates",
}

func loadIntegrityServedCases(t *testing.T) []integrityServedCase {
	t.Helper()
	cases, err := decodeFixtureRows[integrityServedCase](integrityServedStatusYAML)
	if err != nil {
		t.Fatalf("load the served integrity corpus: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		if c.Name == "" || c.Why == "" || present[c.Name] {
			t.Fatalf("served integrity case %+v must carry a unique name and a reason", c)
		}
		present[c.Name] = true
	}
	for _, name := range requiredIntegrityServedCases {
		if !present[name] {
			t.Fatalf("the served integrity corpus no longer contains %q; restore it", name)
		}
	}
	return cases
}

const servedOtherHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func TestServedIntegrityStatus(t *testing.T) {
	pool := govTestPool(t)
	defer pool.Close()
	titles, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatalf("construct title pipeline: %v", err)
	}
	objects := publishEvidenceObjects(t)
	blobs := publishEvidenceStore(t, objects)
	ctx := context.Background()
	queries := sqlc.New(pool)
	h := &Handler{cfg: minimalConfig(), pool: pool, queries: queries, blobs: blobs, titles: titles}

	detail := func(id pgtype.UUID, viewer *uuid.UUID) *httptest.ResponseRecorder {
		sid := uuidFromPg(id).String()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+sid, nil)
		r = withChiURLParam(r, "id", sid)
		if viewer != nil {
			r = r.WithContext(withUserID(r.Context(), *viewer))
		}
		w := httptest.NewRecorder()
		h.GetTranscript(w, r)
		return w
	}

	// The 404 a stranger gets for an unflagged private transcript.
	var plainNotFound []byte
	{
		owner := pullInsertUser(t, ctx, pool, 996001, "served-plain-owner-"+uuid.NewString()[:8])
		stranger := pullInsertUser(t, ctx, pool, 996002, "served-plain-stranger-"+uuid.NewString()[:8])
		defer cleanupOwners(t, context.Background(), pool, owner, stranger)
		content := []byte(`[{"role":"user","content":"served plain"}]`)
		w := publishEvidencePost(t, h, &AuthUser{ID: uuidFromPg(owner), Username: "served"}, publishEvidenceRequest(uuid.NewString(), content, ""), content)
		if w.Code >= 300 {
			t.Fatalf("publish the plain transcript: %d %s", w.Code, w.Body.String())
		}
		var id pgtype.UUID
		if err := pool.QueryRow(ctx, `SELECT id FROM transcripts WHERE owner_id = $1`, owner).Scan(&id); err != nil {
			t.Fatalf("find the plain transcript: %v", err)
		}
		sv := uuidFromPg(stranger)
		got := detail(id, &sv)
		if got.Code != http.StatusNotFound {
			t.Fatalf("plain private transcript for a stranger: status %d, want 404", got.Code)
		}
		plainNotFound = got.Body.Bytes()
	}

	for ordinal, c := range loadIntegrityServedCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			owner := pullInsertUser(t, ctx, pool, int64(996100+ordinal*10), "served-owner-"+uuid.NewString()[:8])
			other := pullInsertUser(t, ctx, pool, int64(996101+ordinal*10), "served-other-"+uuid.NewString()[:8])
			member := pullInsertUser(t, ctx, pool, int64(996102+ordinal*10), "served-member-"+uuid.NewString()[:8])
			collOwner := pullInsertUser(t, ctx, pool, int64(996103+ordinal*10), "served-collowner-"+uuid.NewString()[:8])
			defer cleanupOwners(t, context.Background(), pool, owner, other, member, collOwner)

			group := pullInsertGroup(t, ctx, pool, collOwner, "served-"+uuid.NewString()[:8])
			pullAddMember(t, ctx, pool, group, collOwner, "owner")
			pullAddMember(t, ctx, pool, group, member, "member")

			content := []byte(`[{"role":"user","content":"served ` + c.Name + `"}]`)
			w := publishEvidencePost(t, h, &AuthUser{ID: uuidFromPg(owner), Username: "served"}, publishEvidenceRequest(uuid.NewString(), content, ""), content)
			if w.Code >= 300 {
				t.Fatalf("publish: %d %s", w.Code, w.Body.String())
			}
			var id pgtype.UUID
			var currentHash string
			if err := pool.QueryRow(ctx, `SELECT id, content_hash FROM transcripts WHERE owner_id = $1`, owner).Scan(&id, &currentHash); err != nil {
				t.Fatalf("find the transcript: %v", err)
			}
			if c.Share != "none" {
				pullShare(t, ctx, pool, id, group, c.Share)
			}
			if c.Visibility != "private" {
				execAsSystem(t, ctx, pool, "UPDATE transcripts SET visibility = $2 WHERE id = $1", id, c.Visibility)
			}
			for _, res := range c.Results {
				hash := currentHash
				if res.Hash == "other" {
					hash = servedOtherHash
				}
				verdict := schema.ScryVerdictChecked
				if res.Verdict != "" {
					verdict = schema.ScryVerdict(res.Verdict)
				}
				turns := res.Turns
				if turns == nil {
					turns = []int{}
				}
				if err := recordIntegrityResult(ctx, queries, id, hash, schema.ScryCheckResponse{ScryVersion: res.Version, Verdict: verdict, FlaggedTurns: turns}); err != nil {
					t.Fatalf("record result: %v", err)
				}
			}
			if c.Inherited {
				if _, err := pool.Exec(ctx, `INSERT INTO transcript_integrity_inherited_flags (transcript_id, flagged_at) VALUES ($1, now())`, id); err != nil {
					t.Fatalf("seed the inherited flag: %v", err)
				}
			}

			var viewer *uuid.UUID
			for name, who := range map[string]pgtype.UUID{"owner": owner, "other_user": other, "collective_member": member, "collective_owner": collOwner} {
				if c.Viewer == name {
					v := uuidFromPg(who)
					viewer = &v
				}
			}
			if viewer == nil && c.Viewer != "signed_out" {
				t.Fatalf("unknown viewer %q", c.Viewer)
			}
			if c.OwnerAnonymous {
				execAsSystem(t, ctx, pool, "UPDATE users SET is_discoverable = FALSE WHERE id = $1", owner)
			}
			got := detail(id, viewer)

			if c.Want == "not_found" {
				if got.Code != http.StatusNotFound {
					t.Fatalf("status %d, want 404: %s", got.Code, got.Body.String())
				}
				if !bytes.Equal(got.Body.Bytes(), plainNotFound) {
					t.Fatalf("404 body %q differs from the unflagged private 404 %q", got.Body.String(), plainNotFound)
				}
				return
			}
			if got.Code != http.StatusOK {
				t.Fatalf("status %d: %s", got.Code, got.Body.String())
			}
			var resp schema.VillageTranscriptMetadataResponse
			if err := json.Unmarshal(got.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode the detail response: %v\n%s", err, got.Body.String())
			}
			if err := resp.Validate(); err != nil {
				t.Fatalf("the detail response is not a valid metadata response: %v", err)
			}
			if c.OwnerAnonymous && resp.Owner.IsProfile() {
				t.Fatalf("the owner is shown to the viewer, the case needs an anonymous owner: %+v", resp.Owner)
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(got.Body.Bytes(), &raw); err != nil {
				t.Fatalf("decode the raw body: %v", err)
			}
			for _, leak := range []string{"7.31.4", "7.32.0", "7.100.0", "9.0.0", "scry_version", "verdict"} {
				if strings.Contains(got.Body.String(), leak) {
					t.Fatalf("the detail body leaks %q", leak)
				}
			}

			switch c.Want {
			case "absent":
				if _, ok := raw["integrity"]; ok || resp.Integrity != nil {
					t.Fatalf("integrity is served, want none: %s", raw["integrity"])
				}
			case "served":
				if resp.Integrity == nil {
					t.Fatalf("integrity is absent, want flagged_turns %v", c.WantTurns)
				}
				if resp.Integrity.FlaggedTurns == nil || !slices.Equal(resp.Integrity.FlaggedTurns, c.WantTurns) {
					t.Fatalf("flagged_turns = %v, want %v", resp.Integrity.FlaggedTurns, c.WantTurns)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(raw["integrity"], &fields); err != nil {
					t.Fatalf("decode integrity: %v", err)
				}
				if len(fields) != 1 {
					t.Fatalf("integrity has %d keys, want exactly flagged_turns: %s", len(fields), raw["integrity"])
				}
				if string(raw["integrity"]) == "null" || !strings.HasPrefix(string(fields["flagged_turns"]), "[") {
					t.Fatalf("flagged_turns is not an array: %s", raw["integrity"])
				}
				if c.Name == "public_flagged_signed_out_identical" {
					signedIn := uuidFromPg(other)
					in := detail(id, &signedIn)
					var inRaw map[string]json.RawMessage
					if err := json.Unmarshal(in.Body.Bytes(), &inRaw); err != nil || in.Code != http.StatusOK {
						t.Fatalf("signed-in read: %d %v", in.Code, err)
					}
					if !bytes.Equal(inRaw["integrity"], raw["integrity"]) {
						t.Fatalf("integrity differs between signed-out %s and signed-in %s", raw["integrity"], inRaw["integrity"])
					}
				}
			default:
				t.Fatalf("unknown want %q", c.Want)
			}

			if c.Name == "list_and_pull_never_carry_integrity" {
				ov := uuidFromPg(owner)
				r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts", nil)
				r = r.WithContext(withUserID(r.Context(), ov))
				lw := httptest.NewRecorder()
				h.ListTranscripts(lw, r)
				if lw.Code != http.StatusOK {
					t.Fatalf("list status %d: %s", lw.Code, lw.Body.String())
				}
				if strings.Contains(lw.Body.String(), "integrity") || strings.Contains(lw.Body.String(), "flagged_turns") {
					t.Fatalf("the list carries integrity: %s", lw.Body.String())
				}
			}
		})
	}
}
