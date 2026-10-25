package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

const (
	// invitationLifetimeDays is how long an invitation stays open.
	invitationLifetimeDays = 30
	// invitationsPerInviterPerDay bounds how many new invitations one owner
	// sends in a day, across every collective they own.
	invitationsPerInviterPerDay = 20
	// openInvitationsPerCollective bounds a collective's open invitations.
	openInvitationsPerCollective = 50
	// invitedUsernameMaxLength bounds the handle an owner may type.
	invitedUsernameMaxLength = 100
)

// Membership origins (group_members.origin). A membership the person consented
// to is one they created, joined themselves, or accepted an invitation to; a
// legacy one predates provenance. ListPublicOwnersForViewer reads the same
// list in SQL to decide what the collective's owners may see.
const (
	originCreator    = "creator"
	originSelfJoin   = "self_join"
	originInvitation = "invitation"
)

// consentedMembership reports whether a membership is one the person chose:
// a consented origin and not a leftover join request.
func consentedMembership(member sqlc.GroupMember) bool {
	if member.Role == "pending" {
		return false
	}
	switch member.Origin {
	case originCreator, originSelfJoin, originInvitation:
		return true
	}
	return false
}

// normalizeInvitedUsername turns what the owner typed into the stored handle:
// trimmed, one leading @ dropped, lowercased. It refuses only by shape, never
// by whether an account holds the handle, so a refusal confirms nothing.
func normalizeInvitedUsername(typed string) (string, bool) {
	handle := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(typed), "@"))
	if handle == "" || utf8.RuneCountInString(handle) > invitedUsernameMaxLength {
		return "", false
	}
	for _, r := range handle {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == '/' {
			return "", false
		}
	}
	return handle, true
}

// committedStatus records the status a wrapped writer was given, so a caller
// can act only after a mutation's response says it committed.
type committedStatus struct {
	http.ResponseWriter
	status int
}

func (c *committedStatus) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
	c.ResponseWriter.WriteHeader(status)
}

func (c *committedStatus) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	return c.ResponseWriter.Write(p)
}

// CreateGroupInvitation invites a person to an owned collective by username.
// POST /groups/{id}/invitations (AuthRequired, owner only).
//
// The answer is 202 {"status":"invited"} whether no account holds the name,
// its account is hidden, or it is discoverable, and a row is written in every
// case, so inviting never confirms that an account exists. Any email is sent
// after the response, off the request path.
func (h *Handler) CreateGroupInvitation(w http.ResponseWriter, r *http.Request) {
	var notice *invitationNotice
	recorder := &committedStatus{ResponseWriter: w}
	h.mutateCollective(recorder, r, func(scoped *Handler, w http.ResponseWriter, r *http.Request) {
		notice = scoped.createGroupInvitationLocked(w, r)
	}, false)
	if recorder.status == http.StatusAccepted && notice != nil {
		h.dispatchInvitationNotice(*notice)
	}
}

