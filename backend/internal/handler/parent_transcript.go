package handler

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// parentLookup names one child session whose parent transcript a response
// needs: the child's own transcript id, its owner, and the session that
// started it.
type parentLookup struct {
	Child           pgtype.UUID
	Owner           pgtype.UUID
	ParentSessionID pgtype.Text
}

type parentKey struct {
	owner pgtype.UUID
	local string
}

// parentTranscripts maps a child transcript id to its parent's transcript id.
// A child with no entry has no parent this viewer may open.
type parentTranscripts map[pgtype.UUID]schema.TranscriptID

// of returns the parent transcript id, or nil.
func (p parentTranscripts) of(child pgtype.UUID) *schema.TranscriptID {
	if id, ok := p[child]; ok {
		return &id
	}
	return nil
}

// resolveParentTranscripts resolves, for every child on a page, the transcript
// published from the session that started it, within the child's own owner (a
// session id is unique only per owner). The owner never reaches the wire, so
// this is how a reader folds a child under its parent without one.
//
// A parent is kept only when the viewer may read it under the transcript read
// policy, read for every parent at once (ListReadableTranscriptIDs, held equal
// to transcriptViewAccess by a test), so the id never names a transcript the
// viewer cannot open. Nothing started the session, the parent was never published,
// or the viewer may not read it: the child has no entry and serves null. The
// parents of a page are read in one statement.
func (h *Handler) resolveParentTranscripts(ctx context.Context, viewer *AuthUser, lookups []parentLookup) (parentTranscripts, error) {
	resolved := parentTranscripts{}
	wanted := map[parentKey][]pgtype.UUID{}
	var owners []pgtype.UUID
	var locals []string
	for _, lookup := range lookups {
		if !lookup.ParentSessionID.Valid || lookup.ParentSessionID.String == "" || !lookup.Owner.Valid {
			continue
		}
		key := parentKey{owner: lookup.Owner, local: lookup.ParentSessionID.String}
		if _, seen := wanted[key]; !seen {
			owners = append(owners, lookup.Owner)
			locals = append(locals, lookup.ParentSessionID.String)
		}
		wanted[key] = append(wanted[key], lookup.Child)
	}
	if len(wanted) == 0 {
		return resolved, nil
	}
	rows, err := h.queries.ListTranscriptsByOwnerLocalIDs(ctx, sqlc.ListTranscriptsByOwnerLocalIDsParams{OwnerIds: owners, LocalIds: locals})
	if err != nil {
		return nil, fmt.Errorf("handler.resolveParentTranscripts: the parent transcripts of %d sessions could not be read; no response was built without them; retry the request: %w", len(wanted), err)
	}
	parentIDs := make([]pgtype.UUID, 0, len(rows))
	for _, row := range rows {
		if _, ok := wanted[parentKey{owner: row.OwnerID, local: row.LocalID}]; ok {
			parentIDs = append(parentIDs, row.ID)
		}
	}
	readable := map[pgtype.UUID]bool{}
	if len(parentIDs) > 0 {
		ids, err := h.queries.ListReadableTranscriptIDs(ctx, sqlc.ListReadableTranscriptIDsParams{TranscriptIds: parentIDs, ViewerID: viewerID(viewer)})
		if err != nil {
			return nil, fmt.Errorf("handler.resolveParentTranscripts: whether the viewer may read the parent transcripts could not be decided; retry the request: %w", err)
		}
		for _, id := range ids {
			readable[id] = true
		}
	}
	for _, row := range rows {
		children, ok := wanted[parentKey{owner: row.OwnerID, local: row.LocalID}]
		if !ok || !readable[row.ID] {
			continue
		}
		parent := schema.TranscriptID(uuid.UUID(row.ID.Bytes).String())
		for _, child := range children {
			if child != row.ID {
				resolved[child] = parent
			}
		}
	}
	return resolved, nil
}

// parentLookupOf is the lookup for a stored transcript row.
func parentLookupOf(row sqlc.Transcript) parentLookup {
	return parentLookup{Child: row.ID, Owner: row.OwnerID, ParentSessionID: row.ParentSessionID}
}

// resolveParentTranscriptsOf resolves the parents of stored transcript rows.
func (h *Handler) resolveParentTranscriptsOf(ctx context.Context, viewer *AuthUser, rows []sqlc.Transcript) (parentTranscripts, error) {
	lookups := make([]parentLookup, 0, len(rows))
	for _, row := range rows {
		lookups = append(lookups, parentLookupOf(row))
	}
	return h.resolveParentTranscripts(ctx, viewer, lookups)
}
