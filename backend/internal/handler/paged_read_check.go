package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/peasant-labs/schema"
	schemaRoute "github.com/peasant-labs/schema/route"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// This file is the paged-read parity check: it reads one transcript through
// the whole-document read and through the paged reads, both served by the
// production handlers over the production content path, and compares what
// they serve. The expected rows are derived from the whole-document body with
// the contract's own functions (schema.CookTranscriptRows and
// schema.CutTranscriptSegments); the observed rows are what the directory and
// segment reads served. A finding names a kind, a partition and a position or
// ordinal. It never carries a turn, a ref, a hash or any other byte of
// content, so a report can be read, stored and pasted without disclosing
// anything the transcript holds.

// pagedReadFindingKind is the closed set of parity rule breaks.
type pagedReadFindingKind uint8

const (
	// findingRows: a served row is missing, extra, out of position, or its turn
	// differs from the whole-document turn at that served position.
	findingRows pagedReadFindingKind = iota + 1
	// findingRefSpan: a directory ref or a segment's own ordinal, served-position
	// range, byte length or conversation span differs from the contract cut.
	findingRefSpan
	// findingPage: the directory's page membership, page numbering or totals
	// differ from the paging rule.
	findingPage
	// findingRefusal: one read refused what the other served.
	findingRefusal
	// findingNumber: a served row's display number differs from the producer's.
	findingNumber

	findingKindCount // sentinel; not a kind
)

func allPagedReadFindingKinds() []pagedReadFindingKind {
	kinds := make([]pagedReadFindingKind, 0, int(findingKindCount)-1)
	for k := findingRows; k < findingKindCount; k++ {
		kinds = append(kinds, k)
	}
	return kinds
}

func (k pagedReadFindingKind) String() string {
	switch k {
	case findingRows:
		return "rows"
	case findingRefSpan:
		return "ref_span"
	case findingPage:
		return "page"
	case findingRefusal:
		return "refusal"
	case findingNumber:
		return "number"
	default:
		return fmt.Sprintf("pagedReadFindingKind(%d)", uint8(k))
	}
}

// pagedReadFinding is one content-free rule break. Position and Ordinal are
// -1 when they do not apply.
type pagedReadFinding struct {
	Kind      pagedReadFindingKind
	Partition partitionKey
	Ordinal   int
	Position  int
}

func partitionLabel(key partitionKey) string {
	if key.Kind == schema.TranscriptPartitionHistory {
		return "history:" + strconv.Itoa(key.Section)
	}
	return "main"
}

// observedPartition is what the paged reads served for one partition.
type observedPartition struct {
	Key             partitionKey
	DirectoryStatus int
	// Pages are the directory pages in request order, page 1 first.
	Pages []schema.PositionedDirectory
	// SegmentStatus and Segments are keyed by ordinal.
	SegmentStatus map[int]int
	Segments      map[int]schema.PositionedSegment
}

// pagedReadObservation is one transcript read both ways. Content is held only
// while it is compared.
type pagedReadObservation struct {
	ContentStatus int
	Content       []byte
	Partitions    []observedPartition
}

// expectedPartition is what the contract rules say the paged reads must serve
// for one partition of the whole-document body.
type expectedPartition struct {
	Key     partitionKey
	Refused bool // the cooking rule refuses this partition
	Turns   []json.RawMessage
	Numbers []string
	Refs    []schema.PositionedSegmentRef
}

