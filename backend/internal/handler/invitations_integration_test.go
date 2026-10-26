//go:build integration

package handler

// Collective invitations driven through the production handlers against a
// real PostgreSQL: what the invite route answers and writes for every kind of
// name, what accepting and declining do, the owner's and the invitee's lists,
// revocation, and the invitation limits.

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/config"
)

//go:embed testdata/collective-invitations.yaml
var collectiveInvitationsYAML []byte

//go:embed testdata/invitation-answers.yaml
var invitationAnswersYAML []byte

//go:embed testdata/creator-membership.yaml
var creatorMembershipYAML []byte

type invitationCase struct {
	Name    string `yaml:"name"`
	Invitee string `yaml:"invitee"`
	Typed   string `yaml:"typed"`
	Prior   string `yaml:"prior"`
	Want    struct {
		Status int    `yaml:"status"`
		Row    string `yaml:"row"`
		Linked bool   `yaml:"linked"`
	} `yaml:"want"`
}

var requiredInvitationCases = []string{
	"unknown_name_is_invited",
	"hidden_account_is_invited",
	"discoverable_account_is_invited",
	"typed_handle_is_normalized",
	"consented_member_conflicts",
	"legacy_member_may_be_invited",
	"join_request_may_be_invited",
	"reinvite_while_pending_is_idempotent",
	"reinvite_after_decline_is_idempotent",
	"reinvite_unknown_while_pending_is_idempotent",
	"expired_invitation_is_replaced",
}

type invitationAnswerCase struct {
	Name       string `yaml:"name"`
	Mode       string `yaml:"mode"`
	Membership string `yaml:"membership"`
	State      string `yaml:"state"`
	Actor      string `yaml:"actor"`
	Action     string `yaml:"action"`
	Want       struct {
		Status     int    `yaml:"status"`
		Role       string `yaml:"role"`
		Origin     string `yaml:"origin"`
		Invitation string `yaml:"invitation"`
	} `yaml:"want"`
}

var requiredInvitationAnswerCases = []string{
	"accept_joins_an_open_collective",
	"accept_joins_a_curated_collective",
	"accept_bypasses_the_organization_check",
	"accept_records_a_legacy_members_consent",
	"accept_keeps_a_legacy_contributors_role",
	"accept_turns_a_join_request_into_a_member",
	"accept_keeps_a_legacy_owner_an_owner",
	"accept_by_a_consented_member_changes_nothing",
	"accept_by_someone_else_is_not_found",
	"accept_after_decline_is_not_found",
	"accept_after_expiry_is_not_found",
	"accept_after_revoke_is_not_found",
	"decline_keeps_the_row_for_the_owner",
	"decline_by_someone_else_is_not_found",
	"decline_twice_is_not_found",
}

func loadNamedFixture[T any](t *testing.T, data []byte, fixture string, name func(T) string, required []string) []T {
	t.Helper()
	rows, err := decodeFixtureRows[T](data)
	if err != nil {
		t.Fatalf("load %s: %v", fixture, err)
	}
	present := map[string]struct{}{}
	for _, row := range rows {
		n := name(row)
		if _, duplicate := present[n]; duplicate || n == "" {
			t.Fatalf("%s: empty or duplicate case %q", fixture, n)
		}
		present[n] = struct{}{}
	}
	assertExactTitleFixtureNames(t, fixture, present, required)
	return rows
}

// invitationWorld is one collective with its creator, and the people a case
// needs, all with unique handles.
type invitationWorld struct {
	pool    *pgxpool.Pool
	h       *Handler
	router  http.Handler
	suffix  string
	owner   *AuthUser
	group   pgtype.UUID
	people  []pgtype.UUID
	groups  []pgtype.UUID
	ownerID pgtype.UUID
}

