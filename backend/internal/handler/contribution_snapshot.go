package handler

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// GET /api/v1/collectives/{id}/contributable?view=snapshot&project_hash=...
// (and the deprecated /groups alias) serves the exact selection set of one
// project as compact records: every session of every family whose root is in the
// project, one record per session. Which families those are is defined once, in
// contribution_family.go.
//
// There is no record cap: the answer is the whole set or nothing. The encoded
// body is limited to the contract's schema.VillageContributionSnapshotMaxBytes
// (8 MiB), and time and concurrency bound the cost. A request refused by any of
// the three says so with a code the client can act on:
//
//	413 snapshot_too_large  the whole set would encode past the byte limit
//	503 snapshot_timeout    the read outlived its time bound
//	503 snapshot_busy       too many snapshots are being built right now
//
// A snapshot over the byte limit is refused whole, never truncated: the client
// narrows the search with q or provider and reads it again.
//
// One snapshot is one read-only REPEATABLE READ transaction, so the records,
// their statuses and their parents all describe a single moment even while
// publishes and shares commit around it. The database work finishes, and the
// connection and the concurrency slots are returned, BEFORE the first byte is
// written to the client, so a slow client holds neither.

const (
	// defaultContributionSnapshotTimeout is the time bound when none is
	// configured. Measured reads take about 0.3 s for twelve thousand sessions
	// and 3 to 6 s for a quarter of a million.
	defaultContributionSnapshotTimeout = 15 * time.Second
	// contributionSnapshotQueueWait is how long a snapshot waits for a free
	// slot or a free connection before it answers snapshot_busy.
	contributionSnapshotQueueWait = 2 * time.Second
	// contributionSnapshotLockTimeout bounds waiting on a lock inside the read.
	// A read-only snapshot takes no row locks, so this is a backstop.
	contributionSnapshotLockTimeout = 2 * time.Second
	// contributionSnapshotPerViewer is how many snapshots one person may have
	// in flight. A second open page of the same person queues behind the first.
	contributionSnapshotPerViewer = 1

	contributionSnapshotTimeoutCode = "snapshot_timeout"
	contributionSnapshotBusyCode    = "snapshot_busy"
)

// The contributableView constants are the values of the view query
// parameter the contributable route accepts.
const (
	contributableViewGrouped  = "grouped"
	contributableViewPaged    = "paged"
	contributableViewSnapshot = "snapshot"
)

// contributionSnapshotControl is the handler's snapshot state. The zero value
// is ready for use; the two function-like fields are observation seams for tests,
// like discoveryReadBarrier, and production leaves them unset.
type contributionSnapshotControl struct {
	gate contributionSnapshotGate

	// afterSnapshot, when non-nil, runs inside the snapshot transaction right
	// after the snapshot is fixed and before anything is read. Tests use it to
	// commit competing writes, or to hold a snapshot open. The same statements
	// run whether or not it is set.
	afterSnapshot func(context.Context)

	// queueWait overrides contributionSnapshotQueueWait when positive.
	queueWait time.Duration

	// maxBytes overrides schema.VillageContributionSnapshotMaxBytes when
	// positive, so a test can reach the limit without seeding 8 MiB.
	maxBytes int
}

func (c *contributionSnapshotControl) byteLimit() int {
	if c.maxBytes > 0 {
		return c.maxBytes
	}
	return schema.VillageContributionSnapshotMaxBytes
}

func (c *contributionSnapshotControl) wait() time.Duration {
	if c.queueWait > 0 {
		return c.queueWait
	}
	return contributionSnapshotQueueWait
}

// contributionSnapshotDeadline is the time bound for one snapshot.
func contributionSnapshotDeadline(configured time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	return defaultContributionSnapshotTimeout
}

// contributionSnapshotCapacity is how many snapshots this process builds at
// once. Unset, it is a quarter of the database pool, at least 1 and at most 4,
// so snapshots can never take the whole pool from ordinary traffic.
func contributionSnapshotCapacity(configured int, poolMax int32) int {
	if configured > 0 {
		return configured
	}
	capacity := int(poolMax) / 4
	if capacity > 4 {
		capacity = 4
	}
	if capacity < 1 {
		capacity = 1
	}
	return capacity
}