// comparePagedReads compares one observation and returns every rule break.
// directoryCap is the paging cap, schema.MaxTranscriptDirectoryRefs in
// production.
func comparePagedReads(obs pagedReadObservation, directoryCap int) []pagedReadFinding {
	var findings []pagedReadFinding
	add := func(kind pagedReadFindingKind, key partitionKey, ordinal, position int) {
		findings = append(findings, pagedReadFinding{Kind: kind, Partition: key, Ordinal: ordinal, Position: position})
	}
	main := mainPartitionKey()
	if obs.ContentStatus != http.StatusOK {
		// The whole-document read refused. Every paged read must refuse too.
		for _, part := range obs.Partitions {
			if part.DirectoryStatus == http.StatusOK {
				add(findingRefusal, part.Key, -1, -1)
			}
		}
		if len(obs.Partitions) == 0 {
			add(findingRefusal, main, -1, -1)
		}
		return findings
	}
	expected, err := expectedPartitions(obs.Content)
	if err != nil {
		// The whole-document read served a body the paged reads cannot be
		// compared with. That is a refusal mismatch only if the paged reads
		// served; otherwise both refused the same document.
		for _, part := range obs.Partitions {
			if part.DirectoryStatus == http.StatusOK {
				add(findingRefusal, part.Key, -1, -1)
			}
		}
		return findings
	}
	observed := make(map[partitionKey]observedPartition, len(obs.Partitions))
	for _, part := range obs.Partitions {
		observed[part.Key] = part
	}
	for _, want := range expected {
		got, ok := observed[want.Key]
		if !ok {
			add(findingRefusal, want.Key, -1, -1)
			continue
		}
		delete(observed, want.Key)
		findings = append(findings, comparePartition(want, got, directoryCap)...)
	}
	for _, extra := range obs.Partitions {
		if _, left := observed[extra.Key]; left && extra.DirectoryStatus == http.StatusOK {
			add(findingRows, extra.Key, -1, -1)
		}
	}
	return findings
}

func comparePartition(want expectedPartition, got observedPartition, directoryCap int) []pagedReadFinding {
	var findings []pagedReadFinding
	add := func(kind pagedReadFindingKind, ordinal, position int) {
		findings = append(findings, pagedReadFinding{Kind: kind, Partition: want.Key, Ordinal: ordinal, Position: position})
	}
	if want.Refused {
		// The cooking rule refuses this partition. A directory is still
		// served only when it does not need rows; anything served is a break.
		if got.DirectoryStatus == http.StatusOK {
			add(findingRefusal, -1, -1)
		}
		return findings
	}
	if got.DirectoryStatus != http.StatusOK || len(got.Pages) == 0 {
		add(findingRefusal, -1, -1)
		return findings
	}

	// Paging: page p holds refs [(p-1)*cap, min(p*cap, total)), numbered,
	// and a directory at or under the cap is one page with no page members.
	total := len(want.Refs)
	pages := 1
	if total > directoryCap {
		pages = (total-1)/directoryCap + 1
	}
	if len(got.Pages) != pages {
		add(findingPage, -1, -1)
	}
	var served []schema.PositionedSegmentRef
	for i, page := range got.Pages {
		number := i + 1
		start := (number - 1) * directoryCap
		end := min(start+directoryCap, total)
		if total <= directoryCap {
			start, end = 0, total
		}
		if page.Context.TotalSegments != total {
			add(findingPage, -1, -1)
		}
		if total <= directoryCap {
			if page.DirectoryPage != nil || page.DirectoryPerPage != nil {
				add(findingPage, -1, -1)
			}
		} else if page.DirectoryPage == nil || *page.DirectoryPage != number || page.DirectoryPerPage == nil || *page.DirectoryPerPage != directoryCap {
			add(findingPage, -1, -1)
		}
		if start > end || len(page.Segments) != end-start {
			add(findingPage, -1, -1)
		}
		for j, ref := range page.Segments {
			if ref.Index != start+j {
				add(findingPage, ref.Index, -1)
				break
			}
		}
		served = append(served, page.Segments...)
	}

	// Ref spans: every ref the directory served, matched by its ordinal.
	for _, ref := range served {
		if ref.Index < 0 || ref.Index >= total {
			add(findingRefSpan, ref.Index, -1)
			continue
		}
		if !sameRefSpan(ref, want.Refs[ref.Index]) {
			add(findingRefSpan, ref.Index, -1)
		}
	}

	// Rows and numbers: the segments, in ordinal order, must serve every kept
	// row exactly once at its served position.
	next := 0
	for ordinal, ref := range want.Refs {
		status, read := got.SegmentStatus[ordinal]
		segment, ok := got.Segments[ordinal]
		if !read || status != http.StatusOK || !ok {
			add(findingRefusal, ordinal, -1)
			next = ref.SourceLastPosition + 1
			continue
		}
		if segment.Index != ordinal || segment.ByteLength != ref.ByteLength {
			add(findingRefSpan, ordinal, -1)
		}
		if len(segment.Rows) != ref.SourceLastPosition-ref.SourceFirstPosition+1 {
			add(findingRows, ordinal, -1)
		}
		for _, row := range segment.Rows {
			if row.Position != next || next >= len(want.Turns) {
				add(findingRows, ordinal, row.Position)
				next = row.Position + 1
				continue
			}
			if !sameJSON(row.Turn.Raw(), want.Turns[next]) {
				add(findingRows, ordinal, next)
			}
			if row.DisplayNumber != want.Numbers[next] {
				add(findingNumber, ordinal, next)
			}
			next++
		}
	}
	if next != len(want.Turns) {
		add(findingRows, -1, next)
	}
	return findings
}