func newInvitationWorld(t *testing.T, ctx context.Context, pool *pgxpool.Pool, mode string) *invitationWorld {
	t.Helper()
	w := &invitationWorld{pool: pool, suffix: strings.ReplaceAll(uuid.NewString(), "-", "")[:10]}
	w.h = New(&config.Config{FrontendURL: "https://village.example.test", BaseURL: "https://api.example.test"}, pool, nil)
	w.router = invitationRouter(w.h)
	w.ownerID = w.person(t, ctx, "inv-owner", true)
	w.owner = &AuthUser{ID: uuid.UUID(w.ownerID.Bytes)}
	w.group = w.collective(t, ctx, w.ownerID, mode)
	return w
}

func (w *invitationWorld) person(t *testing.T, ctx context.Context, prefix string, discoverable bool) pgtype.UUID {
	t.Helper()
	id := cvInsertUser(t, ctx, w.pool, prefix+"-"+w.suffix+"-"+fmt.Sprint(len(w.people)), discoverable)
	w.people = append(w.people, id)
	return id
}

func (w *invitationWorld) handle(t *testing.T, ctx context.Context, id pgtype.UUID) string {
	t.Helper()
	var handle string
	if err := w.pool.QueryRow(ctx, `SELECT github_username FROM users WHERE id = $1`, id).Scan(&handle); err != nil {
		t.Fatal(err)
	}
	return handle
}

func (w *invitationWorld) collective(t *testing.T, ctx context.Context, creator pgtype.UUID, mode string) pgtype.UUID {
	t.Helper()
	var group pgtype.UUID
	if err := w.pool.QueryRow(ctx, `INSERT INTO groups (name, created_by, acceptance_mode) VALUES ($1, $2, $3) RETURNING id`,
		"inv-"+w.suffix+"-"+fmt.Sprint(len(w.groups)), creator, mode).Scan(&group); err != nil {
		t.Fatal(err)
	}
	if _, err := w.pool.Exec(ctx, `INSERT INTO group_members (group_id, user_id, role, origin) VALUES ($1, $2, 'owner', 'creator')`, group, creator); err != nil {
		t.Fatal(err)
	}
	w.groups = append(w.groups, group)
	return group
}

func (w *invitationWorld) member(t *testing.T, ctx context.Context, group, person pgtype.UUID, role, origin string) {
	t.Helper()
	if _, err := w.pool.Exec(ctx, `INSERT INTO group_members (group_id, user_id, role, origin) VALUES ($1, $2, $3, $4)`, group, person, role, origin); err != nil {
		t.Fatal(err)
	}
}

// insertInvitation writes an invitation directly in one of the states a case
// starts from.
func (w *invitationWorld) insertInvitation(t *testing.T, ctx context.Context, group pgtype.UUID, handle string, invitee pgtype.UUID, state string) pgtype.UUID {
	t.Helper()
	status, created, expires := "pending", "now()", "now() + interval '30 days'"
	switch state {
	case "pending":
	case "declined":
		status = "declined"
	case "expired":
		created, expires = "now() - interval '31 days'", "now() - interval '1 day'"
	default:
		t.Fatalf("unknown invitation state %q", state)
	}
	var id pgtype.UUID
	if err := w.pool.QueryRow(ctx, `INSERT INTO collective_invitations (group_id, invitee_id, invited_username, invited_by, status, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, `+created+`, `+expires+`) RETURNING id`, group, invitee, strings.ToLower(handle), w.ownerID, status).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// restart replaces the handler with a fresh one on the same database, as a
// server restart or a second instance would, keeping any fake mail sender.
func (w *invitationWorld) restart() {
	var sender *mailService
	if w.h.mail != nil {
		sender = newMailServiceWith(w.h.mail.sender, testEmailSigningKey, &config.Config{FrontendURL: "https://village.example.test", BaseURL: "https://api.example.test"})
	}
	w.h.waitForNotices()
	w.h = New(&config.Config{FrontendURL: "https://village.example.test", BaseURL: "https://api.example.test"}, w.pool, nil)
	w.h.mail = sender
	w.router = invitationRouter(w.h)
}

func (w *invitationWorld) call(t *testing.T, actor *AuthUser, method, path string, body any) (int, []byte) {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if actor != nil {
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, actor))
	}
	rec := httptest.NewRecorder()
	w.router.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func (w *invitationWorld) cleanup(t *testing.T, ctx context.Context) {
	t.Helper()
	w.h.waitForNotices()
	if _, err := w.pool.Exec(ctx, `DELETE FROM groups WHERE id = ANY($1)`, w.groups); err != nil {
		t.Errorf("cleanup collectives: %v", err)
	}
	cleanupOwners(t, ctx, w.pool, w.people...)
}

