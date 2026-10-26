package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// anonymousAnnotatorName replaces a human annotator's name when the viewer may
// not see that person. It is the label the web client renders for an anonymous
// person, so a pulled or listed annotation reads the same way everywhere.
const anonymousAnnotatorName = "anon"

// publicOwners is the resolved answer for every person one response shows,
// keyed by the internal account id. An id that was not resolved reads as
// anonymous, so a missing row can never surface an identity.
type publicOwners map[pgtype.UUID]schema.VillagePublicOwner

// of returns the person as the viewer may see them.
func (p publicOwners) of(id pgtype.UUID) schema.VillagePublicOwner {
	if owner, ok := p[id]; ok {
		return owner
	}
	return schema.AnonymousVillageOwner()
}

// resolvePublicOwners is the one place Village decides how a person is shown to
// a viewer: their public profile, or anonymous. Every handler that sends an
// owner, member, contributor, or annotation author calls it, and the rule
// itself lives in one SQL statement (ListPublicOwnersForViewer) that a page
// reads once for all its people.
//
// A failed read is returned as an error, never as an anonymous or a profile
// guess: the caller refuses the response instead of serving one built on an
// unknown answer.
func (h *Handler) resolvePublicOwners(ctx context.Context, viewer *AuthUser, ids []pgtype.UUID) (publicOwners, error) {
	resolved := publicOwners{}
	unique := make([]pgtype.UUID, 0, len(ids))
	seen := make(map[pgtype.UUID]bool, len(ids))
	for _, id := range ids {
		if !id.Valid || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return resolved, nil
	}
	rows, err := h.queries.ListPublicOwnersForViewer(ctx, sqlc.ListPublicOwnersForViewerParams{
		ViewerID:  viewerID(viewer),
		PersonIds: unique,
	})
	if err != nil {
		return nil, fmt.Errorf("handler.resolvePublicOwners: the profile visibility of %d people could not be read; no response was built from a guessed identity; retry the request: %w", len(unique), err)
	}
	for _, row := range rows {
		resolved[row.ID] = publicOwnerFromRow(row)
	}
	return resolved, nil
}

// resolvePublicOwner resolves a single person.
func (h *Handler) resolvePublicOwner(ctx context.Context, viewer *AuthUser, id pgtype.UUID) (schema.VillagePublicOwner, error) {
	owners, err := h.resolvePublicOwners(ctx, viewer, []pgtype.UUID{id})
	if err != nil {
		return schema.VillagePublicOwner{}, err
	}
	return owners.of(id), nil
}

func publicOwnerFromRow(row sqlc.ListPublicOwnersForViewerRow) schema.VillagePublicOwner {
	if !row.ProfileVisible {
		return schema.AnonymousVillageOwner()
	}
	return publicProfile(row.ID, row.GithubUsername, row.DisplayName, row.AvatarUrl)
}

// publicProfile builds the profile shape from stored account columns. A row
// that cannot form a valid profile (no username) is shown as anonymous rather
// than emitted half-formed.
func publicProfile(id pgtype.UUID, username string, displayName, avatarURL pgtype.Text) schema.VillagePublicOwner {
	if !id.Valid || strings.TrimSpace(username) == "" {
		return schema.AnonymousVillageOwner()
	}
	return schema.NewVillagePublicProfile(
		schema.VillageUUID(uuid.UUID(id.Bytes).String()),
		username,
		pgTextPointer(displayName),
		pgTextPointer(avatarURL),
	)
}

// publicProfileOfUser is the profile shape of an account the viewer may
// already see in full (a discoverable person, or the viewer themselves). It
// narrows the stored row to the four public fields.
func publicProfileOfUser(user sqlc.User) schema.VillagePublicOwner {
	return publicProfile(user.ID, user.GithubUsername, user.DisplayName, user.AvatarUrl)
}

// withholdSessionRepository is withholdRepository for a grouped session row:
// every repository field of an anonymous owner's session becomes null.
func withholdSessionRepository(session *schema.VillageTranscript, owner schema.VillagePublicOwner) {
	if owner.IsProfile() {
		return
	}
	session.GitBranch, session.GitRemote, session.ProjectHash, session.ProjectName = nil, nil, nil, nil
	session.ProjectDisplayName, session.ProjectNameSource, session.ProjectRemoteLabel = nil, nil, nil
}