func sameRefSpan(a, b schema.PositionedSegmentRef) bool {
	return a.Index == b.Index && a.SourceFirstPosition == b.SourceFirstPosition &&
		a.SourceLastPosition == b.SourceLastPosition && a.ByteLength == b.ByteLength &&
		reflect.DeepEqual(a.ConversationTurns, b.ConversationTurns)
}

// sameJSON is semantic equality of two JSON values: the paged read serves the
// stored turn bytes, and the whole-document read may have re-encoded them.
func sameJSON(a, b []byte) bool {
	if bytes.Equal(a, b) {
		return true
	}
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// expectedPartitions derives, from a whole-document body, what each partition's
// paged reads must serve: the cook's kept rows with their numbers, and the cut
// of those rows mapped to served positions.
func expectedPartitions(content []byte) ([]expectedPartition, error) {
	var envelope struct {
		SessionDetail json.RawMessage `json:"sessionDetail"`
	}
	if err := json.Unmarshal(content, &envelope); err != nil || len(envelope.SessionDetail) == 0 {
		return nil, errors.New("the whole-document body has no session detail in handler.expectedPartitions; the paged reads cannot be compared with it")
	}
	var detail struct {
		Harness        *string           `json:"harness"`
		Turns          json.RawMessage   `json:"turns"`
		EarlierHistory []json.RawMessage `json:"earlierHistory"`
	}
	if err := json.Unmarshal(envelope.SessionDetail, &detail); err != nil {
		return nil, errors.New("the whole-document session detail could not be read in handler.expectedPartitions; the paged reads cannot be compared with it")
	}
	harness := schema.Harness("")
	if detail.Harness != nil {
		harness = canonicalHarness(*detail.Harness)
	}
	out := []expectedPartition{expectPartition(mainPartitionKey(), detail.Turns, harness)}
	for i, section := range detail.EarlierHistory {
		var history struct {
			Turns json.RawMessage `json:"turns"`
		}
		if err := json.Unmarshal(section, &history); err != nil {
			return nil, errors.New("a whole-document history section could not be read in handler.expectedPartitions; the paged reads cannot be compared with it")
		}
		out = append(out, expectPartition(historyPartitionKey(i), history.Turns, harness))
	}
	return out, nil
}

func expectPartition(key partitionKey, turns json.RawMessage, harness schema.Harness) expectedPartition {
	want := expectedPartition{Key: key}
	trimmed := bytes.TrimSpace(turns)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		want.Turns, want.Numbers, want.Refs = []json.RawMessage{}, []string{}, []schema.PositionedSegmentRef{}
		return want
	}
	capture := append(append([]byte(`{"turns":`), trimmed...), '}')
	cookTurns, err := schema.NewTranscriptCookTurns(capture)
	if err != nil {
		want.Refused = true
		return want
	}
	verdict, err := schema.CookTranscriptRows(cookTurns, harness)
	if err != nil || len(verdict) != len(cookTurns) {
		want.Refused = true
		return want
	}
	var details []schema.TurnDetail
	for stored, turn := range cookTurns {
		if !verdict[stored].Keep {
			continue
		}
		want.Turns = append(want.Turns, turn.Raw().Raw())
		want.Numbers = append(want.Numbers, verdict[stored].DisplayNumber)
		details = append(details, turn.Detail())
	}
	cuts := schema.CutTranscriptSegments(details, schema.TranscriptSegmentBudgetBytes)
	next := 0
	for _, cut := range cuts {
		first, size := next, 0
		for next < len(details) && size < cut.ByteLength {
			size += schema.TranscriptTurnByteLength(details[next])
			next++
		}
		if size != cut.ByteLength || next == first {
			want.Refused = true
			return want
		}
		want.Refs = append(want.Refs, schema.PositionedSegmentRef{Index: cut.Index, SourceFirstPosition: first, SourceLastPosition: next - 1, ByteLength: cut.ByteLength, ConversationTurns: cut.ConversationTurns})
	}
	if want.Turns == nil {
		want.Turns, want.Numbers, want.Refs = []json.RawMessage{}, []string{}, []schema.PositionedSegmentRef{}
	}
	return want
}