func invitationRouter(h *Handler) http.Handler {
	r := chi.NewRouter()
	r.Post("/groups/{id}/invitations", h.CreateGroupInvitation)
	r.Get("/groups/{id}/invitations", h.ListGroupInvitations)
	r.Delete("/groups/{id}/invitations/{invitationID}", h.RevokeGroupInvitation)
	r.Get("/groups/{id}", h.GetGroup)
	r.Get("/users/me/invitations", h.ListMyInvitations)
	r.Post("/users/me/invitations/{invitationID}/accept", h.AcceptMyInvitation)
	r.Post("/users/me/invitations/{invitationID}/decline", h.DeclineMyInvitation)
	r.Get("/users/me/settings", h.GetUserSettings)
	r.Patch("/users/me/settings", h.UpdateUserSettings)
	r.Put("/users/me/email", h.SetMyNotificationEmail)
	r.Post("/users/me/email/verify", h.VerifyMyNotificationEmail)
	r.Delete("/users/me/email", h.RemoveMyNotificationEmail)
	r.Post("/email/unsubscribe", h.UnsubscribeInvitationEmails)
	r.Get("/users/{username}", h.GetUserPublicProfile)
	return r
}

type invitationRow struct {
	ID        pgtype.UUID
	InviteeID pgtype.UUID
	Status    string
	Expired   bool
}

func readInvitationRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, group pgtype.UUID, handle string) []invitationRow {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT id, invitee_id, status, expires_at <= now() FROM collective_invitations WHERE group_id = $1 AND invited_username = $2`, group, handle)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []invitationRow
	for rows.Next() {
		var row invitationRow
		if err := rows.Scan(&row.ID, &row.InviteeID, &row.Status, &row.Expired); err != nil {
			t.Fatal(err)
		}
		out = append(out, row)
	}
	return out
}

// TestCreateGroupInvitationAnswersAlike drives every row of
// testdata/collective-invitations.yaml through the production invite handler.
func TestCreateGroupInvitationAnswersAlike(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	cases := loadNamedFixture(t, collectiveInvitationsYAML, "collective-invitations", func(c invitationCase) string { return c.Name }, requiredInvitationCases)
	var acceptedBodies [][]byte
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			w := newInvitationWorld(t, ctx, pool, "curated")
			defer w.cleanup(t, ctx)

			var account pgtype.UUID
			handle := "inv-nobody-" + w.suffix
			switch c.Invitee {
			case "unknown":
			case "hidden", "discoverable":
				account = w.person(t, ctx, "inv-"+c.Invitee, c.Invitee == "discoverable")
			case "consented_member":
				account = w.person(t, ctx, "inv-joined", false)
				w.member(t, ctx, w.group, account, "contributor", "self_join")
			case "legacy_member":
				account = w.person(t, ctx, "inv-legacy", false)
				w.member(t, ctx, w.group, account, "member", "legacy")
			case "join_request":
				account = w.person(t, ctx, "inv-pending", false)
				w.member(t, ctx, w.group, account, "pending", "legacy")
			default:
				t.Fatalf("unknown invitee %q", c.Invitee)
			}
			if account.Valid {
				handle = w.handle(t, ctx, account)
			}
			typed := handle
			switch c.Typed {
			case "exact":
			case "upper":
				typed = "  @" + strings.ToUpper(handle) + " "
			default:
				t.Fatalf("unknown typed form %q", c.Typed)
			}
			var prior pgtype.UUID
			if c.Prior != "none" {
				prior = w.insertInvitation(t, ctx, w.group, handle, account, c.Prior)
			}

			status, body := w.call(t, w.owner, http.MethodPost, "/groups/"+pgIDString(w.group)+"/invitations", map[string]string{"username": typed})
			if status != c.Want.Status {
				t.Fatalf("status %d (%s), want %d", status, body, c.Want.Status)
			}
			if status == http.StatusAccepted {
				acceptedBodies = append(acceptedBodies, body)
			}

			rows := readInvitationRows(t, ctx, pool, w.group, handle)
			switch c.Want.Row {
			case "none":
				if len(rows) != 0 {
					t.Fatalf("wrote %d invitations, want none", len(rows))
				}
				return
			case "kept":
				if len(rows) != 1 || rows[0].ID != prior {
					t.Fatalf("rows %+v, want only the earlier invitation %v", rows, prior)
				}
			case "new":
				if len(rows) != 1 || rows[0].ID == prior || rows[0].Status != "pending" || rows[0].Expired {
					t.Fatalf("rows %+v, want one new open pending invitation", rows)
				}
			default:
				t.Fatalf("unknown row expectation %q", c.Want.Row)
			}
			if c.Want.Row == "new" {
				var lifetime time.Duration
				if err := pool.QueryRow(ctx, `SELECT EXTRACT(EPOCH FROM expires_at - created_at)::bigint * 1000000000 FROM collective_invitations WHERE id = $1`, rows[0].ID).Scan(&lifetime); err != nil {
					t.Fatal(err)
				}
				if lifetime != invitationLifetimeDays*24*time.Hour {
					t.Fatalf("invitation lifetime %v, want %d days", lifetime, invitationLifetimeDays)
				}
			}
			if rows[0].InviteeID.Valid != c.Want.Linked || (c.Want.Linked && rows[0].InviteeID != account) {
				t.Fatalf("invitee_id %v, want linked=%v to %v", rows[0].InviteeID, c.Want.Linked, account)
			}

			// The owner's list shows the typed handle, lowercased, and nothing
			// about whether an account holds it.
			status, body = w.call(t, w.owner, http.MethodGet, "/groups/"+pgIDString(w.group)+"/invitations", nil)
			if status != http.StatusOK {
				t.Fatalf("owner list %d: %s", status, body)
			}
			var sent schema.VillageCollectiveInvitationsSentResponse
			if err := json.Unmarshal(body, &sent); err != nil {
				t.Fatal(err)
			}
			if err := sent.Validate(); err != nil {
				t.Fatal(err)
			}
			if len(sent.Invitations) != 1 || sent.Invitations[0].InvitedUsername != handle || !sent.Invitations[0].InvitedBy.IsAccount(schema.VillageUUID(pgIDString(w.ownerID))) {
				t.Fatalf("owner list %s, want one invitation of %q sent by the owner", body, handle)
			}
			if account.Valid && strings.Contains(string(body), pgIDString(account)) {
				t.Fatalf("the owner's list names the invitee's account: %s", body)
			}
		})
	}
	// Every 202 is byte-for-byte the same answer.
	for _, body := range acceptedBodies {
		if string(body) != "{\"status\":\"invited\"}\n" {
			t.Fatalf("a 202 answered %q; every invite must answer the same bytes", body)
		}
	}
}

// TestInvitationAnswers drives every row of testdata/invitation-answers.yaml
// through the accept and decline handlers.
func TestInvitationAnswers(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	cases := loadNamedFixture(t, invitationAnswersYAML, "invitation-answers", func(c invitationAnswerCase) string { return c.Name }, requiredInvitationAnswerCases)
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			w := newInvitationWorld(t, ctx, pool, c.Mode)
			defer w.cleanup(t, ctx)
			invitee := w.person(t, ctx, "inv-invitee", false)
			other := w.person(t, ctx, "inv-other", true)
			switch c.Membership {
			case "none":
			case "legacy_member":
				w.member(t, ctx, w.group, invitee, "member", "legacy")
			case "legacy_contributor":
				w.member(t, ctx, w.group, invitee, "contributor", "legacy")
			case "legacy_join_request":
				w.member(t, ctx, w.group, invitee, "pending", "legacy")
			case "legacy_owner":
				w.member(t, ctx, w.group, invitee, "owner", "legacy")
			case "contributor_self_join":
				w.member(t, ctx, w.group, invitee, "contributor", "self_join")
			default:
				t.Fatalf("unknown membership %q", c.Membership)
			}
			handle := w.handle(t, ctx, invitee)
			state := c.State
			if state == "revoked" {
				state = "pending"
			}
			invitation := w.insertInvitation(t, ctx, w.group, handle, invitee, state)
			if c.State == "revoked" {
				if status, body := w.call(t, w.owner, http.MethodDelete, "/groups/"+pgIDString(w.group)+"/invitations/"+pgIDString(invitation), nil); status != http.StatusOK {
					t.Fatalf("revoke %d: %s", status, body)
				}
			}
			actor := &AuthUser{ID: uuid.UUID(invitee.Bytes)}
			if c.Actor == "other" {
				actor = &AuthUser{ID: uuid.UUID(other.Bytes)}
			}
			status, body := w.call(t, actor, http.MethodPost, "/users/me/invitations/"+pgIDString(invitation)+"/"+c.Action, nil)
			if status != c.Want.Status {
				t.Fatalf("status %d (%s), want %d", status, body, c.Want.Status)
			}
			if status == http.StatusOK && c.Action == "accept" {
				var answer schema.VillageCollectiveStatusRoleResponse
				if err := json.Unmarshal(body, &answer); err != nil || answer.Status != "joined" || string(answer.Role) != c.Want.Role {
					t.Fatalf("accept answered %s, want joined as %s", body, c.Want.Role)
				}
			}

			role, origin := "none", "none"
			if err := pool.QueryRow(ctx, `SELECT role, origin FROM group_members WHERE group_id = $1 AND user_id = $2`, w.group, invitee).Scan(&role, &origin); err != nil && c.Want.Role != "none" {
				t.Fatalf("read the membership: %v", err)
			}
			if c.Actor == "other" || c.Want.Status == http.StatusNotFound {
				// Nothing changed for the invitee: their membership is what the
				// case started with.
				if c.Membership == "none" && role != "none" {
					t.Fatalf("a refused answer left a membership %s/%s", role, origin)
				}
			} else if role != c.Want.Role || origin != c.Want.Origin {
				t.Fatalf("membership %s/%s, want %s/%s", role, origin, c.Want.Role, c.Want.Origin)
			}
			left := "gone"
			var leftStatus string
			if err := pool.QueryRow(ctx, `SELECT status FROM collective_invitations WHERE id = $1`, invitation).Scan(&leftStatus); err == nil {
				left = leftStatus
			}
			if left != c.Want.Invitation {
				t.Fatalf("invitation is %s, want %s", left, c.Want.Invitation)
			}

			// A declined invitation looks exactly like a pending one to the
			// owner, and inviting again writes nothing new.
			if c.Name == "decline_keeps_the_row_for_the_owner" {
				_, list := w.call(t, w.owner, http.MethodGet, "/groups/"+pgIDString(w.group)+"/invitations", nil)
				if !strings.Contains(string(list), `"invited_username":"`+handle+`"`) || strings.Contains(string(list), "declined") {
					t.Fatalf("owner list after a decline: %s", list)
				}
				if status, body := w.call(t, w.owner, http.MethodPost, "/groups/"+pgIDString(w.group)+"/invitations", map[string]string{"username": handle}); status != http.StatusAccepted {
					t.Fatalf("re-invite after decline %d: %s", status, body)
				}
				if rows := readInvitationRows(t, ctx, pool, w.group, handle); len(rows) != 1 || rows[0].ID != invitation || rows[0].Status != "declined" {
					t.Fatalf("re-invite after decline changed the invitation: %+v", rows)
				}
				// The invitee's own list no longer offers it.
				_, mine := w.call(t, actor, http.MethodGet, "/users/me/invitations", nil)
				if strings.Contains(string(mine), pgIDString(invitation)) {
					t.Fatalf("a declined invitation is still offered: %s", mine)
				}
			}
		})
	}
}

// TestMyInvitationsShowTheInviterAsTheInviteeMaySee proves the invitee's list:
// only their own pending, unexpired invitations, the collective's summary even
// when it is members-only, and a hidden inviter as anonymous.
func TestMyInvitationsShowTheInviterAsTheInviteeMaySee(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	w := newInvitationWorld(t, ctx, pool, "curated")
	defer w.cleanup(t, ctx)
	if _, err := pool.Exec(ctx, `UPDATE users SET is_discoverable = false WHERE id = $1`, w.ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE groups SET description = 'night shift', data_access = 'members_only' WHERE id = $1`, w.group); err != nil {
		t.Fatal(err)
	}
	invitee := w.person(t, ctx, "inv-invitee", false)
	someoneElse := w.person(t, ctx, "inv-else", false)
	handle := w.handle(t, ctx, invitee)
	open := w.insertInvitation(t, ctx, w.group, handle, invitee, "pending")
	second := w.collective(t, ctx, w.ownerID, "open")
	w.insertInvitation(t, ctx, second, handle, invitee, "expired")
	third := w.collective(t, ctx, w.ownerID, "open")
	w.insertInvitation(t, ctx, third, w.handle(t, ctx, someoneElse), someoneElse, "pending")

	status, body := w.call(t, &AuthUser{ID: uuid.UUID(invitee.Bytes)}, http.MethodGet, "/users/me/invitations", nil)
	if status != http.StatusOK {
		t.Fatalf("list %d: %s", status, body)
	}
	var mine schema.VillageMyInvitationsResponse
	if err := json.Unmarshal(body, &mine); err != nil {
		t.Fatal(err)
	}
	if err := mine.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(mine.Invitations) != 1 || mine.Invitations[0].ID != schema.VillageUUID(pgIDString(open)) {
		t.Fatalf("list %s, want only the open invitation", body)
	}
	got := mine.Invitations[0]
	if got.Collective.Description == nil || *got.Collective.Description != "night shift" || got.Collective.AcceptanceMode != "curated" {
		t.Fatalf("collective summary %+v", got.Collective)
	}
	if got.InvitedBy.IsProfile() || strings.Contains(string(body), w.handle(t, ctx, w.ownerID)) || strings.Contains(string(body), pgIDString(w.ownerID)) {
		t.Fatalf("a hidden inviter is shown to the invitee: %s", body)
	}
	if !got.ExpiresAt.After(got.CreatedAt) {
		t.Fatalf("expiry %v not after creation %v", got.ExpiresAt, got.CreatedAt)
	}
}

