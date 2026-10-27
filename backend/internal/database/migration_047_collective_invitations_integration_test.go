//go:build integration

package database

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/membership-origin-transitions.yaml
var membershipOriginTransitionsYAML []byte

type membershipOriginTransition struct {
	Name    string `yaml:"name"`
	Role    string `yaml:"role"`
	From    string `yaml:"from"`
	To      string `yaml:"to"`
	Allowed bool   `yaml:"allowed"`
}

// requiredMembershipOriginTransitions names every transition the fixture must
// keep: the one permitted change and a refusal out of every origin.
var requiredMembershipOriginTransitions = []string{
	"legacy_becomes_invitation",
	"legacy_cannot_become_self_join",
	"legacy_cannot_become_creator",
	"invitation_cannot_become_legacy",
	"invitation_cannot_become_self_join",
	"self_join_cannot_become_invitation",
	"self_join_cannot_become_legacy",
	"creator_cannot_become_legacy",
	"creator_cannot_become_invitation",
}

func loadMembershipOriginTransitions(t *testing.T) []membershipOriginTransition {
	t.Helper()
	var cases []membershipOriginTransition
	decoder := yaml.NewDecoder(bytes.NewReader(membershipOriginTransitionsYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cases); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("transition fixture has extra YAML document: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		if c.Name == "" || present[c.Name] {
			t.Fatalf("empty or duplicate transition %q", c.Name)
		}
		present[c.Name] = true
	}
	for _, name := range requiredMembershipOriginTransitions {
		if !present[name] {
			t.Fatalf("the transition fixture no longer contains %q; restore it", name)
		}
	}
	return cases
}

