package handler

// Project identity: the one place the village decides what a project is CALLED.
//
// A project's identity is its hash, never its name. Names arrive from a harness,
// may be withheld for privacy, and may be corrected by their owner afterwards, so
// three different names can describe one project. Every surface therefore resolves
// the same (owner_id, project_hash) pair through internal/projectname and renders
// the single answer that comes back, and every correction is keyed on the hash.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/projectname"
)

// overrideTargetKind and overrideField are the two closed menus of
// owner_overrides. The TABLE reserves the wider menus in its CHECK constraints so
// that implementing a further correctable field later is a code change rather than
// a migration; these Go types are what narrow the reserved menus to the pairs the
// application actually implements today. Deliberately not a second database
// CHECK — one closed set, in one place, that the compiler can see.
type overrideTargetKind string

type overrideField string

const (
	// overrideTargetProject corrects a project, keyed by its 64-hex hash.
	overrideTargetProject overrideTargetKind = "project"
	// overrideFieldDisplayName is the name a surface renders for that project.
	overrideFieldDisplayName overrideField = "display_name"
)

// writableOverridePairs is the complete set of (target_kind, field) pairs the
// application may write. Anything absent is reserved-but-unimplemented and is
// refused before it reaches the database.
var writableOverridePairs = map[overrideTargetKind]map[overrideField]bool{
	overrideTargetProject: {overrideFieldDisplayName: true},
}

// projectDisplayNameMaxLength is the longest correction an owner may store. The
// column allows 4096 for future fields; a display name that no surface can render
// is not a useful correction, so this route is stricter than the column.
const projectDisplayNameMaxLength = 255

// projectHashPattern is the identity shape every project-keyed route accepts:
// exactly 64 LOWERCASE hex digits. Uppercase is refused rather than folded,
// because the stored keys are lowercase and silently accepting another casing
// would make one project addressable under two keys.
var projectHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// errOverridePairNotWritable reports a (kind, field) pair that the table reserves
// but the application does not implement.
var errOverridePairNotWritable = errors.New("owner-override pair is reserved but not implemented")

// validateOverridePair refuses any pair outside the writable set.
func validateOverridePair(kind overrideTargetKind, field overrideField) error {
	if writableOverridePairs[kind][field] {
		return nil
	}
	return fmt.Errorf(
		"%w: the correction (%s, %s) was rejected because only owner-chosen project display names are implemented in this "+
			"version of Village; the owner_overrides menus reserve the other pairs for later fields but no route writes them yet. "+
			"Rejected at the owner-correction trust boundary before any correction was stored, so nothing changed. "+
			"Send target kind %q with field %q, or wait for the release that implements the pair you want",
		errOverridePairNotWritable, kind, field, overrideTargetProject, overrideFieldDisplayName)
}

// validateOverrideTargetKey checks that the key matches the SHAPE the kind
// requires. target_key is untyped TEXT in the database on purpose (a project is
// keyed by a hash, a transcript would be keyed by a UUID), so this application-layer
// check is the only thing standing between a malformed key and a stored row that no
// read will ever match again.
func validateOverrideTargetKey(kind overrideTargetKind, key string) error {
	switch kind {
	case overrideTargetProject:
		if projectHashPattern.MatchString(key) {
			return nil
		}
		return fmt.Errorf(
			"the project key %q is not a project hash: a project is identified by exactly 64 lowercase hexadecimal "+
				"characters, and this value is not. Rejected in the project-correction route before the correction was "+
				"stored, so nothing was written and no existing correction changed. Copy the project_hash exactly as it "+
				"appears on a transcript, in lowercase", key)
	default:
		return validateOverridePair(kind, overrideFieldDisplayName)
	}
}

// projectIdentityKey is one resolvable project: an owner and the hash their
// transcripts group by.
type projectIdentityKey struct {
	OwnerID     pgtype.UUID
	ProjectHash string
}

