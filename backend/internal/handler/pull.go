package handler

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"

	"github.com/peasant-labs/schema"
)

// Pull surface: a distinct, AuthRequired set of routes for the peasant
// CLI to retrieve transcripts it OWNS or that are group-shared with it. It is
// deliberately SEPARATE from the web read endpoints (canViewTranscript) so the
// web viewing policy stays untouched and the pull policy can diverge (and become
// a future ABAC seam). Every "not pullable" outcome is 404, never 403, so
// the surface never leaks the existence of a transcript the caller may not pull.

// pullListPageSize is the default page size for the pullable listing when the
// client does not specify one. maxPullListPageSize caps it.
const (
	pullListPageSize    = 50
	maxPullListPageSize = 200
)

// canPullTranscript is the pull-surface authorization policy and future ABAC
// seam. Owners may pull their own transcripts. Other callers may pull only when
// the transcript has an approved share to a group they belong to. Public
// visibility, collective-owner preview, and pending or rejected shares do not
// grant pull access even when the web viewing policy would allow a preview.
// Group acceptance modes determine how a share reaches approved status; they do
// not replace the approved-share membership check here.
//
// AuthRequired runs upstream, so user is always non-nil here; the nil guard is
// defensive only.
func (h *Handler) canPullTranscript(ctx context.Context, user *AuthUser, t sqlc.Transcript) bool {
	if user == nil {
		return false
	}
	// Owner can always pull their own transcript.
	if t.OwnerID == user.PgID() {
		return true
	}
	// Group-shared with an APPROVED share to a group the requester belongs to.
	if t.Visibility == dbVisibilityShared {
		groupIDs, err := h.queries.ListApprovedTranscriptShareGroups(ctx, t.ID)
		if err != nil {
			return false
		}
		for _, gid := range groupIDs {
			if _, err := h.queries.GetGroupMember(ctx, sqlc.GetGroupMemberParams{
				GroupID: gid,
				UserID:  user.PgID(),
			}); err == nil {
				return true
			}
		}
	}
	// PUBLIC and collective-owner preview are DELIBERATELY excluded (divergence).
	return false
}

// dbVisibility* are the transcripts.visibility CHECK values (the DB enum is
// public/private/shared). They map to the wire schema.Visibility enum
// (public/private/group) via mapDBVisibility; the DB's 'shared' is the
// wire's VisibilityGroup. All three are extracted (not just 'shared') so the
// mapping has no bare string literals.
const (
	dbVisibilityPublic  = "public"
	dbVisibilityPrivate = "private"
	dbVisibilityShared  = "shared"
)

// mapDBVisibility maps the transcripts.visibility CHECK value (public/private/
// shared) to the wire schema.Visibility enum (public/private/group). The
// DB's 'shared' is the wire's VisibilityGroup. An unknown value falls back to
// VisibilityPrivate (the most restrictive) rather than emitting an invalid enum.
func mapDBVisibility(dbVis string) schema.Visibility {
	switch dbVis {
	case dbVisibilityPublic:
		return schema.VisibilityPublic
	case dbVisibilityShared:
		return schema.VisibilityGroup
	case dbVisibilityPrivate:
		return schema.VisibilityPrivate
	default:
		return schema.VisibilityPrivate
	}
}

// wireTranscriptID converts a trusted DB UUID into the wire schema.TranscriptID.
// The id is a canonical UUID produced by Postgres (transcripts.id), so the
// validating NewTranscriptID constructor cannot fail here; we still route through
// it (rather than a raw cast) to preserve the validating constructor boundary
// intact, and fall back to the raw string only if validation ever rejects it
// (impossible for a DB UUID, defensive).
func wireTranscriptID(id pgtype.UUID) schema.TranscriptID {
	raw := uuid.UUID(id.Bytes).String()
	if tid, err := schema.NewTranscriptID(raw); err == nil {
		return tid
	}
	return schema.TranscriptID(raw)
}

