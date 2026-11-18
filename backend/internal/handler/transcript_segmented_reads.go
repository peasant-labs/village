package handler

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// segmentedRead binds every read to the freshly authorized row and the
// viewer's projection; the content cache never makes an access decision.
type segmentedRead struct {
	row            sqlc.Transcript
	opened         *openedTranscript
	withheld       bool
	native         schema.TranscriptNativePresence
	representation string
	reserve        transcriptBuildReserve
	refuseBusy     func(http.ResponseWriter)
}

func segmentedHeaders(w http.ResponseWriter) {
	w.Header().Set("Vary", "Authorization, Cookie")
	w.Header().Set("Cache-Control", "private, no-store")
}

func segmentedError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Cache-Control", "no-store")
	writeError(w, status, message)
}

func (h *Handler) segmentedBusy(w http.ResponseWriter, r *http.Request, row sqlc.Transcript) {
	fresh, err := h.queries.GetTranscriptByID(r.Context(), row.ID)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !h.canViewTranscript(r.Context(), GetUser(r.Context()), fresh) {
		segmentedError(w, http.StatusNotFound, "Transcript not found")
		return
	}
	if err != nil {
		segmentedError(w, http.StatusInternalServerError, "Cannot read transcript access during the read; retry the request.")
		return
	}
	w.Header().Set("Retry-After", "1")
	segmentedError(w, http.StatusServiceUnavailable, "The transcript read is temporarily busy; retry shortly.")
}

func (h *Handler) beginSegmentedRead(w http.ResponseWriter, r *http.Request) (*segmentedRead, bool) {
	segmentedHeaders(w)
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		segmentedError(w, http.StatusBadRequest, "Cannot read the transcript because the identifier is malformed; use a valid transcript address.")
		return nil, false
	}
	row, err := h.queries.GetTranscriptByID(r.Context(), toPgUUID(id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			segmentedError(w, http.StatusNotFound, "Transcript not found")
		} else {
			segmentedError(w, http.StatusInternalServerError, "Cannot read transcript access during the read; retry the request.")
		}
		return nil, false
	}
	user := GetUser(r.Context())
	if !h.canViewTranscript(r.Context(), user, row) {
		segmentedError(w, http.StatusNotFound, "Transcript not found")
		return nil, false
	}
	values := r.URL.Query()
	revision := values.Get("revision")
	if raw, present := values["revision"]; present {
		if len(raw) != 1 || revision == "" {
			segmentedError(w, http.StatusBadRequest, "Cannot read the transcript because revision is malformed; provide a decimal revision.")
			return nil, false
		}
		for _, c := range revision {
			if c < '0' || c > '9' {
				segmentedError(w, http.StatusBadRequest, "Cannot read the transcript because revision is malformed; provide a decimal revision.")
				return nil, false
			}
		}
		if revision != contentGeneration(row.ContentRevision).String() {
			segmentedError(w, http.StatusConflict, "The transcript changed; reload before continuing.")
			return nil, false
		}
	}
	echo := values.Get("representation")
	if raw, present := values["representation"]; present {
		if len(raw) != 1 || echo == "" {
			segmentedError(w, http.StatusBadRequest, "Cannot read the transcript because representation is malformed; reload the transcript and retry.")
			return nil, false
		}
		prefix := schema.RepresentationVersion + "."
		data, decodeErr := hex.DecodeString(strings.TrimPrefix(echo, prefix))
		if !strings.HasPrefix(echo, prefix) || decodeErr != nil || len(data) != 32 || echo != strings.ToLower(echo) {
			segmentedError(w, http.StatusBadRequest, "Cannot read the transcript because representation is malformed; reload the transcript and retry.")
			return nil, false
		}
	}
	owner, err := h.resolvePublicOwner(r.Context(), user, row.OwnerID)
	if err != nil {
		segmentedError(w, http.StatusInternalServerError, "Cannot determine the transcript projection during the read; retry the request.")
		return nil, false
	}
	opened, err := h.openCachedTranscript(r.Context(), row, func(fresh sqlc.Transcript) bool { return h.canViewTranscript(r.Context(), user, fresh) })
	if err != nil {
		if errors.Is(err, errTranscriptReadBusy) {
			h.segmentedBusy(w, r, row)
			return nil, false
		}
		fresh, lookupErr := h.queries.GetTranscriptByID(r.Context(), row.ID)
		if errors.Is(lookupErr, pgx.ErrNoRows) || lookupErr == nil && !h.canViewTranscript(r.Context(), user, fresh) {
			segmentedError(w, http.StatusNotFound, "Transcript not found")
			return nil, false
		}
		segmentedError(w, http.StatusInternalServerError, "Cannot open transcript content during the read; no content was served; retry or ask the owner to republish.")
		return nil, false
	}
	if opened.Reloaded {
		fresh, lookupErr := h.queries.GetTranscriptByID(r.Context(), row.ID)
		if errors.Is(lookupErr, pgx.ErrNoRows) || lookupErr == nil && !h.canViewTranscript(r.Context(), user, fresh) {
			segmentedError(w, http.StatusNotFound, "Transcript not found")
			return nil, false
		}
		if lookupErr != nil {
			segmentedError(w, http.StatusInternalServerError, "Cannot read transcript access during the read; retry the request.")
			return nil, false
		}
		current, currentErr := descriptorFromTranscript(fresh)
		source, sourceErr := descriptorFromTranscript(opened.SourceRow)
		if currentErr != nil || sourceErr != nil || !descriptorsEqual(current, source) {
			segmentedError(w, http.StatusConflict, "The transcript changed during the read; reload before continuing.")
			return nil, false
		}
		row = fresh
		if revision != "" && revision != contentGeneration(row.ContentRevision).String() {
			segmentedError(w, http.StatusConflict, "The transcript changed; reload before continuing.")
			return nil, false
		}
		owner, err = h.resolvePublicOwner(r.Context(), user, row.OwnerID)
		if err != nil {
			segmentedError(w, http.StatusInternalServerError, "Cannot determine the transcript projection during the read; retry the request.")
			return nil, false
		}
	}
	if opened.Index.Revision != contentGeneration(row.ContentRevision) {
		segmentedError(w, http.StatusConflict, "The transcript changed during the read; reload before continuing.")
		return nil, false
	}
	withheld := !owner.IsProfile()
	native := opened.Index.NativePresence
	if withheld {
		native = schema.TranscriptNativeWithheld
	}
	viewer := schema.TranscriptViewerProjectionAnonymous
	if user != nil {
		viewer = schema.TranscriptViewerProjectionAuthenticated
	}
	representation := opened.Index.Representation(viewer, withheld, native)
	if echo != "" && echo != representation {
		segmentedError(w, http.StatusConflict, "The transcript was re-projected; reload before continuing.")
		return nil, false
	}
	return &segmentedRead{row: row, opened: opened, withheld: withheld, native: native, representation: representation, reserve: func() (func(), error) {
		return h.contentCache.AcquireBuild(context.WithoutCancel(r.Context()))
	}, refuseBusy: func(w http.ResponseWriter) { h.segmentedBusy(w, r, row) }}, true
}