// withholdAnnotationRepository leaves an annotation's target project hash and
// target file path absent when the annotated transcript's owner is anonymous
// to the viewer: both identify that owner's repository, and the annotations
// response carries no owner to pair them with.
func withholdAnnotationRepository(summary *schema.AnnotationSummary, transcriptOwner schema.VillagePublicOwner) {
	if transcriptOwner.IsProfile() {
		return
	}
	summary.TargetProjectHash = nil
	summary.TargetFilePath = nil
}

// errContentRepositoryUnreadable is content Village cannot read as a transcript
// session, so it cannot withhold the repository identity of an anonymous owner
// and does not serve it. It is a fact about the stored content, so retrying
// does not change it.
var errContentRepositoryUnreadable = errors.New("the stored transcript content is not a session Village can read, so the repository identity of its anonymous owner cannot be withheld and the content is not served; retrying will not change this; the owner can republish the transcript")

// writeContentRepositoryUnreadable answers a content route whose stored content
// cannot be read to withhold an anonymous owner's repository: 422 with a
// stable code, because the condition is permanent for this content.
func writeContentRepositoryUnreadable(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
		"code":  "content_unreadable_for_anonymous_owner",
		"error": errContentRepositoryUnreadable.Error(),
	})
}

// withheldSessionDetailFields are the sessionDetail fields that identify an
// owner's repository: the project, the working directory, and the git branch
// and remote. Every child session's project is withheld too. The harness
// metadata records (nativeMetadata, retainedUnknown) are withheld with them:
// they carry the harness's own fields, which can include the working
// directory and branch. Readers treat both as optional.
var withheldSessionDetailFields = []string{"project", "workingDirectory", "gitBranch", "gitRemote", "nativeMetadata", "retainedUnknown"}

// earlierHistoryWithheldFields are the harness metadata records an
// earlierHistory section carries, withheld for the same reason.
var earlierHistoryWithheldFields = []string{"nativeMetadata", "retainedUnknown"}