// pullTranscriptInfo builds the wire metadata view for one transcript. It
// reads the served-blob hash from the loaded row (NULL => empty ContentHash =>
// client falls back to an unconditional GET), looks up the annotation count,
// and carries the owner as the caller may see them (resolvePublicOwners).
func (h *Handler) pullTranscriptInfo(ctx context.Context, t sqlc.Transcript, owner schema.VillagePublicOwner) schema.PullTranscriptInfo {
	annCount, _ := h.queries.CountTranscriptAnnotations(ctx, t.ID)

	info := schema.PullTranscriptInfo{
		TranscriptID:    wireTranscriptID(t.ID),
		LocalID:         t.LocalID,
		Owner:           owner,
		Title:           t.Title.String,
		Harness:         schema.Harness(t.ModelProvider),
		ProjectName:     pullProjectName(t.ProjectName),
		Visibility:      mapDBVisibility(t.Visibility),
		License:         wireLicense(t.LicenseID),
		ContractVersion: schema.ContractVersion(t.SchemaVersion),
		PublishedAt:     t.PublishedAt.Time.UnixMilli(),
		UpdatedAt:       t.UpdatedAt.Time.UnixMilli(),
		AnnotationCount: int(annCount),
	}
	if t.ContentHash.Valid {
		info.ContentHash = t.ContentHash.String
	}
	if !owner.IsProfile() {
		// The project name describes an anonymous owner's repository.
		info.ProjectName = nil
	}
	return info
}

