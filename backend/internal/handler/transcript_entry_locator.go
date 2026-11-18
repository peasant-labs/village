package handler

import (
	"bytes"
	"net/http"

	"github.com/peasant-labs/schema"
)

// LocateTranscriptEntry locates a reference only in the served main partition.
func (h *Handler) LocateTranscriptEntry(w http.ResponseWriter, r *http.Request) {
	s, ok := h.beginSegmentedRead(w, r)
	if !ok {
		return
	}
	// The contract decoder writes its own errors. Apply the refusal policy
	// before calling it, then restore the successful read policy afterwards.
	w.Header().Set("Cache-Control", "no-store")
	var request schema.TranscriptEntryLocateRequest
	if !h.decodeContractBody(w, r, opLocateTranscriptEntry, &request) {
		return
	}
	segmentedHeaders(w)
	if request.Revision != s.opened.Index.Revision.String() {
		segmentedError(w, http.StatusConflict, "The transcript changed; reload before continuing.")
		return
	}
	if request.Representation != s.representation {
		segmentedError(w, http.StatusConflict, "The transcript was re-projected; reload before continuing.")
		return
	}
	notFound := func() { segmentedError(w, http.StatusNotFound, "Transcript not found") }
	if request.ContentHash == "" || request.ContentHash != s.visibleContentHash() {
		notFound()
		return
	}
	part, ok := segmentPartition(w, s, mainPartitionKey())
	if !ok {
		return
	}
	release, err := s.reserve()
	if err != nil {
		segmentedError(w, http.StatusInternalServerError, "Cannot locate transcript entry; no result was served; retry later.")
		return
	}
	defer release()
	var captured bytes.Buffer
	captured.WriteString(`{"turns":[`)
	for i, row := range part.Rows {
		raw, err := s.opened.Bytes.ReadTurnBytes(r.Context(), row.Start, row.End)
		if err != nil {
			segmentedError(w, http.StatusInternalServerError, "Cannot locate transcript entry; no result was served; retry later.")
			return
		}
		if i > 0 {
			captured.WriteByte(',')
		}
		captured.Write(raw)
	}
	captured.WriteString(`]}`)
	turns, err := schema.NewTranscriptCookTurns(captured.Bytes())
	if err != nil || len(turns) != len(part.Rows) {
		segmentedError(w, http.StatusInternalServerError, "Cannot locate transcript entry; no result was served; retry later.")
		return
	}
	rows := make([]schema.TranscriptEntryScanRow, len(turns))
	for i, turn := range turns {
		row := part.Rows[i]
		rows[i] = schema.TranscriptEntryScanRow{Turn: turn, State: row.RecordedIndexState, Recorded: row.RecordedIndex}
	}
	hit, found, err := schema.LocateTranscriptEntry(rows, request.Reference)
	if err != nil {
		segmentedError(w, http.StatusInternalServerError, "Cannot locate transcript entry; no result was served; retry later.")
		return
	}
	if !found {
		notFound()
		return
	}
	for _, ref := range part.Positions {
		if hit.Position >= ref.SourceFirstPosition && hit.Position <= ref.SourceLastPosition {
			writeJSON(w, http.StatusOK, schema.TranscriptEntryMatch{Context: s.context(part), Kind: hit.Kind, Position: hit.Position, SegmentOrdinal: ref.Ordinal, RecordedIndex: hit.RecordedIndex})
			return
		}
	}
	segmentedError(w, http.StatusInternalServerError, "Cannot locate transcript entry in the segment directory; no result was served; retry later.")
}