// capturedResponse is an in-memory response for an in-process read.
type capturedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (c *capturedResponse) Header() http.Header {
	if c.header == nil {
		c.header = http.Header{}
	}
	return c.header
}

func (c *capturedResponse) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
}

func (c *capturedResponse) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	return c.body.Write(b)
}

// pagedReadRoutes mounts the production read handlers at their production
// patterns, relative to the API prefix, for in-process reads.
func (h *Handler) pagedReadRoutes() http.Handler {
	mux := chi.NewRouter()
	for _, mount := range []struct {
		route   schemaRoute.Route
		handler http.HandlerFunc
	}{
		{schemaRoute.VillageGetTranscriptSegments, h.GetTranscriptSegments},
		{schemaRoute.VillageGetTranscriptSegment, h.GetTranscriptSegment},
		{schemaRoute.VillageGetTranscriptWindow, h.GetTranscriptWindow},
	} {
		mux.Get(MountPattern(mount.route), mount.handler)
	}
	return mux
}

// pagedReadMaxSegments bounds the segment reads of one observed partition, so
// a corrupt directory cannot drive an unbounded loop. It is far above any
// transcript the publish cap admits.
const pagedReadMaxSegments = 1 << 16

var errPagedReadBusy = errors.New("paged read remained busy until the check deadline")

// observePagedReads reads one transcript as viewer (nil is anonymous) through
// the whole-document read and every paged read the reader would need: every
// directory page and every segment of every partition.
func (h *Handler) observePagedReads(ctx context.Context, routes http.Handler, id uuid.UUID, viewer *AuthUser) (pagedReadObservation, error) {
	busy := false
	get := func(path string, query url.Values) *capturedResponse {
		target := pagedReadPath(id, path)
		if len(query) > 0 {
			target += "?" + query.Encode()
		}
		for {
			r, _ := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
			if viewer != nil {
				r = r.WithContext(context.WithValue(r.Context(), UserContextKey, viewer))
			}
			w := &capturedResponse{}
			routes.ServeHTTP(w, r)
			if w.status == 0 {
				w.status = http.StatusOK
			}
			if w.status != http.StatusServiceUnavailable {
				return w
			}
			seconds, err := strconv.Atoi(w.Header().Get("Retry-After"))
			if err != nil || seconds < 1 {
				seconds = 1
			}
			timer := time.NewTimer(time.Duration(seconds) * time.Second)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				busy = true
				return w
			}
		}
	}
	var obs pagedReadObservation
	obs.ContentStatus, obs.Content = h.observeContentWithoutWrites(ctx, id, viewer)
	main, revision, history, err := h.observePartition(ctx, get, mainPartitionKey(), "")
	if busy {
		return obs, errPagedReadBusy
	}
	if err != nil {
		return obs, err
	}
	obs.Partitions = append(obs.Partitions, main)
	for section := 0; section < history; section++ {
		part, _, _, err := h.observePartition(ctx, get, historyPartitionKey(section), revision)
		if busy {
			return obs, errPagedReadBusy
		}
		if err != nil {
			return obs, err
		}
		obs.Partitions = append(obs.Partitions, part)
	}
	return obs, nil
}

// observeContentWithoutWrites uses the same authorization, decryption, body
// preparation and owner projection as the content route, without read repair.
func (h *Handler) observeContentWithoutWrites(ctx context.Context, id uuid.UUID, viewer *AuthUser) (int, []byte) {
	row, err := h.queries.GetTranscriptByID(ctx, toPgUUID(id))
	if err != nil || !h.canViewTranscript(ctx, viewer, row) {
		return http.StatusNotFound, nil
	}
	read, err := h.readEncryptedTranscript(ctx, row, "", func(fresh sqlc.Transcript) bool {
		return h.canViewTranscript(ctx, viewer, fresh)
	})
	if err != nil {
		return http.StatusInternalServerError, nil
	}
	prepared, err := contentResponseBody(ctx, read.Plaintext)
	if err != nil {
		return http.StatusInternalServerError, nil
	}
	owner, err := h.resolvePublicOwner(ctx, viewer, row.OwnerID)
	if err != nil {
		return http.StatusInternalServerError, nil
	}
	if !owner.IsProfile() {
		prepared.body, err = withholdContentRepository(prepared.body)
		if err != nil {
			return http.StatusInternalServerError, nil
		}
	}
	return http.StatusOK, prepared.body
}