// resolveProjectIdentities answers, for every requested pair, the one display name
// that surface should render.
//
// It issues exactly ONE database statement no matter how many pairs are asked for,
// because its callers are list responses: a page of transcripts spanning many
// owners must not turn into a query per row. A pair with no evidence still gets an
// answer — the resolver's last-resort synthesis from the hash — so a caller never
// has to invent a fallback of its own.
func (h *Handler) resolveProjectIdentities(ctx context.Context, keys []projectIdentityKey) map[projectIdentityKey]projectname.Resolved {
	resolved := map[projectIdentityKey]projectname.Resolved{}
	if len(keys) == 0 {
		return resolved
	}

	ownerSeen := map[pgtype.UUID]bool{}
	hashSeen := map[string]bool{}
	var ownerIDs []pgtype.UUID
	var hashes []string
	for _, k := range keys {
		if !ownerSeen[k.OwnerID] {
			ownerSeen[k.OwnerID] = true
			ownerIDs = append(ownerIDs, k.OwnerID)
		}
		if k.ProjectHash != "" && !hashSeen[k.ProjectHash] {
			hashSeen[k.ProjectHash] = true
			hashes = append(hashes, k.ProjectHash)
		}
	}

	if len(hashes) > 0 {
		rows, err := h.queries.ListOwnerProjectIdentities(ctx, sqlc.ListOwnerProjectIdentitiesParams{
			OwnerIds:      ownerIDs,
			ProjectHashes: hashes,
		})
		// A failed identity read degrades to the hash-derived label below rather
		// than failing the whole response: the resolved fields are a rendering
		// aid layered over payloads that already carry the raw project_hash, and
		// losing a nicer name is not a reason to withhold the transcripts.
		if err == nil {
			for _, row := range rows {
				key := projectIdentityKey{OwnerID: row.OwnerID, ProjectHash: row.ProjectHash}
				resolved[key] = h.projectNames.Resolve(evidenceFromIdentityRow(row))
			}
		}
	}

	for _, k := range keys {
		if _, ok := resolved[k]; !ok {
			resolved[k] = h.projectNames.Resolve(projectname.Evidence{ProjectHash: k.ProjectHash})
		}
	}
	return resolved
}

// evidenceFromIdentityRow turns one aggregated database row into the resolver's
// Evidence. The query returns the owner's project names as ONE array ordered by
// the deterministic pick (published_at DESC, then id); splitting that array into a
// consented name and a privacy label happens HERE, through
// projectname.IsPrivacyLabel, so the rule that tells those apart exists once in Go
// and is never restated as a SQL regex.
func evidenceFromIdentityRow(row sqlc.ListOwnerProjectIdentitiesRow) projectname.Evidence {
	evidence := projectname.Evidence{
		ProjectHash:  row.ProjectHash,
		OverrideName: row.OverrideName,
		GitRemote:    row.GitRemote,
	}
	for _, name := range row.ProjectNames {
		if projectname.IsPrivacyLabel(name) {
			if evidence.PrivacyLabel == "" {
				evidence.PrivacyLabel = name
			}
			continue
		}
		if evidence.ConsentedName == "" {
			evidence.ConsentedName = name
		}
	}
	return evidence
}

