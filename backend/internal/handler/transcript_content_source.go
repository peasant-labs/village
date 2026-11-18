package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

// This file is the content source of the positioned transcript reads: the one
// place that holds a stored generation's bytes, parses them, and decides which
// stored rows are served and what each one is called. A read handler asks the
// source for a partition and receives its served rows. No handler calls the
// cooking rule, and none reads a body to number a row.
//
// Order of work, per generation:
//
//  1. The existing encrypted read authenticates and decrypts the stored object
//     once (readEncryptedTranscript), and the bytes are normalized to the
//     canonical envelope without writing anything.
//  2. One structural pass records where the session detail, each partition's
//     turn array and the served harness lie in those bytes.
//  3. A partition is indexed on first use, from the held bytes: the stored
//     turns are captured once, schema.CookTranscriptRows decides membership
//     and numbers every kept row, and only the verdicts, the served order, the
//     numbers and the byte ranges survive. The captured turns are released.
//
// A main read and a history read of one generation therefore share one
// decryption and one structural pass; each partition is cooked once, with its
// own numbering state.

// errUncookable is the refusal for a partition whose rows cannot be cooked and
// numbered. The answer is a 500 that names the operation and carries no
// partial body. The wrapped cause (a row position, never content) is for the
// operator's log and is not part of the served copy.
var errUncookable = errors.New("transcript rows could not be numbered in handler.transcriptIndex.Partition while indexing a stored partition; nothing was served for this partition and no partial body was produced; the stored transcript needs an operator's attention, so retry later or ask its owner to republish")

// errPartitionAbsent reports a partition key the generation does not hold: a
// history section beyond the recorded sections, or a main key that names one.
var errPartitionAbsent = errors.New("the transcript holds no such partition in handler.transcriptIndex.Partition; nothing was indexed; request a partition the header lists")

// contentGeneration names one generation of stored content by its revision.
// String is the only source of a revision string.
type contentGeneration int64

func (g contentGeneration) String() string { return strconv.FormatInt(int64(g), 10) }

// turnBytesReader returns the bytes of served turns from the held document. A
// range is a pair of offsets the index recorded; the reader never searches.
type turnBytesReader interface {
	ReadTurnBytes(ctx context.Context, start, end int64) ([]byte, error)
}

// partitionKey identifies one index inside one generation. Section is
// meaningful only for Kind == history and is -1 otherwise, so a main key can
// never resolve a section's index.
type partitionKey struct {
	Kind    schema.TranscriptSegmentPartitionKind
	Section int
}

func mainPartitionKey() partitionKey {
	return partitionKey{Kind: schema.TranscriptPartitionMain, Section: -1}
}

func historyPartitionKey(section int) partitionKey {
	return partitionKey{Kind: schema.TranscriptPartitionHistory, Section: section}
}

// partitionByteRange is where one partition's turn array lies in the held
// bytes: [Start, End), the array's own brackets included.
type partitionByteRange struct {
	Key        partitionKey
	Start, End int64
}

// indexedTurn is one served row. Rows are held in served order, so a row's
// index in partitionIndex.Rows is its served position and is the only
// coordinate a wire shape uses. Everything else was decided by the one pass
// that held the parsed turn, so nothing below re-reads a body.
type indexedTurn struct {
	// Stored is the row's position in the stored order, the index into
	// partitionIndex.Verdict.
	Stored int
	// ServedPosition equals the row's index in partitionIndex.Rows.
	ServedPosition int
	// DisplayNumber is the producer-assigned number, never empty for a served row.
	DisplayNumber string
	// Start and End bound the turn object in the held bytes.
	Start, End int64
	// Role, ConversationTurn and ByteLength are what the segment cut and the
	// window need; they come from the turn decoded with the cooked one.
	Role             schema.Role
	ConversationTurn bool
	ByteLength       int
	// RecordedIndex is the turn's own index member, kept apart from its position.
	RecordedIndexState schema.TranscriptRecordedIndexState
	RecordedIndex      *int
}

// partitionIndex is one partition's view: the served rows and, by stored
// position, the verdict on every stored row.
type partitionIndex struct {
	Rows    []indexedTurn
	Verdict []schema.CookedTurn
	// TotalTurns is the number of served rows.
	TotalTurns int
}

// cookRowsFunc is the cooking rule. Production is schema.CookTranscriptRows.
type cookRowsFunc func(turns []schema.TranscriptCookTurn, harness schema.Harness) ([]schema.CookedTurn, error)