// ---------------------------------------------------------------------------
// The concurrency gate
// ---------------------------------------------------------------------------

// contributionSnapshotGate bounds in-flight snapshots per viewer and per
// process. Its zero value is usable; the instance bound is fixed by the first
// acquire. Slots are taken BEFORE a database connection, so a burst of snapshots
// queues here instead of exhausting the pool.
type contributionSnapshotGate struct {
	mu       sync.Mutex
	instance chan struct{}
	viewers  map[uuid.UUID]*viewerSlot
}

type viewerSlot struct {
	slot chan struct{}
	refs int
}

// acquire takes the viewer's slot and one instance slot, waiting at most wait
// (and never past ctx). It reports false when either is unavailable, holding
// nothing. The returned release is idempotent.
func (g *contributionSnapshotGate) acquire(ctx context.Context, viewer uuid.UUID, capacity int, wait time.Duration) (func(), bool) {
	if capacity < 1 {
		capacity = 1
	}
	waitCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	g.mu.Lock()
	if g.instance == nil {
		g.instance = make(chan struct{}, capacity)
	}
	if g.viewers == nil {
		g.viewers = map[uuid.UUID]*viewerSlot{}
	}
	instance := g.instance
	vs := g.viewers[viewer]
	if vs == nil {
		vs = &viewerSlot{slot: make(chan struct{}, contributionSnapshotPerViewer)}
		g.viewers[viewer] = vs
	}
	vs.refs++
	g.mu.Unlock()

	forget := func() {
		g.mu.Lock()
		vs.refs--
		if vs.refs == 0 {
			delete(g.viewers, viewer)
		}
		g.mu.Unlock()
	}

	select {
	case vs.slot <- struct{}{}:
	case <-waitCtx.Done():
		forget()
		return nil, false
	}
	select {
	case instance <- struct{}{}:
	case <-waitCtx.Done():
		<-vs.slot
		forget()
		return nil, false
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			<-instance
			<-vs.slot
			forget()
		})
	}, true
}

// ---------------------------------------------------------------------------
// Error classification and the code-bearing refusal
// ---------------------------------------------------------------------------

// classifyContributionSnapshotError reports whether err is a time bound being
// hit: the deadline, the statement timeout (57014) or the lock timeout (55P03).
func classifyContributionSnapshotError(err error) (code string, ok bool) {
	if err == nil {
		return "", false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return contributionSnapshotTimeoutCode, true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "57014" || pgErr.Code == "55P03") {
		return contributionSnapshotTimeoutCode, true
	}
	return "", false
}

// writeErrorCode writes an error that also carries a machine-readable code, the
// shape the contract's error response declares.
func writeErrorCode(w http.ResponseWriter, status int, msg, code string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": code})
}

func writeSnapshotBusy(w http.ResponseWriter) {
	writeErrorCode(w, http.StatusServiceUnavailable,
		"Village is already building as many contribution snapshots as it allows (or you already have one running), so "+
			"this one was not started. Nothing was changed. Wait a moment, then retry.",
		contributionSnapshotBusyCode)
}

func writeSnapshotTooLarge(w http.ResponseWriter, sessions, limit int) {
	writeErrorCode(w, http.StatusRequestEntityTooLarge, fmt.Sprintf(
		"this snapshot holds %d sessions, which encode to more than the %s limit of one snapshot, so no snapshot was "+
			"returned and nothing partial was sent. Nothing was changed. Narrow the search with q or provider, then "+
			"read the snapshot again.", sessions, formatByteLimit(limit)),
		string(schema.VillageContributionErrorSnapshotTooLarge))
}

// formatByteLimit names a byte limit the way the contract states it: in MiB
// when it is a whole number of them, otherwise in bytes.
func formatByteLimit(n int) string {
	if n >= 1<<20 && n%(1<<20) == 0 {
		return fmt.Sprintf("%d MiB", n>>20)
	}
	return fmt.Sprintf("%d-byte", n)
}