// SetProjectDisplayName stores the owner's chosen name for one of their projects.
//
// The route is keyed on the project HASH. The endpoint it replaces was keyed on the
// stored project_name, which meant a caller holding a project's RENDERED name — the
// only name any surface shows — matched no rows the moment that rendered name
// differed from the raw stored one, and the owner was told their own project did
// not exist. The hash is the identity, so the hash is the key.
//
// transcripts.project_name is never touched: the transcript keeps the exact bytes
// its harness reported, and the owner's preferred rendering is stored beside it.
// PATCH /api/v1/users/me/projects/{projectHash} (AuthRequired)
func (h *Handler) SetProjectDisplayName(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	projectHash := chi.URLParam(r, "projectHash")

	if err := validateOverridePair(overrideTargetProject, overrideFieldDisplayName); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateOverrideTargetKey(overrideTargetProject, projectHash); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var body struct {
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest,
			"the request body is not the JSON object this route expects: renaming a project needs an object with a "+
				"display_name string, because the name is the only thing this route changes. Rejected while decoding the "+
				"body, before anything was stored, so the project is unchanged. Send {\"display_name\": \"...\"}")
		return
	}

	displayName := strings.TrimSpace(body.DisplayName)
	if displayName == "" {
		writeError(w, http.StatusBadRequest,
			"display_name is empty: a project must always have a name to render, so an empty correction cannot be stored. "+
				"Rejected in the project-rename route before anything was written, so any existing name still stands. Send a "+
				"name with at least one non-whitespace character, or send DELETE to this project's display-name to go back to "+
				"the name Village derives on its own")
		return
	}
	if len(displayName) > projectDisplayNameMaxLength {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"display_name is %d characters long: a project name longer than %d characters cannot be rendered by the "+
				"surfaces that show it, so it is refused rather than silently truncated. Rejected in the project-rename "+
				"route before anything was written, so any existing name still stands. Shorten the name to %d characters "+
				"or fewer", len(displayName), projectDisplayNameMaxLength, projectDisplayNameMaxLength))
		return
	}

	owned, err := h.queries.CountOwnerTranscriptsInProject(r.Context(), sqlc.CountOwnerTranscriptsInProjectParams{
		OwnerID:     user.PgID(),
		ProjectHash: projectHash,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError,
			"the project could not be looked up: Village could not read your transcripts to confirm the project is yours. "+
				"The failure happened in the project-rename route before the correction was stored, so nothing changed. "+
				"Retry the request, and if it persists check that the database is reachable")
		return
	}
	if owned == 0 {
		writeError(w, http.StatusNotFound, projectNotFoundForOwnerMessage(projectHash))
		return
	}

	if _, err := h.queries.UpsertOwnerOverride(r.Context(), sqlc.UpsertOwnerOverrideParams{
		OwnerID:    user.PgID(),
		TargetKind: string(overrideTargetProject),
		TargetKey:  projectHash,
		Field:      string(overrideFieldDisplayName),
		Value:      displayName,
	}); err != nil {
		writeError(w, http.StatusInternalServerError,
			"the new project name could not be stored: writing the owner correction failed. It happened after the project "+
				"was confirmed yours and before any response was sent, so the project still carries its previous name. "+
				"Retry the request, and if it persists check that the database is reachable")
		return
	}

	h.writeResolvedProject(r.Context(), w, user.PgID(), projectHash)
}

// ClearProjectDisplayName removes the owner's correction, so the project goes back
// to the name Village derives on its own. It is the counterpart of the rename, not
// a deletion of anything published: no transcript is touched either way.
// DELETE /api/v1/users/me/projects/{projectHash}/display-name (AuthRequired)
func (h *Handler) ClearProjectDisplayName(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	projectHash := chi.URLParam(r, "projectHash")

	if err := validateOverrideTargetKey(overrideTargetProject, projectHash); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	removed, err := h.queries.DeleteOwnerOverride(r.Context(), sqlc.DeleteOwnerOverrideParams{
		OwnerID:    user.PgID(),
		TargetKind: string(overrideTargetProject),
		TargetKey:  projectHash,
		Field:      string(overrideFieldDisplayName),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError,
			"the project name could not be reset: removing the owner correction failed. It happened in the "+
				"clear-project-name route before any response was sent, so the project still carries the name you chose. "+
				"Retry the request, and if it persists check that the database is reachable")
		return
	}
	if removed == 0 {
		writeError(w, http.StatusNotFound, fmt.Sprintf(
			"there is no name of your own to remove for project %s: Village is already showing the name it derives from "+
				"the project's own evidence, so there is nothing to clear. Determined in the clear-project-name route "+
				"before anything was written, so nothing changed. Send PATCH to this project first if you meant to choose "+
				"a name", projectHash))
		return
	}

	h.writeResolvedProject(r.Context(), w, user.PgID(), projectHash)
}