// transcriptIndex is the content source for one generation.
type transcriptIndex struct {
	Revision      contentGeneration
	ContentHash   storage.ContentHash // empty while the generation's identity is pending
	Harness       schema.Harness      // the served harness; the cooking rule's producer class
	ModelProvider string
	// Bytes is the canonical plaintext, one copy per generation.
	Bytes []byte
	// Ranges holds one entry per partition, recorded by the structural pass.
	Ranges []partitionByteRange

	layout contentLayout
	cook   cookRowsFunc

	mu         sync.Mutex
	Partitions map[partitionKey]*partitionIndex // built lazily per partition and section
}

// openedTranscript pairs an index with a reader over its bytes.
type openedTranscript struct {
	Index *transcriptIndex
	Bytes turnBytesReader
}

// transcriptIndexInput is what the source needs about one generation.
type transcriptIndexInput struct {
	Revision      contentGeneration
	ContentHash   storage.ContentHash
	ModelProvider string
	Bytes         []byte
	// Cook is the cooking rule; nil selects the production rule.
	Cook cookRowsFunc
}

// newTranscriptIndex parses the structure of one canonical generation once and
// holds its bytes. It cooks nothing: partitions are indexed on first use. The
// input bytes are held, not copied.
func newTranscriptIndex(in transcriptIndexInput) (*transcriptIndex, error) {
	layout, err := scanContentLayout(in.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errUncookable, err)
	}
	cook := in.Cook
	if cook == nil {
		cook = schema.CookTranscriptRows
	}
	index := &transcriptIndex{
		Revision:      in.Revision,
		ContentHash:   in.ContentHash,
		Harness:       canonicalHarness(layout.Harness),
		ModelProvider: in.ModelProvider,
		Bytes:         in.Bytes,
		layout:        layout,
		cook:          cook,
		Partitions:    make(map[partitionKey]*partitionIndex),
	}
	index.Ranges = append(index.Ranges, partitionByteRange{Key: mainPartitionKey(), Start: layout.Main.Turns.Start, End: layout.Main.Turns.End})
	for section, history := range layout.History {
		index.Ranges = append(index.Ranges, partitionByteRange{Key: historyPartitionKey(section), Start: history.Turns.Start, End: history.Turns.End})
	}
	return index, nil
}

// SessionDetail is the served session detail object, a view into the held
// bytes. The caller must not modify it.
func (x *transcriptIndex) SessionDetail() json.RawMessage {
	return x.Bytes[x.layout.Detail.Start:x.layout.Detail.End]
}

// HistorySections is the number of earlier-history sections the generation
// records, in stored order.
func (x *transcriptIndex) HistorySections() int { return len(x.layout.History) }

// ReadTurnBytes returns the held bytes in [start, end) as a view. The range
// must lie inside the document.
func (x *transcriptIndex) ReadTurnBytes(ctx context.Context, start, end int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if start < 0 || end < start || end > int64(len(x.Bytes)) {
		return nil, fmt.Errorf("turn bytes were requested outside the held document in handler.transcriptIndex.ReadTurnBytes (start %d, end %d, held %d bytes); nothing was read; request a range the index recorded", start, end, len(x.Bytes))
	}
	return x.Bytes[start:end], nil
}

