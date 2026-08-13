package handler

import (
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
// the ABAC seam, URD R6.3). Every "not pullable" outcome is 404, never 403, so
// the surface never leaks the existence of a transcript the caller may not pull.

// pullListPageSize is the default page size for the pullable listing when the
// client does not specify one. maxPullListPageSize caps it.
const (
	pullListPageSize    = 50
	maxPullListPageSize = 200
)

// canPullTranscript is the pull-surface authorization policy and the future ABAC
// seam (URD R6.3 — replace this single function with a collective/ABAC engine).
//
// CANONICAL DOCS: the whole peasant↔village auth model — the canViewTranscript vs
// canPullTranscript ALLOWED/DENIED matrix, the deliberate divergences (public,
// collective-preview, and pending-share all DENY pull), and the 404-not-403
// anti-enumeration rule — lives in the peasant repo at docs/auth.md §4
// (peasant-labs/peasant). The pull-surface architecture (component map, staged
// flow, manifest) is in peasant docs/pull.md. Keep those canonical; this comment
// is the pointer.
//
// It MIRRORS canViewTranscript's logic (transcripts.go) with two DELIBERATE
// divergences, per the ratified divergence table:
//
//   - PUBLIC visibility is NOT pullable (canViewTranscript allows it). The MVP
//     pull policy is "own + group-shared only"; public discovery is a web concern.
//   - COLLECTIVE-OWNER PREVIEW is NOT pullable (canViewTranscript allows a
//     collective owner to preview private/pending submissions). Pull excludes it.
//
// It is also STRICTER on shares than canViewTranscript: canViewTranscript (via
// ListTranscriptShares) grants access for ANY share to a group the requester is
// in, including a 'pending' share. Pull requires the share STATUS to be
// 'approved' (the ratified divergence-table row "group-shared, acceptance
// pending/rejected => deny"), enforced via ListApprovedTranscriptShareGroups.
//
// RECONCILIATION (S2 precondition comment): the supervisor's note said to mirror
// canViewTranscript's shared branch exactly and claimed there is "no per-share
// approved/pending state". That premise is FACTUALLY INCORRECT on develop:
// transcript_shares.status (migration 005, default 'approved') is a real
// per-share lifecycle column, and the share flows DO set it to 'pending' (a
// curated/verified group's reviewer flips it to 'approved' — shares.sql
// UpdateShareStatus / ListPendingGroupShares). Because the premise is wrong but
// the ratified divergence table is explicit that a pending share must DENY pull,
// this policy honors the table's substantive intent: own + APPROVED-group-shared
// only. A member of a group whose share is still 'pending' therefore gets 404,
// which is the divergence the table requires (and a deliberate, documented
// divergence from canViewTranscript). Group acceptance MODES
// (open/verified_only/curated, groups.go:16-20) gate how a share REACHES
// 'approved'; they are orthogonal to this membership-of-an-approved-share check.
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
// (public/private/group) via mapDBVisibility — A-O1: the DB's 'shared' is the
// wire's VisibilityGroup. All three are extracted (not just 'shared') so the
// mapping has no bare string literals.
const (
	dbVisibilityPublic  = "public"
	dbVisibilityPrivate = "private"
	dbVisibilityShared  = "shared"
)

// mapDBVisibility maps the transcripts.visibility CHECK value (public/private/
// shared) to the wire schema.Visibility enum (public/private/group). A-O1: the
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
// it (rather than a raw cast) to keep the ratified typed-constructor intent
// intact, and fall back to the raw string only if validation ever rejects it
// (impossible for a DB UUID, defensive).
func wireTranscriptID(id pgtype.UUID) schema.TranscriptID {
	raw := uuid.UUID(id.Bytes).String()
	if tid, err := schema.NewTranscriptID(raw); err == nil {
		return tid
	}
	return schema.TranscriptID(raw)
}

// pullTranscriptInfo builds the wire metadata view for one transcript. It looks
// reads the served-blob hash from the loaded row (NULL => empty ContentHash =>
// client falls back to an unconditional GET), looks up the annotation count, and resolves the owner's account
// identity via the users table (GetUserByID).
func (h *Handler) pullTranscriptInfo(ctx context.Context, t sqlc.Transcript) schema.PullTranscriptInfo {
	owner, _ := h.queries.GetUserByID(ctx, t.OwnerID)
	annCount, _ := h.queries.CountTranscriptAnnotations(ctx, t.ID)

	info := schema.PullTranscriptInfo{
		TranscriptID:    wireTranscriptID(t.ID),
		LocalID:         t.LocalID,
		OwnerUserID:     uuid.UUID(t.OwnerID.Bytes).String(),
		OwnerUsername:   owner.GithubUsername,
		Title:           t.Title.String,
		Harness:         schema.Harness(t.ModelProvider),
		ProjectName:     t.ProjectName.String,
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
	return info
}

// ListPullableTranscripts lists the transcripts the requester may pull (own +
// approved group-shared; public and collective-preview excluded by
// canPullTranscript / the query predicate). Offset pagination (MVP).
// GET /api/v1/pull/transcripts?page=N&limit=M (AuthRequired)
func (h *Handler) ListPullableTranscripts(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
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
	rows, err := h.queries.ListPullableTranscripts(r.Context(), sqlc.ListPullableTranscriptsParams{
		UserID: user.PgID(),
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list pullable transcripts")
		return
	}

	total, err := h.queries.CountPullableTranscripts(r.Context(), user.PgID())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to count pullable transcripts")
		return
	}

	infos := make([]schema.PullTranscriptInfo, 0, len(rows))
	for _, row := range rows {
		infos = append(infos, pullInfoFromListRow(row))
	}

	writeJSON(w, http.StatusOK, schema.PullListResponse{
		Transcripts: infos,
		Page:        page,
		Limit:       limit,
		Total:       int(total),
	})
}

// pullInfoFromListRow builds the wire metadata view from a batched
// ListPullableTranscripts row. It mirrors pullTranscriptInfo's mapping exactly,
// but every field is already in-hand from the single list query (no per-row
// GetUserByID / GetTranscriptContentHash / CountTranscriptAnnotations).
func pullInfoFromListRow(row sqlc.ListPullableTranscriptsRow) schema.PullTranscriptInfo {
	info := schema.PullTranscriptInfo{
		TranscriptID:    wireTranscriptID(row.ID),
		LocalID:         row.LocalID,
		OwnerUserID:     uuid.UUID(row.OwnerID.Bytes).String(),
		OwnerUsername:   row.OwnerUsername,
		Title:           row.Title.String,
		Harness:         schema.Harness(row.ModelProvider),
		ProjectName:     row.ProjectName.String,
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
	writeJSON(w, http.StatusOK, h.pullTranscriptInfo(r.Context(), t))
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
	if result.Row.ContentHash.Valid {
		w.Header().Set("ETag", fmt.Sprintf("%q", result.Identity.Hash()))
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result.Plaintext)
}

// GetPullTranscriptAnnotations returns the authored annotations for a pullable
// transcript as PullAnnotation rows: the existing AnnotationSummary plus the
// village account identity (AuthorUserID/AuthorUsername) resolved via a users
// join on annotations.owner_id. The author identity is what lets the client
// foreign-mark pulled annotations and exclude its own during refresh.
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

	// Resolve author identity per owner_id, caching lookups so a transcript with
	// N annotations from M authors costs M user lookups, not N.
	type author struct {
		id       string
		username string
	}
	authorCache := map[pgtype.UUID]author{}
	resolveAuthor := func(ownerID pgtype.UUID) author {
		if a, ok := authorCache[ownerID]; ok {
			return a
		}
		u, _ := h.queries.GetUserByID(r.Context(), ownerID)
		a := author{
			id:       uuid.UUID(ownerID.Bytes).String(),
			username: u.GithubUsername,
		}
		authorCache[ownerID] = a
		return a
	}

	annotations := make([]schema.PullAnnotation, 0, len(rows))
	for _, row := range rows {
		a := resolveAuthor(row.OwnerID)
		annotations = append(annotations, schema.PullAnnotation{
			AnnotationSummary: annotationRowToSummary(row),
			AuthorUserID:      a.id,
			AuthorUsername:    a.username,
		})
	}

	// IP1 conformance: the pull-annotations response is a BARE JSON array of
	// PullAnnotation (the committed OpenAPI spec, pkg/schema/openapi/village.go),
	// NOT the web endpoint's {"annotations":[...]} wrapper. Emit the bare array.
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