// writeResolvedProject answers both correction routes with the SAME resolved shape
// the read surfaces render, so a client never has to guess what the change did:
// it reads the new display name and the tier it came from straight out of the
// response instead of re-deriving them.
func (h *Handler) writeResolvedProject(ctx context.Context, w http.ResponseWriter, ownerID pgtype.UUID, projectHash string) {
	key := projectIdentityKey{OwnerID: ownerID, ProjectHash: projectHash}
	resolved := h.resolveProjectIdentities(ctx, []projectIdentityKey{key})[key]
	writeJSON(w, http.StatusOK, resolvedProjectPayload(projectHash, resolved))
}

// resolvedProject is the project-identity envelope every project-aware response
// carries. project_hash stays on it because the hash, not the name, is what a
// client routes and groups by.
type resolvedProject struct {
	ProjectHash string                 `json:"project_hash"`
	DisplayName string                 `json:"project_display_name"`
	NameSource  projectname.NameSource `json:"project_name_source"`
	RemoteLabel string                 `json:"project_remote_label"`
}

func resolvedProjectPayload(projectHash string, resolved projectname.Resolved) resolvedProject {
	return resolvedProject{
		ProjectHash: projectHash,
		DisplayName: resolved.DisplayName,
		NameSource:  resolved.Source,
		RemoteLabel: resolved.RemoteLabel,
	}
}

func projectNotFoundForOwnerMessage(projectHash string) string {
	return fmt.Sprintf(
		"project %s is not one of yours: you have published no transcript that belongs to this project, and Village only "+
			"lets an owner name their own projects. Determined in the project-correction route before anything was "+
			"written, so nothing changed. Open the project from your profile and use the project hash shown there",
		projectHash)
}

// ProjectCollectiveRollupEntry is one collective in a project's roll-up.
//
// It carries NO share counters. The roll-up is restricted to APPROVED shares, and
// the pending and rejected tallies are the owner's alone by user ratification —
// this page can be loaded by a non-owner, so surfacing them here would be a
// disclosure change rather than a convenience. TranscriptCount is therefore the
// count of that collective's APPROVED transcripts from this project, and the only
// count the shape admits.
type ProjectCollectiveRollupEntry struct {
	ID              pgtype.UUID `json:"id"`
	Name            string      `json:"name"`
	Description     pgtype.Text `json:"description"`
	LinkedGithubOrg pgtype.Text `json:"linked_github_org"`
	TranscriptCount int32       `json:"transcript_count"`
}

// projectCollectiveRollup answers which collectives hold this project's accepted
// transcripts, as this viewer may see them.
//
// The visibility decision is made ENTIRELY by the query: it applies the one
// collectives predicate, character-identical to the collective search, plus the
// contributor opt-in gate. This function passes the viewer through and maps the
// row; it must never add, relax, or second-guess a visibility rule, because a
// second copy of that predicate is exactly the drift the single query exists to
// prevent.
func (h *Handler) projectCollectiveRollup(ctx context.Context, ownerID pgtype.UUID, projectHash string, viewerID pgtype.UUID, viewerIsOwner bool) ([]ProjectCollectiveRollupEntry, error) {
	rows, err := h.queries.ListProjectCollectiveRollup(ctx, sqlc.ListProjectCollectiveRollupParams{
		OwnerID:       ownerID,
		ProjectHash:   projectHash,
		UserID:        viewerID,
		ViewerIsOwner: viewerIsOwner,
	})
	if err != nil {
		return nil, err
	}
	entries := make([]ProjectCollectiveRollupEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, ProjectCollectiveRollupEntry{
			ID:              row.ID,
			Name:            row.Name,
			Description:     row.Description,
			LinkedGithubOrg: row.LinkedGithubOrg,
			TranscriptCount: row.TranscriptCount,
		})
	}
	return entries, nil
}

