package handler

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"

	"github.com/google/uuid"
)

// manualAnnotationHashInput is deliberately separate from the published
// annotation-push hash: Village owns the creation and persistence of manual
// labels, and its identity must include the exact viewed transcript UUID.
type manualAnnotationHashInput struct {
	TargetTranscriptID uuid.UUID
	TypeID             string
	AnnotatorName      string
	Value              string
	EntrySessionID     string
	EntryIndex         int
	EntryEndIndex      int
	IsPrimary          bool
	Reason             *string
}

// computeManualAnnotationHash produces a stable, length-delimited SHA-256
// identity for a Village-only manual label. The versioned domain separator and
// typed transcript UUID prevent two same-local-id transcripts from collapsing
// into one viewer-owned annotation. It intentionally does not change the
// schema module's hash algorithm for pushed annotations.
func computeManualAnnotationHash(input manualAnnotationHashInput) string {
	h := sha256.New()
	writeManualAnnotationHashString(h, "village.manual-annotation/v1")
	writeManualAnnotationHashString(h, input.TargetTranscriptID.String())
	writeManualAnnotationHashString(h, input.TypeID)
	writeManualAnnotationHashString(h, input.AnnotatorName)
	writeManualAnnotationHashString(h, input.Value)
	writeManualAnnotationHashString(h, input.EntrySessionID)
	writeManualAnnotationHashInt(h, input.EntryIndex)
	writeManualAnnotationHashInt(h, input.EntryEndIndex)
	if input.IsPrimary {
		_, _ = h.Write([]byte{1})
	} else {
		_, _ = h.Write([]byte{0})
	}
	if input.Reason == nil {
		_, _ = h.Write([]byte{0})
	} else {
		_, _ = h.Write([]byte{1})
		writeManualAnnotationHashString(h, *input.Reason)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func writeManualAnnotationHashString(h hash.Hash, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = h.Write(length[:])
	_, _ = h.Write([]byte(value))
}

func writeManualAnnotationHashInt(h hash.Hash, value int) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(int64(value)))
	_, _ = h.Write(encoded[:])
}