// Partition returns the index of one partition, building it from the held
// bytes on first use. The cooking pass runs once per partition for the life of
// the index; a failure is not remembered, so a later call repeats it and
// refuses the same way. Concurrent first calls build once.
func (x *transcriptIndex) Partition(key partitionKey) (*partitionIndex, error) {
	layout, ok := x.partitionLayout(key)
	if !ok {
		return nil, errPartitionAbsent
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if built, ok := x.Partitions[key]; ok {
		return built, nil
	}
	built, err := x.buildPartition(layout)
	if err != nil {
		return nil, err
	}
	x.Partitions[key] = built
	return built, nil
}

func (x *transcriptIndex) partitionLayout(key partitionKey) (partitionLayout, bool) {
	switch {
	case key == mainPartitionKey():
		return x.layout.Main, true
	case key.Kind == schema.TranscriptPartitionHistory && key.Section >= 0 && key.Section < len(x.layout.History):
		return x.layout.History[key.Section], true
	default:
		return partitionLayout{}, false
	}
}

// buildPartition is the entry-build pass for one partition. It captures the
// stored turns once, cooks them, and keeps only per-row facts.
func (x *transcriptIndex) buildPartition(layout partitionLayout) (*partitionIndex, error) {
	if !layout.Present {
		return &partitionIndex{Rows: []indexedTurn{}, Verdict: []schema.CookedTurn{}}, nil
	}
	array := x.Bytes[layout.Turns.Start:layout.Turns.End]
	// The capture takes one session-detail-shaped object. Only the partition's
	// own turn array goes into it, so a partition never pays for the rest of
	// the document.
	capture := make([]byte, 0, len(array)+len(`{"turns":}`))
	capture = append(capture, `{"turns":`...)
	capture = append(capture, array...)
	capture = append(capture, '}')
	turns, err := schema.NewTranscriptCookTurns(capture)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errUncookable, err)
	}
	verdict, err := x.cook(turns, x.Harness)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errUncookable, err)
	}
	if len(verdict) != len(turns) {
		return nil, fmt.Errorf("%w: the cooking rule returned %d verdicts for %d stored turns in handler.transcriptIndex.buildPartition", errUncookable, len(verdict), len(turns))
	}

	rows := make([]indexedTurn, 0, len(turns))
	cursor := layout.Turns.Start + 1 // after the array's opening bracket
	for stored, turn := range turns {
		served := turn.Raw()
		raw := served.Raw()
		start, end, next, err := x.locateTurn(cursor, raw, stored == len(turns)-1)
		if err != nil {
			return nil, fmt.Errorf("%w: stored turn %d: %w", errUncookable, stored, err)
		}
		cursor = next
		cooked := verdict[stored]
		if cooked.Position != stored {
			return nil, fmt.Errorf("%w: the cooking rule reported position %d for stored turn %d in handler.transcriptIndex.buildPartition", errUncookable, cooked.Position, stored)
		}
		if !cooked.Keep {
			if cooked.ServedPosition != -1 || cooked.DisplayNumber != "" {
				return nil, fmt.Errorf("%w: the cooking rule numbered the removed stored turn %d in handler.transcriptIndex.buildPartition", errUncookable, stored)
			}
			continue
		}
		if cooked.ServedPosition != len(rows) || cooked.DisplayNumber == "" {
			return nil, fmt.Errorf("%w: the cooking rule gave stored turn %d served position %d and number %q where position %d and a number were expected in handler.transcriptIndex.buildPartition", errUncookable, stored, cooked.ServedPosition, cooked.DisplayNumber, len(rows))
		}
		detail := turn.Detail()
		state, recorded := schema.DecodeTranscriptRecordedIndex(served)
		rows = append(rows, indexedTurn{
			Stored:             stored,
			ServedPosition:     cooked.ServedPosition,
			DisplayNumber:      cooked.DisplayNumber,
			Start:              start,
			End:                end,
			Role:               detail.Role,
			ConversationTurn:   schema.IsConversationTurn(detail.Role, detail.EntryType, detail.Depth),
			ByteLength:         schema.TranscriptTurnByteLength(detail),
			RecordedIndexState: state,
			RecordedIndex:      recorded,
		})
	}
	return &partitionIndex{Rows: rows, Verdict: verdict, TotalTurns: len(rows)}, nil
}

// locateTurn finds where the next stored turn lies in the held bytes. The
// capture decoded the array element by element, so the turn's own bytes are
// exactly the array element: this walk only skips the whitespace and commas
// between elements and checks that the bytes are those the capture returned.
func (x *transcriptIndex) locateTurn(cursor int64, raw []byte, last bool) (start, end, next int64, err error) {
	cursor = skipJSONSpace(x.Bytes, cursor)
	end = cursor + int64(len(raw))
	if end > int64(len(x.Bytes)) || !bytes.Equal(x.Bytes[cursor:end], raw) {
		return 0, 0, 0, errors.New("the captured turn is not where the structural pass expected it in handler.transcriptIndex.locateTurn; the document was not indexed; report this as a defect")
	}
	next = skipJSONSpace(x.Bytes, end)
	if next >= int64(len(x.Bytes)) {
		return 0, 0, 0, errors.New("the turn array ended early in handler.transcriptIndex.locateTurn; the document was not indexed; report this as a defect")
	}
	want := byte(',')
	if last {
		want = ']'
	}
	if x.Bytes[next] != want {
		return 0, 0, 0, errors.New("the turn array has an unexpected separator in handler.transcriptIndex.locateTurn; the document was not indexed; report this as a defect")
	}
	return cursor, end, next + 1, nil
}