// errSnapshotTooLarge stops a sizing pass as soon as the encoding passes the
// limit.
var errSnapshotTooLarge = errors.New("the contribution snapshot encodes past its byte limit")

// limitCounter is an io.Writer that only counts, and fails once the count
// passes max. Sizing a snapshot through the same encoder that sends it keeps the
// limit exact, and an oversized set costs at most max bytes of encoding.
type limitCounter struct {
	n, max int
}

func (c *limitCounter) Write(p []byte) (int, error) {
	c.n += len(p)
	if c.n > c.max {
		return 0, errSnapshotTooLarge
	}
	return len(p), nil
}

// fits reports whether the encoded snapshot is at most max bytes.
func (s *contributionSnapshotSet) fits(groupID, projectHash string, max int) (bool, error) {
	err := s.writeJSON(&limitCounter{max: max}, groupID, projectHash)
	if errors.Is(err, errSnapshotTooLarge) {
		return false, nil
	}
	return err == nil, err
}

func writeSnapshotTimeout(w http.ResponseWriter, limit time.Duration) {
	writeErrorCode(w, http.StatusServiceUnavailable, fmt.Sprintf(
		"reading this project's contribution took longer than the %s limit, so no snapshot was returned. Nothing was "+
			"changed. Retry in a moment; if it keeps happening, narrow the search with q or provider, or ask the "+
			"operator to raise CONTRIBUTION_SNAPSHOT_TIMEOUT.", limit),
		contributionSnapshotTimeoutCode)
}

// ---------------------------------------------------------------------------
// The compact record set and its streaming encoder
// ---------------------------------------------------------------------------

// textInterner stores each distinct string once. Lookups by []byte allocate
// nothing, so scanning a quarter of a million rows does not allocate a string
// per row for the handful of distinct projects, visibilities and statuses.
type textInterner struct {
	index  map[string]uint32
	values []string
}

func (in *textInterner) intern(b []byte) uint32 {
	if i, ok := in.index[string(b)]; ok {
		return i
	}
	if in.index == nil {
		in.index = map[string]uint32{}
	}
	s := string(b)
	i := uint32(len(in.values))
	in.values = append(in.values, s)
	in.index[s] = i
	return i
}

// snapshotRecord is one session in 48 bytes: two ids and three indexes into the
// interned text. A status index of 0 means no attempt; otherwise it is the
// interned index plus one.
type snapshotRecord struct {
	id        [16]byte
	parent    [16]byte
	project   uint32
	visible   uint32
	status    uint32
	hasParent bool
}

// contributionSnapshotSet is the whole answer, held compactly until it is
// encoded. The zero value is empty and ready.
type contributionSnapshotSet struct {
	records      []snapshotRecord
	projects     textInterner
	visibilities textInterner
	statuses     textInterner
}

func (s *contributionSnapshotSet) Len() int { return len(s.records) }

// addRecord appends one session. A nil status means the session has no attempt
// in the collective.
func (s *contributionSnapshotSet) addRecord(id, parent [16]byte, hasParent bool, project, visibility, status []byte) {
	record := snapshotRecord{
		id:        id,
		parent:    parent,
		hasParent: hasParent,
		project:   s.projects.intern(project),
		visible:   s.visibilities.intern(visibility),
	}
	if status != nil {
		record.status = s.statuses.intern(status) + 1
	}
	s.records = append(s.records, record)
}

func appendUUID(dst []byte, id [16]byte) []byte {
	const hexDigits = "0123456789abcdef"
	for i, b := range id {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			dst = append(dst, '-')
		}
		dst = append(dst, hexDigits[b>>4], hexDigits[b&0x0f])
	}
	return dst
}

func quotedStrings(values []string) [][]byte {
	quoted := make([][]byte, len(values))
	for i, v := range values {
		quoted[i], _ = json.Marshal(v)
	}
	return quoted
}