// TestMigration047MembershipOriginAndInvitations proves migration 047 against
// real PostgreSQL: the backfill, the dropped default, the creator check, the
// write-once trigger, and the invitation and notification tables' keys and
// deletion rules.
func TestMigration047MembershipOriginAndInvitations(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	for _, m := range migrations {
		if m.version >= 47 {
			continue
		}
		if err := runMigration(pool, m); err != nil {
			t.Fatalf("run migration %d: %v", m.version, err)
		}
	}

	user := func(n int64, name string) pgtype.UUID {
		t.Helper()
		var id pgtype.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO users (github_id, github_username, provider_user_id) VALUES ($1, $2, $3) RETURNING id`, n, name, fmt.Sprint(n)).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", name, err)
		}
		return id
	}
	creator := user(947001, "origin-creator")
	coOwner := user(947002, "origin-co-owner")
	member := user(947003, "origin-member")
	pending := user(947004, "origin-pending")
	var group pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO groups (name, created_by) VALUES ('origin-collective', $1) RETURNING id`, creator).Scan(&group); err != nil {
		t.Fatal(err)
	}
	for person, role := range map[pgtype.UUID]string{creator: "owner", coOwner: "owner", member: "member", pending: "pending"} {
		if _, err := pool.Exec(ctx, `INSERT INTO group_members (group_id, user_id, role) VALUES ($1, $2, $3)`, group, person, role); err != nil {
			t.Fatal(err)
		}
	}

	if err := runMigration(pool, requireMigrationVersion(t, 47)); err != nil {
		t.Fatalf("run migration 047: %v", err)
	}

	// The backfill: only the creator's owner row is known; every other row,
	// a second owner included, is legacy.
	for person, want := range map[pgtype.UUID]string{creator: "creator", coOwner: "legacy", member: "legacy", pending: "legacy"} {
		var origin string
		if err := pool.QueryRow(ctx, `SELECT origin FROM group_members WHERE group_id = $1 AND user_id = $2`, group, person).Scan(&origin); err != nil {
			t.Fatal(err)
		}
		if origin != want {
			t.Errorf("backfilled origin %q, want %q", origin, want)
		}
	}

	// Deleting an account sets invited_by NULL on the invitations it sent, so
	// that column carries an index for the lookup.
	var indexDef string
	if err := pool.QueryRow(ctx, `SELECT indexdef FROM pg_indexes WHERE tablename = 'collective_invitations' AND indexname = 'idx_collective_invitations_invited_by'`).Scan(&indexDef); err != nil {
		t.Fatalf("read the invited_by index: %v", err)
	}
	if !strings.Contains(indexDef, "(invited_by)") {
		t.Fatalf("idx_collective_invitations_invited_by is %q, want an index on invited_by", indexDef)
	}

	// The default is gone: an insert that does not name its origin fails.
	newcomer := user(947005, "origin-newcomer")
	if _, err := pool.Exec(ctx, `INSERT INTO group_members (group_id, user_id, role) VALUES ($1, $2, 'member')`, group, newcomer); err == nil {
		t.Fatal("an insert without an origin was accepted; the default must be dropped")
	}
	// A creator row must be an owner.
	if _, err := pool.Exec(ctx, `INSERT INTO group_members (group_id, user_id, role, origin) VALUES ($1, $2, 'member', 'creator')`, group, newcomer); err == nil {
		t.Fatal("a creator membership that is not an owner was accepted")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO group_members (group_id, user_id, role, origin) VALUES ($1, $2, 'member', 'unknown')`, group, newcomer); err == nil {
		t.Fatal("an origin outside the closed set was accepted")
	}

	// The write-once trigger, one fresh row per transition.
	for i, c := range loadMembershipOriginTransitions(t) {
		t.Run(c.Name, func(t *testing.T) {
			person := user(947100+int64(i), "origin-transition-"+c.Name)
			if _, err := pool.Exec(ctx, `INSERT INTO group_members (group_id, user_id, role, origin) VALUES ($1, $2, $3, $4)`, group, person, c.Role, c.From); err != nil {
				t.Fatalf("seed the %s row: %v", c.From, err)
			}
			_, err := pool.Exec(ctx, `UPDATE group_members SET origin = $3 WHERE group_id = $1 AND user_id = $2`, group, person, c.To)
			if c.Allowed && err != nil {
				t.Fatalf("%s -> %s refused: %v", c.From, c.To, err)
			}
			if !c.Allowed && err == nil {
				t.Fatalf("%s -> %s accepted; the origin is written once", c.From, c.To)
			}
		})
	}
	// A role change keeps the origin and is not refused.
	if _, err := pool.Exec(ctx, `UPDATE group_members SET role = 'contributor' WHERE group_id = $1 AND user_id = $2`, group, member); err != nil {
		t.Fatalf("a role change of a legacy row was refused: %v", err)
	}

	// Invitations: one open invitation per collective and handle, a lowercase
	// handle, and the deletion rules.
	invite := func(handle string, invitee pgtype.UUID) error {
		_, err := pool.Exec(ctx, `INSERT INTO collective_invitations (group_id, invitee_id, invited_username, invited_by, status, expires_at)
			VALUES ($1, $2, $3, $4, 'pending', now() + interval '30 days')`, group, invitee, handle, creator)
		return err
	}
	invitee := user(947006, "origin-invitee")
	if err := invite("origin-invitee", invitee); err != nil {
		t.Fatal(err)
	}
	if err := invite("origin-invitee", invitee); err == nil {
		t.Fatal("a second invitation for the same collective and handle was accepted")
	}
	if err := invite("Mixed-Case", pgtype.UUID{}); err == nil {
		t.Fatal("a handle that is not lowercase was accepted")
	}
	if err := invite("nobody-holds-this", pgtype.UUID{}); err != nil {
		t.Fatalf("an invitation for a handle no account holds was refused: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO collective_invitations (group_id, invited_username, status, expires_at) VALUES ($1, 'x', 'accepted', now() + interval '1 day')`, group); err == nil {
		t.Fatal("a status outside pending and declined was accepted")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO collective_invitations (group_id, invited_username, status, created_at, expires_at) VALUES ($1, 'y', 'pending', now(), now() - interval '1 day')`, group); err == nil {
		t.Fatal("an invitation that expires before it was created was accepted")
	}

	// Deleting the invitee leaves an inert row; deleting the collective (below)
	// removes its invitations.
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, invitee); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM collective_invitations WHERE group_id = $1 AND invited_username = 'origin-invitee' AND invitee_id IS NULL`, group).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("the invitee's deletion left %d inert invitations (%v), want 1", remaining, err)
	}

	// Notification addresses: one per account, nothing about an address
	// without one, and removed with the account.
	if _, err := pool.Exec(ctx, `INSERT INTO user_notification_emails (user_id, verified_at) VALUES ($1, now())`, member); err == nil {
		t.Fatal("a verification without an address was accepted")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_notification_emails (user_id, address, verify_token_hash) VALUES ($1, 'a@example.org', '\x00')`, member); err == nil {
		t.Fatal("a verification token without an expiry was accepted")
	}
	// Daily limit counts: one slot at a time up to the limit, a closed set of
	// limits, and removed with the account.
	q := sqlc.New(pool)
	for i := 1; i <= 3; i++ {
		used, err := q.TakeDailyQuota(ctx, sqlc.TakeDailyQuotaParams{UserID: member, Quota: "invitation_email", DailyLimit: 3})
		if err != nil || used != int32(i) {
			t.Fatalf("slot %d: used %d (%v)", i, used, err)
		}
	}
	if _, err := q.TakeDailyQuota(ctx, sqlc.TakeDailyQuotaParams{UserID: member, Quota: "invitation_email", DailyLimit: 3}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("the fourth slot of three was taken (%v)", err)
	}
	if _, err := q.TakeDailyQuota(ctx, sqlc.TakeDailyQuotaParams{UserID: member, Quota: "unknown_limit", DailyLimit: 3}); err == nil {
		t.Fatal("a limit outside the closed set was counted")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_daily_quotas (user_id, quota, day, used) VALUES ($1, 'invitation_sent', current_date - 5, 1)`, member); err != nil {
		t.Fatal(err)
	}
	if err := q.DeleteStaleDailyQuotas(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM user_daily_quotas WHERE user_id = $1`, member).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("after clearing old counts %d remain (%v), want today's one", remaining, err)
	}

	var defaultOn bool
	if err := pool.QueryRow(ctx, `INSERT INTO user_notification_emails (user_id, address) VALUES ($1, 'member@example.org') RETURNING email_invitations`, member).Scan(&defaultOn); err != nil || !defaultOn {
		t.Fatalf("a new address must start with invitation emails on (%v, %v)", defaultOn, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, member); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM user_notification_emails WHERE address = 'member@example.org'`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("a deleted account kept %d notification addresses (%v)", remaining, err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM user_daily_quotas WHERE user_id = $1`, member).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("a deleted account kept %d limit counts (%v)", remaining, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM groups WHERE id = $1`, group); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM collective_invitations WHERE group_id = $1`, group).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("a deleted collective kept %d invitations (%v)", remaining, err)
	}
}