func skipJSONSpace(b []byte, at int64) int64 {
	for at < int64(len(b)) {
		switch b[at] {
		case ' ', '\t', '\n', '\r':
			at++
		default:
			return at
		}
	}
	return at
}

// byteSpan is a half-open range of the held bytes.
type byteSpan struct{ Start, End int64 }

// partitionLayout locates one partition's turn array. Present is false when
// the document has no turn array for it (absent or null), an empty partition.
type partitionLayout struct {
	Turns   byteSpan
	Present bool
}

// contentLayout is what the structural pass learns about one generation.
type contentLayout struct {
	Detail  byteSpan
	Harness string
	Main    partitionLayout
	History []partitionLayout
}

// layoutScanner walks a document with a token decoder so that offsets are
// exact. Every value it does not need is skipped whole.
type layoutScanner struct {
	dec *json.Decoder
}

func scanContentLayout(document []byte) (contentLayout, error) {
	scanner := layoutScanner{dec: json.NewDecoder(bytes.NewReader(document))}
	layout, err := scanner.envelope()
	if err != nil {
		return contentLayout{}, fmt.Errorf("the stored document has no indexable structure in handler.scanContentLayout: %w; nothing was indexed; republish the transcript", err)
	}
	if _, err := scanner.dec.Token(); !errors.Is(err, io.EOF) {
		return contentLayout{}, errors.New("the stored document has content after its envelope in handler.scanContentLayout; nothing was indexed; republish the transcript")
	}
	return layout, nil
}

func (s *layoutScanner) envelope() (contentLayout, error) {
	var layout contentLayout
	seenDetail := false
	err := s.members(func(key string) error {
		if key != "sessionDetail" {
			_, err := s.skip()
			return err
		}
		if seenDetail {
			return errors.New("sessionDetail appears twice")
		}
		seenDetail = true
		span, err := s.detail(&layout)
		layout.Detail = span
		return err
	})
	if err != nil {
		return contentLayout{}, err
	}
	if !seenDetail {
		return contentLayout{}, errors.New("sessionDetail is absent")
	}
	return layout, nil
}

func (s *layoutScanner) detail(layout *contentLayout) (byteSpan, error) {
	seen := map[string]bool{}
	return s.object(func(key string) error {
		switch key {
		case "harness", "turns", "earlierHistory":
			if seen[key] {
				return fmt.Errorf("%s appears twice", key)
			}
			seen[key] = true
		default:
			_, err := s.skip()
			return err
		}
		switch key {
		case "harness":
			raw, err := s.skip()
			if err != nil {
				return err
			}
			var harness *string
			if err := json.Unmarshal(raw.bytes, &harness); err != nil {
				return errors.New("harness is not a string")
			}
			if harness != nil {
				layout.Harness = *harness
			}
		case "turns":
			partition, err := s.turnArray()
			layout.Main = partition
			return err
		case "earlierHistory":
			return s.history(layout)
		}
		return nil
	})
}

func (s *layoutScanner) history(layout *contentLayout) error {
	token, err := s.dec.Token()
	if err != nil {
		return fmt.Errorf("earlierHistory: %w", err)
	}
	if token == nil {
		return nil // null: no sections
	}
	if token != json.Delim('[') {
		return errors.New("earlierHistory is not an array")
	}
	for s.dec.More() {
		var partition partitionLayout
		seen := false
		if _, err := s.object(func(key string) error {
			if key != "turns" {
				_, err := s.skip()
				return err
			}
			if seen {
				return errors.New("a history section has two turn arrays")
			}
			seen = true
			var err error
			partition, err = s.turnArray()
			return err
		}); err != nil {
			return fmt.Errorf("earlierHistory[%d]: %w", len(layout.History), err)
		}
		layout.History = append(layout.History, partition)
	}
	return s.close(']')
}

// turnArray reads one value that must be an array or null.
func (s *layoutScanner) turnArray() (partitionLayout, error) {
	value, err := s.skip()
	if err != nil {
		return partitionLayout{}, err
	}
	trimmed := bytes.TrimSpace(value.bytes)
	if bytes.Equal(trimmed, []byte("null")) {
		return partitionLayout{}, nil
	}
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return partitionLayout{}, errors.New("turns is not an array")
	}
	return partitionLayout{Turns: value.span, Present: true}, nil
}

type skippedValue struct {
	bytes []byte
	span  byteSpan
}