func (h *Handler) createGroupInvitationLocked(w http.ResponseWriter, r *http.Request) *invitationNotice {
	user := GetUser(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid collective ID")
		return nil
	}
	groupID := toPgUUID(id)
	member, err := h.queries.GetGroupMember(r.Context(), sqlc.GetGroupMemberParams{GroupID: groupID, UserID: user.PgID()})
	if err != nil || member.Role != "owner" {
		writeError(w, http.StatusForbidden, "Owner access required")
		return nil
	}
	var req schema.VillageCollectiveInvitationRequest
	if !h.decodeContractBody(w, r, opCreateGroupInvitation, &req) {
		return nil
	}
	handle, ok := normalizeInvitedUsername(req.Username)
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Type a username of 1 to %d characters with no spaces", invitedUsernameMaxLength))
		return nil
	}
	group, err := h.queries.GetGroupByID(r.Context(), groupID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read the collective; nobody was invited. Retry the request.")
		return nil
	}

	// The account that holds the handle, if any. Its discoverability is never
	// consulted: a hidden person is invited exactly like a discoverable one.
	var inviteeID pgtype.UUID
	invitee, err := h.queries.GetUserByUsername(r.Context(), handle)
	switch {
	case err == nil:
		inviteeID = invitee.ID
	case errors.Is(err, pgx.ErrNoRows):
	default:
		writeError(w, http.StatusInternalServerError, "Could not read the username; nobody was invited. Retry the request.")
		return nil
	}
	if inviteeID.Valid {
		existing, err := h.queries.GetGroupMember(r.Context(), sqlc.GetGroupMemberParams{GroupID: groupID, UserID: inviteeID})
		switch {
		case err == nil && consentedMembership(existing):
			writeError(w, http.StatusConflict, "@"+handle+" is already a member of this collective")
			return nil
		case err != nil && !errors.Is(err, pgx.ErrNoRows):
			writeError(w, http.StatusInternalServerError, "Could not read the collective's members; nobody was invited. Retry the request.")
			return nil
		}
	}

	// Inviting again while an invitation is open, pending or declined, is the
	// same answer with no write and no notice.
	if _, err := h.queries.GetOpenGroupInvitationByUsername(r.Context(), sqlc.GetOpenGroupInvitationByUsernameParams{GroupID: groupID, InvitedUsername: handle}); err == nil {
		writeJSON(w, http.StatusAccepted, schema.VillageStatusResponse{Status: "invited"})
		return nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "Could not read the collective's invitations; nobody was invited. Retry the request.")
		return nil
	}

	open, err := h.queries.CountOpenGroupInvitations(r.Context(), groupID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not count the collective's invitations; nobody was invited. Retry the request.")
		return nil
	}
	if open >= openInvitationsPerCollective {
		writeError(w, http.StatusTooManyRequests, fmt.Sprintf("This collective already has %d open invitations. Revoke some, or wait for them to be answered or to expire, then invite again.", openInvitationsPerCollective))
		return nil
	}
	// Counted on the invite's own transaction: a refused or failed invite
	// gives its slot back.
	allowed, err := takeDailyQuota(r.Context(), h.queries, user.PgID(), quotaInvitationSent, invitationsPerInviterPerDay)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not count your invitations; nobody was invited. Retry the request.")
		return nil
	}
	if !allowed {
		writeError(w, http.StatusTooManyRequests, fmt.Sprintf("You have sent %d invitations in the last day, the most one owner may send. Try again tomorrow.", invitationsPerInviterPerDay))
		return nil
	}

	if err := h.queries.DeleteExpiredGroupInvitationByUsername(r.Context(), sqlc.DeleteExpiredGroupInvitationByUsernameParams{GroupID: groupID, InvitedUsername: handle}); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not replace an expired invitation; nobody was invited. Retry the request.")
		return nil
	}
	created, err := h.queries.CreateGroupInvitation(r.Context(), sqlc.CreateGroupInvitationParams{
		GroupID: groupID, InviteeID: inviteeID, InvitedUsername: handle, InvitedBy: user.PgID(), LifetimeDays: invitationLifetimeDays,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// A concurrent invitation for the same handle won; same answer.
		writeJSON(w, http.StatusAccepted, schema.VillageStatusResponse{Status: "invited"})
		return nil
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not record the invitation; nobody was invited. Retry the request.")
		return nil
	}
	writeJSON(w, http.StatusAccepted, schema.VillageStatusResponse{Status: "invited"})
	return &invitationNotice{invitationID: created.ID, inviteeID: created.InviteeID, inviterID: user.PgID(), collectiveName: group.Name}
}