// TestInvitationLimits proves the two limits answer 429 and write nothing.
func TestInvitationLimits(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()

	t.Run("open_invitations_per_collective", func(t *testing.T) {
		w := newInvitationWorld(t, ctx, pool, "curated")
		defer w.cleanup(t, ctx)
		for i := 0; i < openInvitationsPerCollective; i++ {
			w.insertInvitation(t, ctx, w.group, fmt.Sprintf("inv-cap-%s-%d", w.suffix, i), pgtype.UUID{}, "pending")
		}
		// An expired invitation does not count.
		w.insertInvitation(t, ctx, w.group, "inv-cap-expired-"+w.suffix, pgtype.UUID{}, "expired")
		status, body := w.call(t, w.owner, http.MethodPost, "/groups/"+pgIDString(w.group)+"/invitations", map[string]string{"username": "inv-one-more-" + w.suffix})
		if status != http.StatusTooManyRequests {
			t.Fatalf("status %d (%s), want 429", status, body)
		}
		if rows := readInvitationRows(t, ctx, pool, w.group, "inv-one-more-"+w.suffix); len(rows) != 0 {
			t.Fatalf("a refused invitation was written: %+v", rows)
		}
		// Re-inviting an open handle is still the idempotent answer.
		if status, body := w.call(t, w.owner, http.MethodPost, "/groups/"+pgIDString(w.group)+"/invitations", map[string]string{"username": fmt.Sprintf("inv-cap-%s-0", w.suffix)}); status != http.StatusAccepted {
			t.Fatalf("re-invite at the cap %d: %s", status, body)
		}
	})

	t.Run("invitations_per_inviter_per_day", func(t *testing.T) {
		w := newInvitationWorld(t, ctx, pool, "curated")
		defer w.cleanup(t, ctx)
		for i := 0; i < invitationsPerInviterPerDay; i++ {
			group := w.group
			if i%2 == 1 {
				group = w.collective(t, ctx, w.ownerID, "open")
			}
			if status, body := w.call(t, w.owner, http.MethodPost, "/groups/"+pgIDString(group)+"/invitations", map[string]string{"username": fmt.Sprintf("inv-day-%s-%d", w.suffix, i)}); status != http.StatusAccepted {
				t.Fatalf("invitation %d answered %d: %s", i, status, body)
			}
		}
		// The count is in the database: a restarted server still refuses.
		w.restart()
		status, body := w.call(t, w.owner, http.MethodPost, "/groups/"+pgIDString(w.group)+"/invitations", map[string]string{"username": "inv-day-over-" + w.suffix})
		if status != http.StatusTooManyRequests {
			t.Fatalf("status %d (%s), want 429", status, body)
		}
		if rows := readInvitationRows(t, ctx, pool, w.group, "inv-day-over-"+w.suffix); len(rows) != 0 {
			t.Fatalf("a refused invitation was written: %+v", rows)
		}
	})
}