func readGeneration(w http.ResponseWriter, r *http.Request) (int, bool) {
	values, present := r.URL.Query()["v"]
	if !present {
		return 1, true
	}
	if len(values) != 1 {
		segmentedError(w, http.StatusBadRequest, "Cannot select the segmented read version; use a supported version.")
		return 0, false
	}
	n, err := strconv.Atoi(values[0])
	version, supported := schema.SegmentedReadProtocolVersion(n)
	if err == nil && supported {
		switch version {
		case schema.SegmentedReadMinVersion:
			return 1, true
		case schema.SegmentedReadAPIVersion:
			return 2, true
		}
	}
	segmentedError(w, http.StatusBadRequest, "Cannot select the segmented read version; use a supported version.")
	return 0, false
}

func readPartition(w http.ResponseWriter, r *http.Request, version int) (partitionKey, schema.TranscriptSegmentPartition, bool) {
	kind := schema.TranscriptSegmentPartitionKind(r.URL.Query().Get("partition"))
	if kind == "" {
		kind = schema.TranscriptPartitionMain
	}
	sectionRaw, present := r.URL.Query()["section"]
	if kind == schema.TranscriptPartitionMain && !present {
		return mainPartitionKey(), schema.TranscriptSegmentPartition{Kind: kind}, true
	}
	if version == 2 && kind == schema.TranscriptPartitionHistory && present && len(sectionRaw) == 1 {
		n, err := strconv.Atoi(sectionRaw[0])
		if err == nil && n >= 0 {
			return historyPartitionKey(n), schema.TranscriptSegmentPartition{Kind: kind, SectionIndex: &n}, true
		}
	}
	segmentedError(w, http.StatusBadRequest, "Cannot select the transcript partition; use main or a history section with a nonnegative section index.")
	return partitionKey{}, schema.TranscriptSegmentPartition{}, false
}