// skip reads one whole value and reports where it lay. The value's own bytes
// are returned for the small members the scanner interprets; the large ones are
// dropped by the caller.
func (s *layoutScanner) skip() (skippedValue, error) {
	var raw json.RawMessage
	if err := s.dec.Decode(&raw); err != nil {
		return skippedValue{}, err
	}
	end := s.dec.InputOffset()
	return skippedValue{bytes: raw, span: byteSpan{Start: end - int64(len(raw)), End: end}}, nil
}

func (s *layoutScanner) open(delim json.Delim) (int64, error) {
	token, err := s.dec.Token()
	if err != nil {
		return 0, err
	}
	if token != delim {
		return 0, fmt.Errorf("expected %q", rune(delim))
	}
	return s.dec.InputOffset() - 1, nil
}

func (s *layoutScanner) close(delim json.Delim) error {
	token, err := s.dec.Token()
	if err != nil {
		return err
	}
	if token != delim {
		return fmt.Errorf("expected %q", rune(delim))
	}
	return nil
}

// members walks one object's members; fn consumes each member's value.
func (s *layoutScanner) members(fn func(key string) error) error {
	_, err := s.object(fn)
	return err
}

func (s *layoutScanner) object(fn func(key string) error) (byteSpan, error) {
	start, err := s.open('{')
	if err != nil {
		return byteSpan{}, fmt.Errorf("an object is required: %w", err)
	}
	for s.dec.More() {
		token, err := s.dec.Token()
		if err != nil {
			return byteSpan{}, err
		}
		key, ok := token.(string)
		if !ok {
			return byteSpan{}, errors.New("an object member name is required")
		}
		if err := fn(key); err != nil {
			return byteSpan{}, err
		}
	}
	if err := s.close('}'); err != nil {
		return byteSpan{}, err
	}
	return byteSpan{Start: start, End: s.dec.InputOffset()}, nil
}

// openTranscriptContent reads the generation row currently stores through the
// existing encrypted read and builds its content source. The read is
// readEncryptedTranscript: it authenticates the object against the row's
// identity, validates the content boundary, and reloads once when the object
// moved, asking authorize about the fresh row. Nothing is written: a stored
// shape that the whole-document read would rewrite is normalized in memory and
// left as stored.
func (h *Handler) openTranscriptContent(ctx context.Context, row sqlc.Transcript, authorize func(sqlc.Transcript) bool) (*openedTranscript, error) {
	read, err := h.readEncryptedTranscript(ctx, row, "", authorize)
	if err != nil {
		return nil, err
	}
	canonical, err := canonicalContentBytes(ctx, read.Plaintext)
	if err != nil {
		return nil, err
	}
	var hash storage.ContentHash
	if loaded, err := identityFromTranscript(read.Row); err == nil {
		if _, known := loaded.Known(); known {
			hash = read.Identity.Hash()
		}
	}
	index, err := newTranscriptIndex(transcriptIndexInput{
		Revision:      contentGeneration(read.Row.ContentRevision),
		ContentHash:   hash,
		ModelProvider: read.Row.ModelProvider,
		Bytes:         canonical,
	})
	if err != nil {
		return nil, err
	}
	return &openedTranscript{Index: index, Bytes: index}, nil
}

// canonicalContentBytes is the whole-document read's normalization without its
// write: the bytes GET /content would serve for this stored object, before the
// per-viewer repository withholding. A stored object that is already canonical
// is returned as it is. Anything older is migrated and re-encoded, falling back
// as the whole-document read falls back, but never persisted: installing a
// rewritten generation stays the whole-document read's decision.
func canonicalContentBytes(ctx context.Context, raw []byte) ([]byte, error) {
	payload, rewrite, err := defaultContentMigrator.Migrate(ctx, raw)
	if err != nil {
		return nil, err
	}
	if err := validateObservedModelValues(payload); err != nil {
		return nil, err
	}
	if err := validateThinkingLevelValues(payload); err != nil {
		return nil, err
	}
	if !rewrite {
		return raw, nil
	}
	encoded, encodeErr := encodeCanonicalTranscript(payload)
	switch {
	case encodeErr == nil:
		return encoded, nil
	case errors.Is(encodeErr, errCanonicalRewriteTooLarge):
		return originalContentEnvelope(raw)
	default:
		if payload.RetainedUnknown != nil || payload.Diagnostics != nil {
			return nil, errors.New("Stored retained transcript failed canonical validation during read normalization in handler.canonicalContentBytes; no content was indexed; republish valid canonical content and retry")
		}
		return marshalTranscriptContentEnvelope(currentContractVersion, payload)
	}
}