// writeJSON streams the snapshot response, sorted by session id, through a small
// buffer: nothing proportional to the answer is built besides the compact set.
func (s *contributionSnapshotSet) writeJSON(w io.Writer, groupID, projectHash string) error {
	slices.SortFunc(s.records, func(a, b snapshotRecord) int { return bytes.Compare(a.id[:], b.id[:]) })
	projects := quotedStrings(s.projects.values)
	visibilities := quotedStrings(s.visibilities.values)
	statuses := quotedStrings(s.statuses.values)

	bw := bufio.NewWriterSize(w, 64<<10)
	group, _ := json.Marshal(groupID)
	project, _ := json.Marshal(projectHash)
	head := make([]byte, 0, 256)
	head = append(head, `{"group_id":`...)
	head = append(head, group...)
	head = append(head, `,"project_hash":`...)
	head = append(head, project...)
	head = append(head, `,"count":`...)
	head = strconv.AppendInt(head, int64(len(s.records)), 10)
	head = append(head, `,"records":[`...)
	if _, err := bw.Write(head); err != nil {
		return err
	}

	line := make([]byte, 0, 512)
	for i := range s.records {
		r := &s.records[i]
		line = line[:0]
		if i > 0 {
			line = append(line, ',')
		}
		line = append(line, `{"id":"`...)
		line = appendUUID(line, r.id)
		line = append(line, `","visibility":`...)
		line = append(line, visibilities[r.visible]...)
		if r.status != 0 {
			line = append(line, `,"latest_status":`...)
			line = append(line, statuses[r.status-1]...)
		}
		line = append(line, `,"project_hash":`...)
		line = append(line, projects[r.project]...)
		if r.hasParent {
			line = append(line, `,"parent_transcript_id":"`...)
			line = appendUUID(line, r.parent)
			line = append(line, '"')
		}
		line = append(line, '}')
		if _, err := bw.Write(line); err != nil {
			return err
		}
	}
	if _, err := bw.WriteString("]}\n"); err != nil {
		return err
	}
	return bw.Flush()
}

// rawUUID reads a uuid result value in either wire format: 16 bytes in binary,
// 36 characters in text.
func rawUUID(b []byte) ([16]byte, bool) {
	var id [16]byte
	switch len(b) {
	case 16:
		copy(id[:], b)
		return id, true
	case 36:
		parsed, err := uuid.ParseBytes(b)
		if err != nil {
			return id, false
		}
		return parsed, true
	}
	return id, false
}

// ---------------------------------------------------------------------------
// Request handling
// ---------------------------------------------------------------------------

// snapshotParams is a validated snapshot request.
type snapshotParams struct {
	projectHash string
	filter      contributionFamilyFilter
}

// parseSnapshotParams reads the parameters a snapshot uses: project_hash, q and
// provider (view is the dispatcher's). Every other parameter is ignored, among
// them limit and cursor: a snapshot has no page, cursor or record limit, it is
// always the whole project (or a 413 when that would pass the byte limit).
func parseSnapshotParams(query url.Values) (snapshotParams, string) {
	if refusal := refuseRepeatedParameters(query, "project_hash"); refusal != "" {
		return snapshotParams{}, refusal
	}
	search, refusal := parseContributionSearch(query)
	if refusal != "" {
		return snapshotParams{}, refusal
	}
	hash := query.Get("project_hash")
	if hash == "" {
		return snapshotParams{}, "project_hash is required with view=snapshot: name the project whose sessions you want, as 64 " +
			"lowercase hexadecimal characters exactly as a transcript reports it. Nothing was read."
	}
	if !projectHashPattern.MatchString(hash) {
		return snapshotParams{}, fmt.Sprintf("project_hash %q is not a project hash: a project is identified by exactly 64 lowercase "+
			"hexadecimal characters. Nothing was read.", hash)
	}
	return snapshotParams{
		projectHash: hash,
		filter:      contributionFamilyFilter{Query: search.Query, Provider: search.Provider},
	}, ""
}