func pagedReadPath(id uuid.UUID, suffix string) string {
	return "/transcripts/" + id.String() + suffix
}

func (h *Handler) observePartition(ctx context.Context, get func(string, url.Values) *capturedResponse, key partitionKey, revision string) (observedPartition, string, int, error) {
	part := observedPartition{Key: key, SegmentStatus: map[int]int{}, Segments: map[int]schema.PositionedSegment{}}
	query := func() url.Values {
		q := url.Values{"v": {strconv.Itoa(positionedReadSelector())}}
		if key.Kind == schema.TranscriptPartitionHistory {
			q.Set("partition", string(schema.TranscriptPartitionHistory))
			q.Set("section", strconv.Itoa(key.Section))
		}
		if revision != "" {
			q.Set("revision", revision)
		}
		return q
	}
	first := get("/segments", query())
	if first.status == http.StatusServiceUnavailable {
		return part, revision, 0, errPagedReadBusy
	}
	part.DirectoryStatus = first.status
	if first.status != http.StatusOK {
		return part, revision, 0, nil
	}
	var page schema.PositionedDirectory
	if err := json.Unmarshal(first.body.Bytes(), &page); err != nil {
		return part, revision, 0, fmt.Errorf("the directory of one partition could not be decoded in handler.observePartition; the check cannot continue for this transcript: %w", err)
	}
	part.Pages = append(part.Pages, page)
	if revision == "" {
		revision = page.Context.Revision
	}
	history := len(page.History)
	if page.DirectoryPage != nil && page.DirectoryPerPage != nil && *page.DirectoryPerPage > 0 {
		pages := (page.Context.TotalSegments-1) / *page.DirectoryPerPage + 1
		for number := 2; number <= pages && number <= pagedReadMaxSegments; number++ {
			q := query()
			q.Set("page", strconv.Itoa(number))
			next := get("/segments", q)
			if next.status == http.StatusServiceUnavailable {
				return part, revision, history, errPagedReadBusy
			}
			if next.status != http.StatusOK {
				part.DirectoryStatus = next.status
				break
			}
			var more schema.PositionedDirectory
			if err := json.Unmarshal(next.body.Bytes(), &more); err != nil {
				return part, revision, history, fmt.Errorf("a directory page could not be decoded in handler.observePartition; the check cannot continue for this transcript: %w", err)
			}
			part.Pages = append(part.Pages, more)
		}
	}
	total := min(page.Context.TotalSegments, pagedReadMaxSegments)
	for ordinal := 0; ordinal < total; ordinal++ {
		if err := ctx.Err(); err != nil {
			return part, revision, history, err
		}
		response := get("/segments/"+strconv.Itoa(ordinal), query())
		if response.status == http.StatusServiceUnavailable {
			return part, revision, history, errPagedReadBusy
		}
		part.SegmentStatus[ordinal] = response.status
		if response.status != http.StatusOK {
			continue
		}
		var segment schema.PositionedSegment
		if err := json.Unmarshal(response.body.Bytes(), &segment); err != nil {
			return part, revision, history, fmt.Errorf("a segment could not be decoded in handler.observePartition; the check cannot continue for this transcript: %w", err)
		}
		part.Segments[ordinal] = segment
	}
	return part, revision, history, nil
}

// positionedReadSelector is the protocol selector the contract assigns to the
// positioned read version.
func positionedReadSelector() int {
	for _, generation := range schema.SegmentedReadGenerations() {
		if version, ok := schema.SegmentedReadProtocolVersion(generation); ok && version == schema.SegmentedReadAPIVersion {
			return generation
		}
	}
	return 0
}

// pagedReadSummary counts one subject's findings by kind.
type pagedReadSummary struct {
	Counts [findingKindCount]int
}

func summarizeFindings(findings []pagedReadFinding) pagedReadSummary {
	var s pagedReadSummary
	for _, f := range findings {
		s.Counts[f.Kind]++
	}
	return s
}

func (s pagedReadSummary) Clean() bool {
	for _, n := range s.Counts {
		if n != 0 {
			return false
		}
	}
	return true
}