// withholdContentRepository removes the repository identity from served
// transcript content whose owner is anonymous to the viewer. It accepts the
// durable envelope ({contractVersion, kind, sessionDetail}) and, for stored
// blobs published before the envelope, a bare session detail. Every other
// field is passed through as the stored bytes encode it, and content that
// names no repository is returned byte-for-byte. Text inside the conversation
// is content and is not covered.
func withholdContentRepository(content []byte) ([]byte, error) {
	fail := func(err error) ([]byte, error) {
		return nil, fmt.Errorf("%w: %v", errContentRepositoryUnreadable, err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(content, &top); err != nil {
		return fail(err)
	}
	if raw, ok := top["sessionDetail"]; ok {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return content, nil
		}
		var detail map[string]json.RawMessage
		if err := json.Unmarshal(raw, &detail); err != nil {
			return fail(err)
		}
		changed, err := withholdSessionDetailRepository(detail)
		if err != nil {
			return fail(err)
		}
		if !changed {
			return content, nil
		}
		encoded, err := json.Marshal(detail)
		if err != nil {
			return fail(err)
		}
		top["sessionDetail"] = encoded
	} else {
		changed, err := withholdSessionDetailRepository(top)
		if err != nil {
			return fail(err)
		}
		if !changed {
			return content, nil
		}
	}
	out, err := json.Marshal(top)
	if err != nil {
		return fail(err)
	}
	return out, nil
}

// withholdSessionDetailRepository deletes the repository fields from one
// decoded session detail and reports whether it removed any.
func withholdSessionDetailRepository(detail map[string]json.RawMessage) (bool, error) {
	changed := false
	for _, field := range withheldSessionDetailFields {
		if _, ok := detail[field]; ok {
			delete(detail, field)
			changed = true
		}
	}
	// Earlier history sections carry the same harness metadata records.
	if history, ok := detail["earlierHistory"]; ok && !bytes.Equal(bytes.TrimSpace(history), []byte("null")) {
		var sections []map[string]json.RawMessage
		if err := json.Unmarshal(history, &sections); err != nil {
			return false, err
		}
		historyChanged := false
		for _, section := range sections {
			for _, field := range earlierHistoryWithheldFields {
				if _, ok := section[field]; ok {
					delete(section, field)
					historyChanged = true
				}
			}
		}
		if historyChanged {
			encoded, err := json.Marshal(sections)
			if err != nil {
				return false, err
			}
			detail["earlierHistory"] = encoded
			changed = true
		}
	}
	children, ok := detail["childSessions"]
	if !ok || bytes.Equal(bytes.TrimSpace(children), []byte("null")) {
		return changed, nil
	}
	var list []map[string]json.RawMessage
	if err := json.Unmarshal(children, &list); err != nil {
		return false, err
	}
	childChanged := false
	for _, child := range list {
		if _, ok := child["project"]; ok {
			delete(child, "project")
			childChanged = true
		}
	}
	if childChanged {
		encoded, err := json.Marshal(list)
		if err != nil {
			return false, err
		}
		detail["childSessions"] = encoded
	}
	return changed || childChanged, nil
}

// annotatorNameFor applies the viewer's answer to a human annotation's
// annotator name. An agent or rule annotator names a tool, not a person, and
// is left as stored.
func annotatorNameFor(summary schema.AnnotationSummary, author schema.VillagePublicOwner) string {
	if summary.AnnotatorKind == schema.AnnotatorHuman && !author.IsProfile() {
		return anonymousAnnotatorName
	}
	return summary.AnnotatorName
}

// ownerFilter is the person and repository filters of a transcript list,
// narrowed to the accounts the viewer may see as a profile. Active is false
// when the request names none of them. An active filter with no accounts
// matches nothing: a list filtered by an owner the viewer may not see is the
// same empty answer as a filter by a name nobody holds, so a filter cannot tie
// anonymous transcripts to a person, an organization, or a repository.
type ownerFilter struct {
	Active   bool
	OwnerIDs []pgtype.UUID
}

// listFilterRequest names the list filters that select by a person or by a
// repository. Project and Repository are the ILIKE patterns the list applies;
// empty means the filter is absent.
type listFilterRequest struct {
	OwnerName   string
	Org         string
	Project     string
	Repository  string
	ProjectHash string
}

// resolveOwnerFilter turns the person and repository filters into the set of
// owners whose transcripts may match for this viewer: owners each named
// filter selects, intersected, then kept only when the viewer may see them as
// a profile. The list still applies its own filter conditions; this set is
// added to them.
func (h *Handler) resolveOwnerFilter(ctx context.Context, viewer *AuthUser, request listFilterRequest) (ownerFilter, error) {
	filter := ownerFilter{OwnerIDs: []pgtype.UUID{}}
	repositoryFiltered := request.Project != "" || request.Repository != "" || request.ProjectHash != ""
	if request.OwnerName == "" && request.Org == "" && !repositoryFiltered {
		return filter, nil
	}
	filter.Active = true
	var candidates []pgtype.UUID
	narrow := func(ids []pgtype.UUID) {
		if candidates == nil {
			candidates = append([]pgtype.UUID{}, ids...)
			return
		}
		keep := make(map[pgtype.UUID]bool, len(ids))
		for _, id := range ids {
			keep[id] = true
		}
		kept := []pgtype.UUID{}
		for _, id := range candidates {
			if keep[id] {
				kept = append(kept, id)
			}
		}
		candidates = kept
	}
	if request.OwnerName != "" {
		user, err := h.queries.GetUserByUsername(ctx, request.OwnerName)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return filter, nil
			}
			return filter, fmt.Errorf("handler.resolveOwnerFilter: the owner filter could not be read; no list was returned; retry the request: %w", err)
		}
		// The filter has always matched the handle exactly; the lookup is
		// case-insensitive, so keep the exact comparison.
		if user.GithubUsername != request.OwnerName {
			return filter, nil
		}
		narrow([]pgtype.UUID{user.ID})
	}
	if request.Org != "" {
		members, err := h.queries.ListUserIDsWithVisibleOrg(ctx, request.Org)
		if err != nil {
			return filter, fmt.Errorf("handler.resolveOwnerFilter: the organization filter could not be read; no list was returned; retry the request: %w", err)
		}
		narrow(members)
	}
	if repositoryFiltered {
		owners, err := h.queries.ListOwnersMatchingRepositoryFilter(ctx, sqlc.ListOwnersMatchingRepositoryFilterParams{
			ProjectPattern: request.Project, RepositoryPattern: request.Repository, ProjectHash: request.ProjectHash,
			ViewerID: viewerID(viewer),
		})
		if err != nil {
			return filter, fmt.Errorf("handler.resolveOwnerFilter: the repository filter could not be read; no list was returned; retry the request: %w", err)
		}
		narrow(owners)
	}
	owners, err := h.resolvePublicOwners(ctx, viewer, candidates)
	if err != nil {
		return filter, err
	}
	for _, id := range candidates {
		if owners.of(id).IsProfile() {
			filter.OwnerIDs = append(filter.OwnerIDs, id)
		}
	}
	return filter, nil
}