// TestInvitationOwnerRoutesRefuseOthers proves the owner routes answer 403 to
// a member who is not an owner, and revoking another collective's invitation
// is 404.
func TestInvitationOwnerRoutesRefuseOthers(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	w := newInvitationWorld(t, ctx, pool, "curated")
	defer w.cleanup(t, ctx)
	member := w.person(t, ctx, "inv-member", true)
	w.member(t, ctx, w.group, member, "member", "self_join")
	actor := &AuthUser{ID: uuid.UUID(member.Bytes)}
	base := "/groups/" + pgIDString(w.group) + "/invitations"
	if status, _ := w.call(t, actor, http.MethodPost, base, map[string]string{"username": "someone"}); status != http.StatusForbidden {
		t.Fatalf("member invite %d, want 403", status)
	}
	if status, _ := w.call(t, actor, http.MethodGet, base, nil); status != http.StatusForbidden {
		t.Fatalf("member list %d, want 403", status)
	}
	other := w.collective(t, ctx, w.ownerID, "open")
	foreign := w.insertInvitation(t, ctx, other, "inv-foreign-"+w.suffix, pgtype.UUID{}, "pending")
	if status, _ := w.call(t, w.owner, http.MethodDelete, base+"/"+pgIDString(foreign), nil); status != http.StatusNotFound {
		t.Fatalf("revoking another collective's invitation %d, want 404", status)
	}
}