// ListGroupInvitations lists an owned collective's open invitations, each as
// the handle the owner typed. GET /groups/{id}/invitations (AuthRequired,
// owner only).
func (h *Handler) ListGroupInvitations(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid collective ID")
		return
	}
	groupID := toPgUUID(id)
	member, err := h.queries.GetGroupMember(r.Context(), sqlc.GetGroupMemberParams{GroupID: groupID, UserID: user.PgID()})
	if err != nil || member.Role != "owner" {
		writeError(w, http.StatusForbidden, "Owner access required")
		return
	}
	rows, err := h.queries.ListGroupInvitations(r.Context(), groupID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not list the collective's invitations; retry the request")
		return
	}
	inviters := make([]pgtype.UUID, 0, len(rows))
	for _, row := range rows {
		inviters = append(inviters, row.InvitedBy)
	}
	owners, err := h.resolvePublicOwners(r.Context(), user, inviters)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not resolve who sent the collective's invitations; retry the request")
		return
	}
	response := schema.VillageCollectiveInvitationsSentResponse{Invitations: make([]schema.VillageCollectiveInvitationSent, 0, len(rows))}
	for _, row := range rows {
		response.Invitations = append(response.Invitations, schema.VillageCollectiveInvitationSent{
			ID: villageUUID(row.ID), InvitedUsername: row.InvitedUsername, InvitedBy: owners.of(row.InvitedBy),
			CreatedAt: row.CreatedAt.Time, ExpiresAt: row.ExpiresAt.Time,
		})
	}
	writeJSON(w, http.StatusOK, response)
}

// RevokeGroupInvitation revokes an open invitation of an owned collective.
// DELETE /groups/{id}/invitations/{invitationID} (AuthRequired, owner only).
// Any listed invitation answers 200, whatever became of its invitee.
func (h *Handler) RevokeGroupInvitation(w http.ResponseWriter, r *http.Request) {
	h.mutateCollective(w, r, (*Handler).revokeGroupInvitationLocked, false)
}

func (h *Handler) revokeGroupInvitationLocked(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid collective ID")
		return
	}
	invitationID, err := uuid.Parse(chi.URLParam(r, "invitationID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid invitation ID")
		return
	}
	groupID := toPgUUID(id)
	member, err := h.queries.GetGroupMember(r.Context(), sqlc.GetGroupMemberParams{GroupID: groupID, UserID: user.PgID()})
	if err != nil || member.Role != "owner" {
		writeError(w, http.StatusForbidden, "Owner access required")
		return
	}
	revoked, err := h.queries.RevokeGroupInvitation(r.Context(), sqlc.RevokeGroupInvitationParams{ID: toPgUUID(invitationID), GroupID: groupID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not revoke the invitation; retry the request")
		return
	}
	if revoked == 0 {
		writeError(w, http.StatusNotFound, "This collective has no open invitation with that id; reload its invitations")
		return
	}
	writeJSON(w, http.StatusOK, schema.VillageStatusResponse{Status: "revoked"})
}

// ListMyInvitations lists the caller's own pending invitations.
// GET /users/me/invitations (AuthRequired).
func (h *Handler) ListMyInvitations(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	rows, err := h.queries.ListMyInvitations(r.Context(), user.PgID())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not list your invitations; retry the request")
		return
	}
	inviters := make([]pgtype.UUID, 0, len(rows))
	for _, row := range rows {
		inviters = append(inviters, row.InvitedBy)
	}
	// The inviting owner as the caller may see them: an owner who is not
	// discoverable is anonymous here.
	owners, err := h.resolvePublicOwners(r.Context(), user, inviters)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not resolve who invited you; retry the request")
		return
	}
	response := schema.VillageMyInvitationsResponse{Invitations: make([]schema.VillageCollectiveInvitation, 0, len(rows))}
	for _, row := range rows {
		response.Invitations = append(response.Invitations, schema.VillageCollectiveInvitation{
			ID: villageUUID(row.ID),
			Collective: schema.VillageInvitedCollective{
				ID: villageUUID(row.GroupID), Name: row.GroupName, Description: pgTextPointer(row.GroupDescription),
				AcceptanceMode: schema.VillageCollectiveAcceptanceMode(row.GroupAcceptanceMode),
			},
			InvitedBy: owners.of(row.InvitedBy), CreatedAt: row.CreatedAt.Time, ExpiresAt: row.ExpiresAt.Time,
		})
	}
	writeJSON(w, http.StatusOK, response)
}

// errInvitationNotOpen is an invitation that is not the caller's, not
// pending, or expired. All three are one 404.
var errInvitationNotOpen = errors.New("no pending invitation with this id is addressed to you")