func (s *segmentedRead) context(part *partitionIndex) schema.TranscriptReadContext {
	x := s.opened.Index
	h := x.Header
	context := schema.TranscriptReadContext{ProtocolVersion: schema.SegmentedReadAPIVersion, Transport: x.Transport, ContentSessionID: x.ContentSessionID, Revision: x.Revision.String(), Representation: s.representation, Harness: h.Harness, Model: h.Model, ThinkingLevel: h.ThinkingLevel, ThinkingLevelRaw: h.ThinkingLevelRaw, Validation: x.Validation, NativePresence: s.native, TotalTurns: part.TotalTurns, TotalSegments: part.TotalSegments, TotalConversationTurns: part.TotalConversationTurns}
	context.ContentHash = s.visibleContentHash()
	return context
}

func (s *segmentedRead) visibleContentHash() string {
	if s.withheld || s.opened.Index.Validation == schema.TranscriptReadMigrated {
		return ""
	}
	return string(s.opened.Index.ContentHash)
}

func segmentPartition(w http.ResponseWriter, s *segmentedRead, key partitionKey) (*partitionIndex, bool) {
	part, err := s.opened.Index.Partition(key, s.reserve)
	if errors.Is(err, errPartitionAbsent) {
		segmentedError(w, http.StatusNotFound, "Transcript partition not found")
		return nil, false
	}
	if errors.Is(err, errTranscriptReadBusy) {
		s.refuseBusy(w)
		return nil, false
	}
	if err != nil {
		segmentedError(w, http.StatusInternalServerError, "Cannot index transcript rows during the read; no partial content was served; retry or ask the owner to republish.")
		return nil, false
	}
	return part, true
}

func directoryPage(w http.ResponseWriter, r *http.Request, total int) (int, int, *int, *int, bool) {
	page := 1
	if raw, has := r.URL.Query()["page"]; has {
		if len(raw) != 1 {
			segmentedError(w, http.StatusBadRequest, "Cannot read the directory page; provide one positive page number.")
			return 0, 0, nil, nil, false
		}
		var err error
		page, err = strconv.Atoi(raw[0])
		if err != nil || page <= 0 {
			segmentedError(w, http.StatusBadRequest, "Cannot read the directory page; provide a positive page number.")
			return 0, 0, nil, nil, false
		}
	}
	cap := schema.MaxTranscriptDirectoryRefs
	pages := 1
	if total > cap {
		pages = (total-1)/cap + 1
	}
	if page > pages {
		segmentedError(w, http.StatusNotFound, fmt.Sprintf("Directory page %d is outside the %d available pages.", page, pages))
		return 0, 0, nil, nil, false
	}
	if total <= cap {
		return 0, total, nil, nil, true
	}
	start := (page - 1) * cap
	end := min(start+cap, total)
	return start, end, &page, &cap, true
}