// pgIDString is a stored id as the text a path or a response carries.
func pgIDString(id pgtype.UUID) string { return uuid.UUID(id.Bytes).String() }

// creatorMembershipCase is one row of testdata/creator-membership.yaml.
type creatorMembershipCase struct {
	Name   string `yaml:"name"`
	Action string `yaml:"action"`
	Actor  string `yaml:"actor"`
	Target string `yaml:"target"`
	Want   struct {
		Status  int    `yaml:"status"`
		Target  string `yaml:"target"`
		Creator string `yaml:"creator"`
	} `yaml:"want"`
}

var requiredCreatorMembershipCases = []string{
	"co_owner_cannot_demote_the_creator",
	"creator_cannot_demote_themselves",
	"creator_may_change_a_co_owners_role",
	"co_owner_cannot_remove_the_creator",
	"creator_cannot_leave",
	"owner_removes_a_member",
	"creator_removes_a_co_owner",
	"deleting_the_collective_removes_the_creator",
	"deleting_the_creators_account_removes_the_creator",
}

// membershipRole is a person's role in a collective, or "gone".
func membershipRole(t *testing.T, ctx context.Context, pool *pgxpool.Pool, group, person pgtype.UUID) string {
	t.Helper()
	var role string
	err := pool.QueryRow(ctx, `SELECT role FROM group_members WHERE group_id = $1 AND user_id = $2`, group, person).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "gone"
	}
	if err != nil {
		t.Fatal(err)
	}
	return role
}