// listContributionSnapshot serves view=snapshot.
func (h *Handler) listContributionSnapshot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := GetUser(ctx)

	groupID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"the collective id %q in this address is not a valid identifier, so there is no collective to answer for. "+
				"Nothing was read. Open the collective from its own page and retry from there.", chi.URLParam(r, "id")))
		return
	}
	pgGroupID := toPgUUID(groupID)

	// The same gate, and the same answer for a collective that does not exist
	// and one the caller is not in, as the flat listing.
	if _, err := h.queries.GetGroupMember(ctx, sqlc.GetGroupMemberParams{GroupID: pgGroupID, UserID: user.PgID()}); err != nil {
		writeError(w, http.StatusForbidden, fmt.Sprintf(
			"you are not a member of collective %s, so Village will not list what you could contribute to it. Nothing "+
				"was read. Join the collective first, then open its contribute page.", groupID))
		return
	}

	params, refusal := parseSnapshotParams(r.URL.Query())
	if refusal != "" {
		writeError(w, http.StatusBadRequest, refusal)
		return
	}
	if h.pool == nil {
		writeError(w, http.StatusInternalServerError,
			"contribution snapshots need a database pool and this server has none, so nothing was read. This is a server "+
				"wiring fault; construct the handler with a pool and restart.")
		return
	}

	control := &h.contributionSnapshot
	limit := contributionSnapshotDeadline(h.cfg.ContributionSnapshotTimeout)
	capacity := contributionSnapshotCapacity(h.cfg.ContributionSnapshotMaxConcurrent, h.pool.Config().MaxConns)

	release, ok := control.gate.acquire(ctx, uuid.UUID(user.PgID().Bytes), capacity, control.wait())
	if !ok {
		if ctx.Err() == nil {
			writeSnapshotBusy(w)
		}
		return
	}
	set, err := h.readContributionSnapshot(ctx, limit, control, user.PgID(), pgGroupID, params)
	// The database work is over: hand the connection (already returned by the
	// read) and the slots back before spending any time on the client.
	release()
	if err != nil {
		h.refuseContributionSnapshot(w, r, limit, err)
		return
	}

	// The whole set or nothing: size it before the status line, so an oversized
	// snapshot is refused with nothing partial sent.
	limitBytes := control.byteLimit()
	ok, err = set.fits(groupID.String(), params.projectHash, limitBytes)
	if err != nil {
		writeError(w, http.StatusInternalServerError,
			"Village could not encode this contribution snapshot, so nothing was returned. Nothing was changed. Retry; "+
				"if it keeps happening, report it to the operator.")
		return
	}
	if !ok {
		writeSnapshotTooLarge(w, set.Len(), limitBytes)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	// A write error means the client went away mid-body; the status line is
	// already sent, so there is nothing left to tell it.
	_ = set.writeJSON(w, groupID.String(), params.projectHash)
}

var errSnapshotBusy = errors.New("no database connection was free for a contribution snapshot")

func (h *Handler) refuseContributionSnapshot(w http.ResponseWriter, r *http.Request, limit time.Duration, err error) {
	if r.Context().Err() != nil {
		return // the client is gone; nobody is left to answer
	}
	if errors.Is(err, errSnapshotBusy) {
		writeSnapshotBusy(w)
		return
	}
	if _, timedOut := classifyContributionSnapshotError(err); timedOut {
		writeSnapshotTimeout(w, limit)
		return
	}
	writeError(w, http.StatusInternalServerError,
		"the project's sessions could not be read, so no snapshot was returned. Nothing was changed. Retry in a moment.")
}