const invitationNotOpenMessage = "You have no pending invitation with that id. It may have expired, been revoked, or been answered; reload your invitations."

// AcceptMyInvitation joins the collective an invitation names. Accepting is
// consent: the collective's owners may then see the caller's profile.
// POST /users/me/invitations/{invitationID}/accept (AuthRequired).
//
// An owner's invitation admits the person whatever the collective's
// acceptance mode, curated and verified_only included.
func (h *Handler) AcceptMyInvitation(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	invitationID, err := uuid.Parse(chi.URLParam(r, "invitationID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid invitation ID")
		return
	}
	lookup := sqlc.GetMyPendingInvitationParams{ID: toPgUUID(invitationID), InviteeID: user.PgID()}
	invitation, err := h.queries.GetMyPendingInvitation(r.Context(), lookup)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, invitationNotOpenMessage)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read the invitation; nothing was joined. Retry the request.")
		return
	}

	var role string
	var affected []sqlc.PullRequestAttachment
	err = h.inCollectiveAuthorityTx(r.Context(), nil, user.PgID(), []pgtype.UUID{invitation.GroupID}, func(ctx context.Context, q Querier) error {
		if _, err := q.GetMyPendingInvitationForUpdate(ctx, sqlc.GetMyPendingInvitationForUpdateParams(lookup)); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errInvitationNotOpen
			}
			return err
		}
		existing, err := q.GetGroupMemberForShare(ctx, sqlc.GetGroupMemberForShareParams{GroupID: invitation.GroupID, UserID: user.PgID()})
		switch {
		case err == nil && consentedMembership(existing):
			// Already a member who consented: nothing new is joined.
			role = existing.Role
		case err != nil && !errors.Is(err, pgx.ErrNoRows):
			return err
		default:
			if role, err = q.AcceptInvitationMembership(ctx, sqlc.AcceptInvitationMembershipParams{GroupID: invitation.GroupID, UserID: user.PgID()}); err != nil {
				return err
			}
		}
		if err := q.DeleteInvitation(ctx, invitation.ID); err != nil {
			return err
		}
		if h.gh != nil {
			// A new member can change which attachment checks the collective
			// grants, so they are refreshed after commit, as for a join.
			if affected, err = q.ListAttachmentsForCollectiveGrants(ctx, invitation.GroupID); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, errInvitationNotOpen) {
		writeError(w, http.StatusNotFound, invitationNotOpenMessage)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not join the collective; nothing changed. Retry the request.")
		return
	}
	if len(affected) > 0 {
		ctx, cancel := attachmentWorkContext(r.Context())
		defer cancel()
		if err := h.refreshKnownAttachments(ctx, affected); err != nil {
			slog.Error("attachment refresh failed after an accepted invitation", "operation", "collective_grant_refresh", "stage", "post_commit", "consequence", "the membership is committed but an external check may be stale", "remediation", "retry the attachment refresh", "cause_type", fmt.Sprintf("%T", err))
		}
	}
	writeJSON(w, http.StatusOK, schema.VillageCollectiveStatusRoleResponse{Status: "joined", Role: schema.VillageGroupRole(role)})
}

// DeclineMyInvitation declines one of the caller's pending invitations. The
// owners are not told. POST /users/me/invitations/{invitationID}/decline
// (AuthRequired).
func (h *Handler) DeclineMyInvitation(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	invitationID, err := uuid.Parse(chi.URLParam(r, "invitationID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid invitation ID")
		return
	}
	declined, err := h.queries.DeclineMyInvitation(r.Context(), sqlc.DeclineMyInvitationParams{ID: toPgUUID(invitationID), InviteeID: user.PgID()})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not decline the invitation; retry the request")
		return
	}
	if declined == 0 {
		writeError(w, http.StatusNotFound, invitationNotOpenMessage)
		return
	}
	writeJSON(w, http.StatusOK, schema.VillageStatusResponse{Status: "declined"})
}

// villageUUID is a stored id in its wire form.
func villageUUID(id pgtype.UUID) schema.VillageUUID {
	return schema.VillageUUID(uuid.UUID(id.Bytes).String())
}