// GetUserProject serves one user's project page: who owns it, what it is called,
// and which of its transcripts the caller may see.
//
// Auth is OPTIONAL, so the hidden-user boundary matters here exactly as much as it
// does on the profile page: a user who is not discoverable answers 404 to everyone
// but themselves, so their existence cannot be confirmed by guessing a URL. A
// discoverable owner whose transcripts are all private is a DIFFERENT answer — 200
// with an empty list — because the user is not hidden, only their work is.
// GET /api/v1/users/{username}/projects/{projectHash} (AuthOptional)
func (h *Handler) GetUserProject(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	if username == "" {
		writeError(w, http.StatusBadRequest,
			"the request names no user: a project page belongs to the user who published it, so the route needs a "+
				"username. Rejected before any lookup, so nothing was read. Request "+
				"/api/v1/users/{username}/projects/{projectHash}")
		return
	}
	projectHash := chi.URLParam(r, "projectHash")
	if err := validateOverrideTargetKey(overrideTargetProject, projectHash); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	target, err := h.queries.GetUserByUsername(r.Context(), username)
	if err != nil {
		writeError(w, http.StatusNotFound, projectPageNotFoundMessage)
		return
	}

	// The hidden-user boundary, mirroring the public profile route: a
	// non-discoverable user is invisible to everyone except themselves, and the
	// answer is 404 rather than 403 so the refusal does not confirm that the
	// account exists.
	viewer := GetUser(r.Context())
	if !target.IsDiscoverable {
		if viewer == nil || viewer.PgID() != target.ID {
			writeError(w, http.StatusNotFound, projectPageNotFoundMessage)
			return
		}
	}

	viewerID := pgtype.UUID{}
	viewerIsOwner := false
	if viewer != nil {
		viewerID = viewer.PgID()
		viewerIsOwner = viewerID == target.ID
	}

	transcripts, err := h.queries.ListProjectTranscriptsForViewer(r.Context(), sqlc.ListProjectTranscriptsForViewerParams{
		OwnerID:     target.ID,
		ProjectHash: projectHash,
		ViewerID:    viewerID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError,
			"the project's transcripts could not be listed: reading them failed. It happened after the owner was found "+
				"and before any transcript was returned, so nothing was disclosed and nothing changed. Retry the request, "+
				"and if it persists check that the database is reachable")
		return
	}

	key := projectIdentityKey{OwnerID: target.ID, ProjectHash: projectHash}
	resolved := h.resolveProjectIdentities(r.Context(), []projectIdentityKey{key})[key]

	collectives, err := h.projectCollectiveRollup(r.Context(), target.ID, projectHash, viewerID, viewerIsOwner)
	if err != nil {
		writeError(w, http.StatusInternalServerError,
			"the project's collectives could not be listed: reading the contribution roll-up failed. It happened "+
				"after the project was resolved and before the response was sent, so nothing was disclosed and "+
				"nothing changed. Retry the request, and if it persists check that the database is reachable")
		return
	}

	responses := make([]transcriptResponse, 0, len(transcripts))
	for _, t := range transcripts {
		responses = append(responses, projectPageTranscriptResponse(t, resolved))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"project":     resolvedProjectPayload(projectHash, resolved),
		"owner":       target,
		"transcripts": responses,
		"collectives": collectives,
	})
}

// projectPageNotFoundMessage answers every not-found outcome on the project page
// with ONE message. A missing user, a hidden user and a hidden user's project must
// be indistinguishable, or the difference between them becomes the disclosure.
const projectPageNotFoundMessage = "no such project page: either no user goes by that name, or the user has chosen not to be " +
	"discoverable, so Village will not confirm that the page exists. Determined at the profile-visibility boundary before " +
	"any transcript was read, so nothing was disclosed. Check the username, or ask the user to make their profile " +
	"discoverable in their settings"