// contributionSnapshotSQL builds the one statement that reads a snapshot.
// $1 is the owner, $2 the project, $3 the collective; the filter's arguments
// follow.
//
// The walk starts at the project's roots and follows parent_session_id down,
// never onto a session on a parent loop (that session is a root of its own
// family). The planner joins each level through the (owner_id,
// parent_session_id) index or by hashing, whichever it costs lower.
func contributionSnapshotSQL(filter contributionFamilyFilter, owner, group pgtype.UUID, projectHash string) (string, []any) {
	fam := buildContributionFamilySQL(filter, 4)
	matchRoot, matchChild := fam.RootColumns("r"), fam.MemberColumns("c")

	var b strings.Builder
	b.WriteString(`WITH RECURSIVE fam AS (
  SELECT r.id AS root_id, r.id, NULL::uuid AS parent_id, r.local_id, r.visibility, r.project_hash` + matchRoot + `
  FROM transcripts r
  WHERE r.owner_id = $1 AND r.project_hash = $2 AND ` + contributionRootCondition("r") + `
  UNION ALL
  SELECT f.root_id, c.id, f.id, c.local_id, c.visibility, c.project_hash` + matchChild + `
  FROM fam f
  JOIN transcripts c ON c.owner_id = $1 AND c.parent_session_id = f.local_id
  WHERE ` + contributionDescentSQL("f", "c") + `
)
SELECT f.id, f.parent_id, f.visibility, f.project_hash, a.status
FROM fam f
`)
	if fam.Having != "" {
		b.WriteString(`JOIN (SELECT root_id FROM fam GROUP BY root_id HAVING ` + fam.Having + `) kept ON kept.root_id = f.root_id
`)
	}
	b.WriteString(`` + contributionLatestAttemptSQL("f", "$3", "a"))
	return b.String(), append([]any{owner, projectHash, group}, fam.Args...)
}

// readContributionSnapshot reads the whole snapshot in one read-only REPEATABLE
// READ transaction and returns it, with its connection already released.
func (h *Handler) readContributionSnapshot(ctx context.Context, limit time.Duration, control *contributionSnapshotControl,
	owner, group pgtype.UUID, params snapshotParams) (*contributionSnapshotSet, error) {

	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()

	acquireCtx, cancelAcquire := context.WithTimeout(ctx, control.wait())
	conn, err := h.pool.Acquire(acquireCtx)
	cancelAcquire()
	if err != nil {
		if ctx.Err() == nil {
			return nil, errSnapshotBusy
		}
		return nil, err
	}
	defer conn.Release()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin the snapshot transaction: %w", err)
	}
	defer rollbackTxBestEffort("contribution_snapshot", tx)

	// The first statement of the transaction fixes its snapshot, so it is also
	// where the time bounds are set; both are local to this transaction.
	if _, err := tx.Exec(ctx, "SELECT set_config('statement_timeout', $1, true), set_config('lock_timeout', $2, true)",
		strconv.FormatInt(limit.Milliseconds(), 10), strconv.FormatInt(contributionSnapshotLockTimeout.Milliseconds(), 10)); err != nil {
		return nil, fmt.Errorf("fix the snapshot and its time bounds: %w", err)
	}
	if control.afterSnapshot != nil {
		control.afterSnapshot(ctx)
	}

	// The project names a search can match are the displayed names of the
	// owner's ROOT projects, resolved inside the snapshot like every other read.
	filter := params.filter
	if filter.hasQuery() {
		roots, err := h.readContributionRootProjects(ctx, sqlc.New(tx), owner, filter.Query)
		if err != nil {
			return nil, err
		}
		filter.QueryProjects = roots.Named
	}

	sql, args := contributionSnapshotSQL(filter, owner, group, params.projectHash)
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("read the snapshot: %w", err)
	}
	defer rows.Close()

	set := &contributionSnapshotSet{}
	for rows.Next() {
		v := rows.RawValues()
		id, okID := rawUUID(v[0])
		if !okID || v[2] == nil || v[3] == nil {
			return nil, errors.New("read the snapshot: a row had an unreadable id, visibility or project")
		}
		var parent [16]byte
		hasParent := v[1] != nil
		if hasParent {
			var okParent bool
			if parent, okParent = rawUUID(v[1]); !okParent {
				return nil, errors.New("read the snapshot: a row had an unreadable parent id")
			}
		}
		set.addRecord(id, parent, hasParent, v[3], v[2], v[4])
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the snapshot: %w", err)
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("end the snapshot transaction: %w", err)
	}
	return set, nil
}