// TestCreatorMembershipStays drives every row of
// testdata/creator-membership.yaml through the production role, remove,
// collective-delete and account-delete handlers. Mutation check: dropping
// the creator guard in removeGroupMemberLocked fails
// co_owner_cannot_remove_the_creator and creator_cannot_leave.
func TestCreatorMembershipStays(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	cases := loadNamedFixture(t, creatorMembershipYAML, "creator-membership", func(c creatorMembershipCase) string { return c.Name }, requiredCreatorMembershipCases)
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			w := newInvitationWorld(t, ctx, pool, "curated")
			defer w.cleanup(t, ctx)
			coOwner := w.person(t, ctx, "inv-co-owner", true)
			w.member(t, ctx, w.group, coOwner, "owner", "legacy")
			member := w.person(t, ctx, "inv-member", true)
			w.member(t, ctx, w.group, member, "member", "self_join")
			people := map[string]pgtype.UUID{"creator": w.ownerID, "co_owner": coOwner, "member": member}
			actor, ok := people[c.Actor]
			target, known := people[c.Target]
			if !ok || !known {
				t.Fatalf("unknown actor %q or target %q", c.Actor, c.Target)
			}
			r := chi.NewRouter()
			r.Patch("/groups/{id}/members/{userID}/role", w.h.PromoteMember)
			r.Delete("/groups/{id}/members/{userID}", w.h.RemoveGroupMember)
			r.Delete("/groups/{id}", w.h.DeleteGroup)
			r.Delete("/auth/me", w.h.DeleteAccount)
			group := "/groups/" + pgIDString(w.group)
			method, path, body := http.MethodDelete, "", ""
			switch c.Action {
			case "role":
				method, path, body = http.MethodPatch, group+"/members/"+pgIDString(target)+"/role", `{"role":"member"}`
			case "remove":
				path = group + "/members/" + pgIDString(target)
			case "delete_collective":
				path = group
			case "delete_account":
				path = "/auth/me"
			default:
				t.Fatalf("unknown action %q", c.Action)
			}
			req := httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &AuthUser{ID: uuid.UUID(actor.Bytes)}))
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != c.Want.Status {
				t.Fatalf("%s %s answered %d: %s, want %d", method, path, rec.Code, rec.Body.String(), c.Want.Status)
			}
			if got := membershipRole(t, ctx, pool, w.group, target); got != c.Want.Target {
				t.Fatalf("target membership %s, want %s", got, c.Want.Target)
			}
			if got := membershipRole(t, ctx, pool, w.group, w.ownerID); got != c.Want.Creator {
				t.Fatalf("creator membership %s, want %s", got, c.Want.Creator)
			}
			if c.Want.Creator == "owner" {
				var origin string
				if err := pool.QueryRow(ctx, `SELECT origin FROM group_members WHERE group_id = $1 AND user_id = $2`, w.group, w.ownerID).Scan(&origin); err != nil || origin != originCreator {
					t.Fatalf("the creator's origin is now %q (%v)", origin, err)
				}
			}
		})
	}
}
