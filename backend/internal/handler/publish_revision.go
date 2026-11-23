package handler

import (
	"errors"
	"math"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

var (
	errDivergentRevision = errors.New("the stored transcript revision has no usable content identity in handler publication; nothing was replaced; ask an operator to reconcile the transcript before retrying")
	errRevisionOverflow  = errors.New("the transcript content revision cannot advance beyond its maximum in handler publication; nothing was replaced; ask an operator to reconcile the transcript")
)

// establishedRevisionIdentity reads only identities that are complete and
// canonical. Historical incomplete or malformed primary identities at revision
// one are not evidence: their predecessor must first be authenticated.
func establishedRevisionIdentity(row sqlc.Transcript) (storage.ContentIdentity, bool, error) {
	if row.ContentRevision < 1 {
		return storage.ContentIdentity{}, false, errDivergentRevision
	}
	if row.SegmentedIdentityHash.Valid || row.SegmentedIdentitySize.Valid {
		if row.ContentHash.Valid || !row.SegmentedIdentityHash.Valid || !row.SegmentedIdentitySize.Valid {
			return storage.ContentIdentity{}, false, errDivergentRevision
		}
		identity, err := storage.NewContentIdentity(row.SegmentedIdentityHash.String, row.SegmentedIdentitySize.Int64)
		if err != nil {
			return storage.ContentIdentity{}, false, errDivergentRevision
		}
		return identity, true, nil
	}
	if row.ContentHash.Valid && row.BlobSizeBytes.Valid {
		identity, err := storage.NewContentIdentity(row.ContentHash.String, row.BlobSizeBytes.Int64)
		if err == nil {
			return identity, true, nil
		}
	}
	if row.ContentRevision > 1 {
		return storage.ContentIdentity{}, false, errDivergentRevision
	}
	return storage.ContentIdentity{}, false, nil
}

// nextContentRevision compares authenticated plaintext identity, not storage
// location or key wrapping. Equal bytes keep the revision even at the maximum.
func nextContentRevision(revision int64, previous, replacement storage.ContentIdentity) (int64, error) {
	if revision < 1 {
		return 0, errDivergentRevision
	}
	if previous.Hash() == replacement.Hash() && previous.PlaintextSize() == replacement.PlaintextSize() {
		return revision, nil
	}
	if revision == math.MaxInt64 {
		return 0, errRevisionOverflow
	}
	return revision + 1, nil
}