// ListPullableTranscripts lists the transcripts the requester may pull (own +
// approved group-shared; public and collective-preview excluded by
// canPullTranscript / the query predicate). Offset pagination (MVP).
//
// The optional collectives filter narrows the list to transcripts with an
// approved share into any listed collective the requester belongs to. It is a
// comma-separated list, the same shape as the tags filter on the transcript
// lists. The owner clause is deliberately absent from the filtered predicate, so
// a transcript the requester owns but never shared with a listed collective is
// NOT returned. A collective the requester is not in contributes no rows, so the
// filter never becomes an existence or membership oracle.
//
// GET /api/v1/pull/transcripts?page=N&limit=M&collectives=<uuid>[,<uuid>...] (AuthRequired)
func (h *Handler) ListPullableTranscripts(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	collectives, err := parseCollectiveFilter(r.URL.Query().Get("collectives"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	page := parsePositiveInt(r.URL.Query().Get("page"), 1)
	limit := parsePositiveInt(r.URL.Query().Get("limit"), pullListPageSize)
	if limit > maxPullListPageSize {
		limit = maxPullListPageSize
	}
	offset := (page - 1) * limit

	// ONE query for the page (every column pullTranscriptInfo needs, incl.
	// content_hash, owner username, and a per-row annotation count) + ONE COUNT —
	// NOT the old 2+4N round-trips (per-row GetTranscriptByID +
	// GetTranscriptContentHash + CountTranscriptAnnotations + GetUserByID).
	//
	// The collectives filter reads through its own dedicated pair. The unfiltered
	// predicate is "owner OR approved share"; reusing it with a nullable group
	// condition would leak the requester's own unshared transcripts into a
	// filtered page.
	var (
		infos []schema.PullTranscriptInfo
		total int64
	)
	if len(collectives) == 0 {
		rows, listErr := h.queries.ListPullableTranscripts(r.Context(), sqlc.ListPullableTranscriptsParams{
			UserID: user.PgID(),
			Limit:  int32(limit),
			Offset: int32(offset),
		})
		if listErr != nil {
			writeError(w, http.StatusInternalServerError, "Failed to list pullable transcripts")
			return
		}
		total, listErr = h.queries.CountPullableTranscripts(r.Context(), user.PgID())
		if listErr != nil {
			writeError(w, http.StatusInternalServerError, "Failed to count pullable transcripts")
			return
		}
		owners, ownerErr := h.resolvePublicOwners(r.Context(), user, pullRowOwners(rows))
		if ownerErr != nil {
			writeError(w, http.StatusInternalServerError, "Failed to resolve the owners of pullable transcripts")
			return
		}
		infos = make([]schema.PullTranscriptInfo, 0, len(rows))
		for _, row := range rows {
			infos = append(infos, pullInfoFromListRow(row, owners.of(row.OwnerID)))
		}
	} else {
		rows, listErr := h.queries.ListPullableTranscriptsInCollective(r.Context(), sqlc.ListPullableTranscriptsInCollectiveParams{
			UserID:      user.PgID(),
			Collectives: collectives,
			PageLimit:   int32(limit),
			PageOffset:  int32(offset),
		})
		if listErr != nil {
			writeError(w, http.StatusInternalServerError, "Failed to list pullable transcripts")
			return
		}
		total, listErr = h.queries.CountPullableTranscriptsInCollective(r.Context(), sqlc.CountPullableTranscriptsInCollectiveParams{
			UserID:      user.PgID(),
			Collectives: collectives,
		})
		if listErr != nil {
			writeError(w, http.StatusInternalServerError, "Failed to count pullable transcripts")
			return
		}
		converted := make([]sqlc.ListPullableTranscriptsRow, 0, len(rows))
		for _, row := range rows {
			// The filtered row carries the same columns in the same order as the
			// unfiltered one, so one projector serves both. A field drift between
			// the two queries breaks this conversion at compile time.
			converted = append(converted, sqlc.ListPullableTranscriptsRow(row))
		}
		owners, ownerErr := h.resolvePublicOwners(r.Context(), user, pullRowOwners(converted))
		if ownerErr != nil {
			writeError(w, http.StatusInternalServerError, "Failed to resolve the owners of pullable transcripts")
			return
		}
		infos = make([]schema.PullTranscriptInfo, 0, len(converted))
		for _, row := range converted {
			infos = append(infos, pullInfoFromListRow(row, owners.of(row.OwnerID)))
		}
	}

	writeJSON(w, http.StatusOK, schema.PullListResponse{
		Transcripts: infos,
		Page:        page,
		Limit:       limit,
		Total:       int(total),
	})
}

// parseCollectiveFilter parses the optional collectives query value: a
// comma-separated list of collective identifiers. An absent value, or one with
// only empty elements, returns nil (the unfiltered list). Duplicates collapse. A
// value that is not a collective identifier is refused here, at the boundary, so
// the query layer never sees an id it cannot interpret.
func parseCollectiveFilter(raw string) ([]pgtype.UUID, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	ids := make([]pgtype.UUID, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		id, err := uuid.Parse(value)
		if err != nil {
			return nil, fmt.Errorf("the collectives filter lists %q, which is not a collective identifier; send comma-separated collective UUIDs, for example ?collectives=99d59925-36bc-424c-a789-8be54d9702ba", value)
		}
		canonical := id.String()
		if _, duplicate := seen[canonical]; duplicate {
			continue
		}
		seen[canonical] = struct{}{}
		ids = append(ids, toPgUUID(id))
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return ids, nil
}

// pullProjectName is the envelope's optional project name: absent when none was
// recorded, as the earlier omitempty string was.
func pullProjectName(name pgtype.Text) *string {
	if !name.Valid || name.String == "" {
		return nil
	}
	value := name.String
	return &value
}

// pullRowOwners lists the owner of every row on a pull page, for one batched
// resolvePublicOwners read.
func pullRowOwners(rows []sqlc.ListPullableTranscriptsRow) []pgtype.UUID {
	ids := make([]pgtype.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.OwnerID)
	}
	return ids
}

// pullInfoFromListRow builds the wire metadata view from a batched
// ListPullableTranscripts row. It mirrors pullTranscriptInfo's mapping exactly,
// but every field is already in-hand from the single list query (no per-row
// GetTranscriptContentHash / CountTranscriptAnnotations), and the owner comes
// from one batched resolvePublicOwners read for the page.
func pullInfoFromListRow(row sqlc.ListPullableTranscriptsRow, owner schema.VillagePublicOwner) schema.PullTranscriptInfo {
	info := schema.PullTranscriptInfo{
		TranscriptID:    wireTranscriptID(row.ID),
		LocalID:         row.LocalID,
		Owner:           owner,
		Title:           row.Title.String,
		Harness:         schema.Harness(row.ModelProvider),
		ProjectName:     pullProjectName(row.ProjectName),
		Visibility:      mapDBVisibility(row.Visibility),
		License:         wireLicense(row.LicenseID),
		ContractVersion: schema.ContractVersion(row.SchemaVersion),
		PublishedAt:     row.PublishedAt.Time.UnixMilli(),
		UpdatedAt:       row.UpdatedAt.Time.UnixMilli(),
		AnnotationCount: int(row.AnnotationCount),
	}
	if row.ContentHash.Valid {
		info.ContentHash = row.ContentHash.String
	}
	if !owner.IsProfile() {
		// The project name describes an anonymous owner's repository.
		info.ProjectName = nil
	}
	return info
}

// GetPullTranscript returns one transcript's pull metadata (incl. content_hash).
// 404 (not 403) when the transcript does not exist OR is not pullable.
// GET /api/v1/pull/transcripts/{id} (AuthRequired)
func (h *Handler) GetPullTranscript(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	t, ok := h.lookupPullable(w, r, user)
	if !ok {
		return
	}
	owner, err := h.resolvePublicOwner(r.Context(), user, t.OwnerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to resolve the transcript owner")
		return
	}
	writeJSON(w, http.StatusOK, h.pullTranscriptInfo(r.Context(), t, owner))
}

// GetPullTranscriptContent authenticates and decrypts the stored ciphertext,
// then serves the original published plaintext with a conditional-GET ETag
// derived from the SERVER-COMPUTED plaintext content_hash:
//
//   - ETag: "<content_hash>" when the hash is present.
//   - If-None-Match matching that ETag => 304 Not Modified (no body).
//   - NULL hash => NO ETag header => the client falls back to an unconditional
//     GET + local hash compare.
//
// The body is the authenticated plaintext that peasant published, not the
// display-migrated web payload. The client decodes that TranscriptContent
// envelope locally; ciphertext and wrapped-key material never cross this route.
// GET /api/v1/pull/transcripts/{id}/content (AuthRequired)
func (h *Handler) GetPullTranscriptContent(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	t, ok := h.lookupPullable(w, r, user)
	if !ok {
		return
	}

	result, err := h.readEncryptedTranscript(r.Context(), t, r.Header.Get("If-None-Match"), func(fresh sqlc.Transcript) bool {
		return h.canPullTranscript(r.Context(), user, fresh)
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if result.NotModified {
		w.Header().Set("ETag", fmt.Sprintf("%q", result.Identity.Hash()))
		w.WriteHeader(http.StatusNotModified)
		return
	}
	// The envelope carries no owner, so the repository identity of an owner who
	// is anonymous to the caller is left out of it here, as on the web content
	// route.
	owner, err := h.resolvePublicOwner(r.Context(), user, t.OwnerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to resolve the transcript owner; no content was served")
		return
	}
	body := result.Plaintext
	withheld := false
	if !owner.IsProfile() {
		trimmed, err := withholdContentRepository(body)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		withheld = !bytes.Equal(trimmed, body)
		body = trimmed
	}
	// The ETag names the stored blob's hash. Bytes with a field withheld are
	// not that blob, so they carry no ETag and the client compares locally.
	if result.Row.ContentHash.Valid && !withheld {
		w.Header().Set("ETag", fmt.Sprintf("%q", result.Identity.Hash()))
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// GetPullTranscriptAnnotations returns the authored annotations for a pullable
// transcript as PullAnnotation rows: the existing AnnotationSummary plus the
// author as the caller may see them. The caller's own rows always carry the
// caller's profile, which is what lets the client exclude its own during
// refresh; an anonymous author is foreign.
// GET /api/v1/pull/transcripts/{id}/annotations (AuthRequired)
func (h *Handler) GetPullTranscriptAnnotations(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	t, ok := h.lookupPullable(w, r, user)
	if !ok {
		return
	}

	rows, err := h.queries.ListAnnotationsByTranscriptID(r.Context(), t.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to fetch annotations")
		return
	}

	// Every author as the caller may see them, in one read. A human annotator's
	// name is the person's handle, so it follows the same answer.
	authorIDs := make([]pgtype.UUID, 0, len(rows)+1)
	authorIDs = append(authorIDs, t.OwnerID)
	for _, row := range rows {
		authorIDs = append(authorIDs, row.OwnerID)
	}
	authors, err := h.resolvePublicOwners(r.Context(), user, authorIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to resolve annotation authors")
		return
	}

	annotations := make([]schema.PullAnnotation, 0, len(rows))
	for _, row := range rows {
		author := authors.of(row.OwnerID)
		summary := annotationRowToSummary(row)
		summary.AnnotatorName = annotatorNameFor(summary, author)
		withholdAnnotationRepository(&summary, authors.of(t.OwnerID))
		annotations = append(annotations, schema.PullAnnotation{
			AnnotationSummary: summary,
			Author:            author,
		})
	}

	// The generated Village API contract defines this response as a bare
	// PullAnnotation array, unlike the web endpoint's {"annotations":[...]} wrapper.
	writeJSON(w, http.StatusOK, annotations)
}

// lookupPullable parses the {id} param, fetches the transcript, and enforces
// canPullTranscript. It writes the appropriate response (400 invalid id; 404 for
// both "not found" and "not pullable" so existence never leaks) and returns
// ok=false when the caller should stop. AuthRequired runs upstream; user is the
// authenticated caller.
func (h *Handler) lookupPullable(w http.ResponseWriter, r *http.Request, user *AuthUser) (sqlc.Transcript, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid transcript ID")
		return sqlc.Transcript{}, false
	}
	t, err := h.queries.GetTranscriptByID(r.Context(), toPgUUID(id))
	if err != nil {
		writeError(w, http.StatusNotFound, "Transcript not found")
		return sqlc.Transcript{}, false
	}
	if !h.canPullTranscript(r.Context(), user, t) {
		// 404, NOT 403: do not reveal that a transcript the caller cannot pull
		// exists (public-but-unshared, pending share, collective-preview, etc.).
		writeError(w, http.StatusNotFound, "Transcript not found")
		return sqlc.Transcript{}, false
	}
	return t, true
}

// ifNoneMatchMatches reports whether the client's If-None-Match header value
// designates the served-blob hash, tolerating the three forms a well-behaved or
// slightly-off client may send:
//
//   - the QUOTED ETag the server emits verbatim:   "<hash>"
//   - a WEAK validator prefix on that ETag:        W/"<hash>"
//   - the RAW served-blob hash (the documented key): <hash>
//
// Per RFC 7232 the canonical contract is "echo the ETag header verbatim"; the
// raw-hash tolerance exists so the documented served-blob-hash key still fires
// the 304 fast path (the conditional-GET optimization is otherwise silently
// defeated). An empty header never matches.
func ifNoneMatchMatches(ifNoneMatch, hash string) bool {
	if ifNoneMatch == "" || hash == "" {
		return false
	}
	v := strings.TrimSpace(ifNoneMatch)
	v = strings.TrimPrefix(v, "W/") // weak validator prefix
	v = strings.TrimSpace(v)
	v = strings.Trim(v, `"`) // surrounding quotes
	return v == hash
}

// parsePositiveInt parses a query-param int, returning def for empty/invalid/
// non-positive input (so page/limit always stay >= 1).
func parsePositiveInt(raw string, def int) int {
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return def
	}
	return n
}
