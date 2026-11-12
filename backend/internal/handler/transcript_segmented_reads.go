package handler

import "net/http"

// TranscriptSegmentedReadNotImplemented answers the three segmented transcript
// reads the served contract declares before Village serves them: the segment
// directory, one segment, and the window around an anchor turn. It reads
// nothing: no transcript is looked up, so the answer is the same for every id
// and discloses nothing about whether one exists.
func (h *Handler) TranscriptSegmentedReadNotImplemented(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotImplemented, "Segmented transcript reads are declared by the Village API but this server does not serve them yet; nothing was read; fetch the whole transcript from GET /api/v1/transcripts/{id}/content instead")
}