func (h *Handler) GetTranscriptSegments(w http.ResponseWriter, r *http.Request) {
	s, ok := h.beginSegmentedRead(w, r)
	if !ok {
		return
	}
	version, ok := readGeneration(w, r)
	if !ok {
		return
	}
	key, partition, ok := readPartition(w, r, version)
	if !ok {
		return
	}
	part, ok := segmentPartition(w, s, key)
	if !ok {
		return
	}
	start, end, page, size, ok := directoryPage(w, r, part.TotalSegments)
	if !ok {
		return
	}
	if version == 2 {
		out := schema.PositionedDirectory{Context: s.context(part), Partition: partition, UsagePresent: part.UsagePresent, Segments: part.Refs[start:end], DirectoryPage: page, DirectoryPerPage: size}
		if key == mainPartitionKey() {
			out.History = s.opened.Index.History
		}
		if err := out.Validate(); err != nil {
			segmentedError(w, http.StatusInternalServerError, "Cannot build the transcript directory; no partial directory was served; retry later.")
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	id, refs, ok := legacyDirectory(s, part)
	if !ok {
		segmentedError(w, http.StatusInternalServerError, "Cannot serve the legacy transcript directory because recorded identities are unsuitable; request the positioned version.")
		return
	}
	out := schema.TranscriptSegmentDirectory{SessionID: id, Revision: s.opened.Index.Revision.String(), TotalTurns: part.TotalTurns, TotalConversationTurns: part.TotalConversationTurns, TotalSegments: part.TotalSegments, Segments: refs[start:end], DirectoryPage: page, DirectoryPerPage: size}
	if err := out.Validate(); err != nil {
		segmentedError(w, http.StatusInternalServerError, "Cannot serve the legacy transcript directory; request the positioned version.")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func legacyDirectory(s *segmentedRead, p *partitionIndex) (schema.SessionID, []schema.TranscriptSegmentRef, bool) {
	if s.opened.Index.ContentSessionID == nil {
		return "", nil, false
	}
	id, err := schema.NewSessionID(*s.opened.Index.ContentSessionID)
	if err != nil {
		return "", nil, false
	}
	refs := make([]schema.TranscriptSegmentRef, 0, len(p.Refs))
	for _, ref := range p.Refs {
		if ref.FirstTurn == nil || ref.LastTurn == nil {
			return "", nil, false
		}
		refs = append(refs, schema.TranscriptSegmentRef{Index: ref.Index, FirstTurn: *ref.FirstTurn, LastTurn: *ref.LastTurn, ByteLength: ref.ByteLength, ConversationTurns: ref.ConversationTurns})
	}
	return id, refs, true
}

func (h *Handler) GetTranscriptSegment(w http.ResponseWriter, r *http.Request) {
	s, ok := h.beginSegmentedRead(w, r)
	if !ok {
		return
	}
	version, ok := readGeneration(w, r)
	if !ok {
		return
	}
	key, partition, ok := readPartition(w, r, version)
	if !ok {
		return
	}
	part, ok := segmentPartition(w, s, key)
	if !ok {
		return
	}
	ordinal, err := strconv.Atoi(chi.URLParam(r, "index"))
	if err != nil || ordinal < 0 {
		segmentedError(w, http.StatusBadRequest, "Cannot select a segment; provide a nonnegative segment ordinal.")
		return
	}
	if ordinal >= len(part.Refs) {
		segmentedError(w, http.StatusNotFound, "Transcript segment not found")
		return
	}
	ref := part.Refs[ordinal]
	if version == 2 {
		segment := schema.PositionedSegment{Context: s.context(part), Partition: partition, Index: ordinal, Rows: make([]schema.TranscriptSegmentRow, 0, ref.SourceLastPosition-ref.SourceFirstPosition+1), ByteLength: ref.ByteLength, HasOlder: ordinal > 0, HasNewer: ordinal < part.TotalSegments-1}
		for _, row := range part.Rows[ref.SourceFirstPosition : ref.SourceLastPosition+1] {
			raw, err := s.opened.Bytes.ReadTurnBytes(r.Context(), row.Start, row.End)
			if err != nil {
				segmentedError(w, http.StatusInternalServerError, "Cannot read transcript rows; no partial segment was served; retry later.")
				return
			}
			turn, err := schema.NewTranscriptServedTurn(raw)
			if err != nil {
				segmentedError(w, http.StatusInternalServerError, "Cannot encode transcript rows; no partial segment was served; retry later.")
				return
			}
			segment.Rows = append(segment.Rows, schema.TranscriptSegmentRow{Position: row.ServedPosition, RecordedIndexState: row.RecordedIndexState, RecordedIndex: row.RecordedIndex, DisplayNumber: row.DisplayNumber, Turn: turn})
		}
		if !s.withheld {
			inSegment := make(map[int]bool, ref.SourceLastPosition-ref.SourceFirstPosition+1)
			for _, row := range part.Rows[ref.SourceFirstPosition : ref.SourceLastPosition+1] {
				if row.RecordedIndex != nil {
					inSegment[*row.RecordedIndex] = true
				}
			}
			for _, record := range part.Native {
				if record.Attachment == nil || record.Attachment.TurnIndex == nil {
					continue
				}
				if inSegment[*record.Attachment.TurnIndex] {
					segment.NativeMetadata = append(segment.NativeMetadata, record)
				}
			}
		}
		if err := schema.ValidatePositionedSegmentBoundaries(segment, ref); err != nil {
			segmentedError(w, http.StatusInternalServerError, "Cannot validate transcript segment boundaries; no partial segment was served.")
			return
		}
		body, err := schema.MarshalPositionedSegment(segment)
		if err != nil {
			segmentedError(w, http.StatusInternalServerError, "Cannot encode transcript segment; no partial segment was served.")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return
	}
	_, refs, ok := legacyDirectory(s, part)
	if !ok {
		segmentedError(w, http.StatusInternalServerError, "Cannot serve the legacy transcript segment; request the positioned version.")
		return
	}
	legacy := schema.TranscriptSegment{Index: ordinal, Revision: s.opened.Index.Revision.String(), FirstTurn: refs[ordinal].FirstTurn, LastTurn: refs[ordinal].LastTurn, ByteLength: ref.ByteLength, HasOlder: ordinal > 0, HasNewer: ordinal < part.TotalSegments-1, Turns: []schema.TranscriptTurn{}}
	for _, row := range part.Rows[ref.SourceFirstPosition : ref.SourceLastPosition+1] {
		raw, err := s.opened.Bytes.ReadTurnBytes(r.Context(), row.Start, row.End)
		if err != nil {
			segmentedError(w, http.StatusInternalServerError, "Cannot read transcript rows; no partial segment was served.")
			return
		}
		var turn schema.TurnDetail
		if json.Unmarshal(raw, &turn) != nil {
			segmentedError(w, http.StatusInternalServerError, "Cannot decode the legacy transcript segment; request the positioned version.")
			return
		}
		legacy.Turns = append(legacy.Turns, schema.TranscriptTurn{TurnDetail: turn, ByteLength: row.ByteLength})
	}
	if legacy.Validate() != nil || schema.ValidateTranscriptSegmentBoundaries(legacy, part.TotalSegments) != nil {
		segmentedError(w, http.StatusInternalServerError, "Cannot validate the legacy transcript segment; request the positioned version.")
		return
	}
	writeJSON(w, http.StatusOK, legacy)
}

func (h *Handler) GetTranscriptWindow(w http.ResponseWriter, r *http.Request) {
	s, ok := h.beginSegmentedRead(w, r)
	if !ok {
		return
	}
	version, ok := readGeneration(w, r)
	if !ok {
		return
	}
	key, partition, ok := readPartition(w, r, version)
	if !ok {
		return
	}
	part, ok := segmentPartition(w, s, key)
	if !ok {
		return
	}
	span := schema.TranscriptWindowSpanDefault
	if raw, has := r.URL.Query()["span"]; has {
		if len(raw) != 1 {
			segmentedError(w, http.StatusBadRequest, "Cannot select a window span; provide one supported span.")
			return
		}
		var err error
		span, err = strconv.Atoi(raw[0])
		if err != nil || span < schema.TranscriptWindowSpanMinimum || span > schema.TranscriptWindowSpanMaximum {
			segmentedError(w, http.StatusBadRequest, "Cannot select a window span; use a supported span.")
			return
		}
	}
	var anchor *int
	if raw, has := r.URL.Query()["anchor"]; has {
		if len(raw) != 1 {
			segmentedError(w, http.StatusBadRequest, "Cannot select a window anchor; provide one nonnegative recorded index.")
			return
		}
		value, err := strconv.Atoi(raw[0])
		if err != nil || value < 0 {
			segmentedError(w, http.StatusBadRequest, "Cannot select a window anchor; provide a nonnegative recorded index.")
			return
		}
		anchor = &value
	}
	if version == 1 {
		if anchor == nil {
			segmentedError(w, http.StatusBadRequest, "The legacy window requires an anchor; provide a nonnegative recorded index.")
			return
		}
		id, refs, ok := legacyDirectory(s, part)
		if !ok {
			segmentedError(w, http.StatusInternalServerError, "Cannot serve the legacy transcript window; request the positioned version.")
			return
		}
		turns := make([]schema.TranscriptWindowTurn, 0, len(part.Rows))
		for _, row := range part.Rows {
			if row.RecordedIndex == nil {
				segmentedError(w, http.StatusInternalServerError, "Cannot serve the legacy transcript window; request the positioned version.")
				return
			}
			turns = append(turns, schema.TranscriptWindowTurn{Index: *row.RecordedIndex, Role: row.Role})
		}
		window, err := schema.ResolveTranscriptWindow(id, s.opened.Index.Revision.String(), *anchor, span, turns, refs)
		if err != nil {
			segmentedError(w, http.StatusNotFound, "Transcript window not found")
			return
		}
		writeJSON(w, http.StatusOK, window)
		return
	}
	turns := make([]schema.PositionedWindowTurn, 0, len(part.Rows))
	for _, row := range part.Rows {
		turns = append(turns, schema.PositionedWindowTurn{Position: row.ServedPosition, Role: row.Role, Depth: row.Depth, RecordedIndex: row.RecordedIndex})
	}
	window, err := schema.ResolvePositionedWindow(schema.PositionedWindowInput{Context: s.context(part), Partition: partition, RequestedAnchor: anchor, Span: span, Turns: turns, Refs: part.Refs})
	if err != nil {
		segmentedError(w, http.StatusNotFound, "Transcript window not found")
		return
	}
	if err := schema.ValidatePositionedWindowCoverage(window, part.Refs); err != nil {
		segmentedError(w, http.StatusInternalServerError, "Cannot validate transcript window coverage; no window was served.")
		return
	}
	writeJSON(w, http.StatusOK, window)
}

func (h *Handler) GetTranscriptHeader(w http.ResponseWriter, r *http.Request) {
	s, ok := h.beginSegmentedRead(w, r)
	if !ok {
		return
	}
	integrity, err := h.servedIntegrity(r.Context(), s.row)
	if err != nil {
		segmentedError(w, http.StatusInternalServerError, "Cannot read transcript integrity during header revalidation; no header was served; retry.")
		return
	}
	x := s.opened.Index
	header := x.Header
	if s.withheld {
		header = schema.WithholdSessionHeaderRepository(header)
	}
	canonical := false
	if header.Harness != nil {
		for _, name := range schema.Harnesses() {
			if string(name) == *header.Harness {
				canonical = true
				break
			}
		}
	}
	out := schema.TranscriptContentHeader{ContractVersion: x.ContractVersion, Kind: schema.ContentKindSessionDetail, Revision: x.Revision.String(), Representation: s.representation, SessionHeader: header, Canonical: canonical, OmittedImages: x.OmittedImages, RetainedEvidence: x.RetainedEvidence}
	if s.withheld {
		out.RetainedEvidence = nil
	} else {
		out.ContentHash = s.visibleContentHash()
	}
	if integrity != nil && len(integrity.FlaggedTurns) != 0 && out.ContentHash != "" {
		part, ok := segmentPartition(w, s, mainPartitionKey())
		if !ok {
			return
		}
		out.FlaggedTurns = remapFlaggedTurns(part, integrity.FlaggedTurns)
	}
	if err := out.Validate(); err != nil {
		segmentedError(w, http.StatusInternalServerError, "Cannot validate the transcript header; no header was served.")
		return
	}
	hashWord := schema.TranscriptContentHashPending
	if x.Durable {
		hashWord = schema.TranscriptContentHashKnown
	}
	visibility := schema.TranscriptRetainedOwnerVisible
	if s.withheld {
		visibility = schema.TranscriptRetainedOwnerWithheld
	}
	etag := schema.TranscriptHeaderETag(schema.HeaderETagInputs{Representation: out.Representation, ContractVersion: out.ContractVersion, Kind: out.Kind, Canonical: out.Canonical, OmittedImages: out.OmittedImages, FlaggedTurns: out.FlaggedTurns, RetainedEvidence: out.RetainedEvidence, ContentHashWord: hashWord, RetainedOwnerVisibility: visibility})
	w.Header().Set("ETag", etag)
	if r.URL.Query().Get("revision") != "" && x.Validation != schema.TranscriptReadMigrated {
		w.Header().Set("Cache-Control", "private, no-cache")
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	if ifNoneMatchMatches(r.Header.Get("If-None-Match"), strings.Trim(etag, `"`)) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func remapFlaggedTurns(part *partitionIndex, storedPositions []int) []schema.TranscriptFlaggedTurn {
	var marks []schema.TranscriptFlaggedTurn
	seen := map[int]bool{}
	for _, stored := range storedPositions {
		if stored < 0 || stored >= len(part.Verdict) {
			continue
		}
		verdict := part.Verdict[stored]
		if !verdict.Keep || seen[verdict.ServedPosition] {
			continue
		}
		seen[verdict.ServedPosition] = true
		marks = append(marks, schema.TranscriptFlaggedTurn{Position: verdict.ServedPosition, Turn: part.Rows[verdict.ServedPosition].RecordedIndex})
	}
	sort.Slice(marks, func(i, j int) bool { return marks[i].Position < marks[j].Position })
	return marks
}
