package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// indexConfig names one candidate index set. The names are benchmark names
// (bench_*), so they never collide with shipped migrations.
type indexConfig struct {
	Name    string
	Indexes []string
}

var indexConfigs = []indexConfig{
	{"R0", nil},
	{"R1", []string{"bench_r1"}},
	{"R2", []string{"bench_r2"}},
	{"R3", []string{"bench_r1", "bench_r2"}},
	{"H1", []string{"bench_h1"}},
	{"R1+H1", []string{"bench_r1", "bench_h1"}},
	{"R2+H1", []string{"bench_r2", "bench_h1"}},
	{"R3+H1", []string{"bench_r1", "bench_r2", "bench_h1"}},
}

// The session-time expression must match the one the browse query orders by.
const sessionKey = "COALESCE(session_start, published_at, 'epoch'::timestamptz)"

var indexDDL = map[string]string{
	"bench_r1": "CREATE INDEX bench_r1 ON transcripts (owner_id, (" + sessionKey + ") DESC, id)",
	"bench_r2": "CREATE INDEX bench_r2 ON transcripts (owner_id, project_hash, (" + sessionKey + ") DESC, id)",
	"bench_h1": "CREATE INDEX bench_h1 ON transcripts (owner_id, parent_session_id)",
}

var benchIndexNames = []string{"bench_r1", "bench_r2", "bench_h1"}

// applyIndexes drops every benchmark index and creates the ones in cfg, then
// refreshes planner statistics. It returns the time spent creating indexes.
func applyIndexes(ctx context.Context, conn *pgx.Conn, cfg indexConfig) (time.Duration, error) {
	for _, name := range benchIndexNames {
		if _, err := conn.Exec(ctx, "DROP INDEX IF EXISTS "+name); err != nil {
			return 0, err
		}
	}
	start := time.Now()
	for _, name := range cfg.Indexes {
		if _, err := conn.Exec(ctx, indexDDL[name]); err != nil {
			return 0, fmt.Errorf("create %s: %w", name, err)
		}
	}
	elapsed := time.Since(start)
	if _, err := conn.Exec(ctx, "ANALYZE transcripts"); err != nil {
		return elapsed, err
	}
	return elapsed, nil
}

// rootCond is true when the alias is a browse root: it has no parent, or its
// parent is not among the owner's published transcripts.
func rootCond(alias string) string {
	return fmt.Sprintf("(%[1]s.parent_session_id IS NULL OR NOT EXISTS (SELECT 1 FROM transcripts p WHERE p.owner_id = %[1]s.owner_id AND p.local_id = %[1]s.parent_session_id))", alias)
}

// pageSQL returns the root keyset page with its helpers. Args: $1 owner,
// $2 cursor session key, $3 cursor id, and $4 project hash when withProject.
// Roots are ordered by session key descending, then id ascending, which is the
// order the R1 and R2 indexes serve. Helpers are every descendant of the page
// roots, followed through parent_session_id and capped at five levels.
func pageSQL(withProject bool, limit int) string {
	proj := ""
	if withProject {
		proj = "AND t.project_hash = $4"
	}
	lim := ""
	if limit > 0 {
		lim = fmt.Sprintf("LIMIT %d", limit)
	}
	return fmt.Sprintf(`
WITH RECURSIVE page AS (
  SELECT t.id, t.local_id, t.title, t.visibility, t.project_hash, t.parent_session_id
  FROM transcripts t
  WHERE t.owner_id = $1 AND %s %s
    AND (%s < $2 OR (%s = $2 AND t.id > $3))
  ORDER BY %s DESC, t.id ASC
  %s
), fam AS (
  SELECT h.id, h.local_id, h.title, h.visibility, h.project_hash, h.parent_session_id, 1 AS depth
  FROM transcripts h
  WHERE h.owner_id = $1 AND h.parent_session_id IN (SELECT local_id FROM page)
  UNION ALL
  SELECT h.id, h.local_id, h.title, h.visibility, h.project_hash, h.parent_session_id, f.depth + 1
  FROM fam f JOIN transcripts h ON h.owner_id = $1 AND h.parent_session_id = f.local_id
  WHERE f.depth < 5
)
SELECT id::text, local_id, title, visibility, project_hash, parent_session_id FROM page
UNION ALL
SELECT id::text, local_id, title, visibility, project_hash, parent_session_id FROM fam`,
		rootCond("t"), proj, sessionKey, sessionKey, sessionKey, lim)
}

// pickerSQL counts every transcript in the families of the owner's roots,
// keyed by the root's project. Arg: $1 owner.
const pickerSQL = `
WITH RECURSIVE roots AS (
  SELECT t.local_id, t.project_hash FROM transcripts t WHERE t.owner_id = $1 AND ` + "%ROOT%" + `
), fam AS (
  SELECT r.project_hash AS root_project, r.local_id, 0 AS depth FROM roots r
  UNION ALL
  SELECT f.root_project, h.local_id, f.depth + 1
  FROM fam f JOIN transcripts h ON h.owner_id = $1 AND h.parent_session_id = f.local_id
  WHERE f.depth < 5
)
SELECT root_project, count(*) FROM fam GROUP BY root_project`

// familySQL returns the roots in the listed projects whose family (root or any
// helper) has a title matching $3. Args: $1 owner, $2 project hashes, $3 pattern.
var familySQL = `
WITH RECURSIVE fam AS (
  SELECT r.project_hash AS root_project, r.local_id AS root_local, r.local_id, 0 AS depth
  FROM transcripts r
  WHERE r.owner_id = $1 AND r.project_hash = ANY($2) AND ` + "%ROOTR%" + `
  UNION ALL
  SELECT f.root_project, f.root_local, h.local_id, f.depth + 1
  FROM fam f JOIN transcripts h ON h.owner_id = $1 AND h.parent_session_id = f.local_id
  WHERE f.depth < 5
)
SELECT DISTINCT f.root_local
FROM fam f JOIN transcripts m ON m.owner_id = $1 AND m.local_id = f.local_id
WHERE m.title ILIKE $3`

// snapshotSQL returns every compact record of the families of one project's
// roots, or of every root when withProject is false. Args: $1 owner, $2 group,
// and $3 project hash when withProject.
func snapshotSQL(withProject bool) string {
	proj := ""
	if withProject {
		proj = "AND t.project_hash = $3"
	}
	return fmt.Sprintf(`
WITH RECURSIVE roots AS (
  SELECT t.local_id FROM transcripts t WHERE t.owner_id = $1 AND %s %s
), fam AS (
  SELECT r.local_id, 0 AS depth FROM roots r
  UNION ALL
  SELECT h.local_id, f.depth + 1
  FROM fam f JOIN transcripts h ON h.owner_id = $1 AND h.parent_session_id = f.local_id
  WHERE f.depth < 5
)
SELECT m.id::text, m.visibility, m.project_hash, m.parent_session_id, la.status
FROM fam f
JOIN transcripts m ON m.owner_id = $1 AND m.local_id = f.local_id
LEFT JOIN LATERAL (
  SELECT a.status FROM transcript_share_attempts a
  WHERE a.transcript_id = m.id AND a.group_id = $2
  ORDER BY a.event_num DESC LIMIT 1
) la ON true`, rootCond("t"), proj)
}

// snapshotRecord is the compact record the snapshot returns.
type snapshotRecord struct {
	ID              string  `json:"id"`
	Visibility      string  `json:"visibility"`
	ProjectHash     string  `json:"project_hash"`
	ParentSessionID *string `json:"parent_session_id"`
	Status          *string `json:"status"`
}

type readCase struct {
	name     string
	sql      string
	args     []any
	snapshot bool
}

// cursorAt returns the session key and id of the root at the given offset in
// browse order. The offset is a setup step and is not measured.
func cursorAt(ctx context.Context, conn *pgx.Conn, owner [16]byte, offset int) (time.Time, string, error) {
	var ts time.Time
	var id string
	q := fmt.Sprintf(`SELECT %s, t.id::text FROM transcripts t WHERE t.owner_id = $1 AND %s
		ORDER BY %s DESC, t.id ASC OFFSET $2 LIMIT 1`, sessionKey, rootCond("t"), sessionKey)
	if err := conn.QueryRow(ctx, q, owner, offset).Scan(&ts, &id); err != nil {
		return ts, id, err
	}
	return ts, id, nil
}

// sentinelTime is later than any session key, so the first page has no cursor.
var sentinelTime = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

var zeroUUID = [16]byte{}

// projectListForFamily returns the 20 hashes used by the family search: the
// 40-row project plus the first 19 tail projects, which are small and spread.
func projectListForFamily(m Manifest) []string {
	var out []string
	var small string
	for _, p := range m.Projects {
		if p.Name == "project-40" {
			small = p.Hash
		}
	}
	for _, p := range m.Projects {
		if strings.HasPrefix(p.Name, "tail-") && len(out) < 19 {
			out = append(out, p.Hash)
		}
	}
	return append(out, small)
}

func projectHash(m Manifest, name string) (string, error) {
	for _, p := range m.Projects {
		if p.Name == name {
			return p.Hash, nil
		}
	}
	return "", fmt.Errorf("manifest has no project %q", name)
}

// buildReadCases returns the read matrix for one dataset. The browse cursor
// for the deep page is resolved first, outside the measured time.
func buildReadCases(ctx context.Context, conn *pgx.Conn, m Manifest, owner [16]byte, dataset string) ([]readCase, error) {
	big, err := projectHash(m, writeProjectName(dataset))
	if err != nil {
		return nil, err
	}
	small, err := projectHash(m, "project-40")
	if err != nil {
		return nil, err
	}
	var deepOffset int
	if dataset == "large" {
		deepOffset = 99499 // last root of browse page 199, so the next page is page 200
	} else {
		var roots int
		if err := conn.QueryRow(ctx, fmt.Sprintf("SELECT count(*)::int FROM transcripts t WHERE t.owner_id = $1 AND %s", rootCond("t")), owner).Scan(&roots); err != nil {
			return nil, err
		}
		deepOffset = roots / 2
	}
	dts, did, err := cursorAt(ctx, conn, owner, deepOffset)
	if err != nil {
		return nil, fmt.Errorf("resolve deep cursor: %w", err)
	}
	cases := []readCase{
		{name: "first_page_500_roots_with_helpers", sql: pageSQL(false, 500), args: []any{owner, sentinelTime, zeroUUID}},
		{name: "deep_page_500_roots_with_helpers", sql: pageSQL(false, 500), args: []any{owner, dts, did}},
		{name: "project_small_40_rows_with_helpers", sql: pageSQL(true, 0), args: []any{owner, sentinelTime, zeroUUID, small}},
		{name: "project_large_3000_rows_with_helpers", sql: pageSQL(true, 0), args: []any{owner, sentinelTime, zeroUUID, big}},
		{name: "picker_aggregate", sql: strings.ReplaceAll(pickerSQL, "%ROOT%", rootCond("t")), args: []any{owner}},
		{name: "family_search_title_ilike", sql: strings.ReplaceAll(familySQL, "%ROOTR%", rootCond("r")), args: []any{owner, projectListForFamily(m), "%migration%"}},
		{name: "snapshot_project_large_uncapped", sql: snapshotSQL(true), args: []any{owner, groupID(m), big}, snapshot: true},
		{name: "snapshot_library_uncapped", sql: snapshotSQL(false), args: []any{owner, groupID(m)}, snapshot: true},
	}
	if dataset == "large" {
		huge, err := projectHash(m, "project-250000")
		if err != nil {
			return nil, err
		}
		cases = append(cases, readCase{name: "snapshot_project_huge_250000_uncapped", sql: snapshotSQL(true), args: []any{owner, groupID(m), huge}, snapshot: true})
	}
	return cases, nil
}

func groupID(m Manifest) [16]byte {
	var out [16]byte
	s := strings.ReplaceAll(m.GroupID, "-", "")
	for i := 0; i < 16; i++ {
		fmt.Sscanf(s[2*i:2*i+2], "%02x", &out[i])
	}
	return out
}

// execRead runs one read to completion and returns row count, raw payload
// bytes, and for snapshots the JSON payload bytes and allocation figures.
type readOutcome struct {
	rows     int64
	bytes    int64
	alloc    uint64
	peakHeap uint64
}

// execRead runs one read on conn. A snapshot runs inside one read-only
// REPEATABLE READ transaction, so every record comes from a single snapshot of
// the database.
func execRead(ctx context.Context, conn *pgx.Conn, rc readCase) (readOutcome, error) {
	if !rc.snapshot {
		return execQuery(ctx, conn, rc)
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return readOutcome{}, err
	}
	defer tx.Rollback(ctx)
	return execQuery(ctx, tx, rc)
}

type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func execQuery(ctx context.Context, q queryer, rc readCase) (readOutcome, error) {
	var out readOutcome
	rows, err := q.Query(ctx, rc.sql, rc.args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	if !rc.snapshot {
		for rows.Next() {
			out.rows++
			for _, v := range rows.RawValues() {
				out.bytes += int64(len(v))
			}
		}
		return out, rows.Err()
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	out.peakHeap = before.HeapInuse
	var recs []snapshotRecord
	for rows.Next() {
		var r snapshotRecord
		if err := rows.Scan(&r.ID, &r.Visibility, &r.ProjectHash, &r.ParentSessionID, &r.Status); err != nil {
			return out, err
		}
		out.rows++
		recs = append(recs, r)
		if out.rows%5000 == 0 {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			if m.HeapInuse > out.peakHeap {
				out.peakHeap = m.HeapInuse
			}
		}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	payload, err := json.Marshal(recs)
	if err != nil {
		return out, err
	}
	out.bytes = int64(len(payload))
	runtime.ReadMemStats(&after)
	out.alloc = after.TotalAlloc - before.TotalAlloc
	if after.HeapInuse > out.peakHeap {
		out.peakHeap = after.HeapInuse
	}
	return out, nil
}

// measureRead runs warm-up and measured repetitions on one connection and
// records the latency distribution. It also captures one EXPLAIN ANALYZE.
func measureRead(ctx context.Context, conn *pgx.Conn, dir string, base result, rc readCase, reps int) (result, error) {
	const warm = 3
	for i := 0; i < warm; i++ {
		if _, err := execRead(ctx, conn, rc); err != nil {
			return base, fmt.Errorf("%s warm-up: %w", rc.name, err)
		}
	}
	samples := make([]float64, 0, reps)
	var last readOutcome
	for i := 0; i < reps; i++ {
		start := time.Now()
		out, err := execRead(ctx, conn, rc)
		if err != nil {
			return base, fmt.Errorf("%s: %w", rc.name, err)
		}
		samples = append(samples, float64(time.Since(start).Microseconds())/1000)
		last = out
	}
	r := base
	r.Workload = rc.name
	r.Mode = "warm"
	r.Rows = last.rows
	r.Bytes = last.bytes
	r.AllocBytes = last.alloc
	r.PeakHeap = last.peakHeap
	summarizeSamples(&r, samples)

	planPath, err := capturePlan(ctx, conn, dir, rc)
	if err != nil {
		return r, fmt.Errorf("%s plan: %w", rc.name, err)
	}
	r.Plan = planPath
	return r, nil
}

// capturePlan writes EXPLAIN (ANALYZE, BUFFERS) output for one read.
func capturePlan(ctx context.Context, conn *pgx.Conn, dir string, rc readCase) (string, error) {
	rows, err := conn.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS, VERBOSE, FORMAT TEXT) "+rc.sql, rc.args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return "", err
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "plans", rc.name+".txt")
	if err := writeText(path, strings.Join(lines, "\n")+"\n"); err != nil {
		return "", err
	}
	return path, nil
}

// measureOpts carries the repetition and concurrency settings from flags.
type measureOpts struct {
	reps              int
	coldSamples       int
	concurrentSeconds int
}

// runReads runs the read matrix on an already-configured work database.
func runReads(ctx context.Context, e *env, p Preset, cfg indexConfig, work string, opts measureOpts, dir string) error {
	dsn, err := dsnFor(e.adminDSN, work)
	if err != nil {
		return err
	}
	conn, err := connectConn(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	m, err := loadManifest(e.out, p.Name)
	if err != nil {
		return err
	}
	owner := uuidBytes(m.OwnerID)
	cases, err := buildReadCases(ctx, conn, m, owner, p.Name)
	if err != nil {
		return err
	}
	base := result{Dataset: p.Name, Config: cfg.Name}
	for _, rc := range cases {
		r, err := measureRead(ctx, conn, dir, base, rc, opts.reps)
		if err != nil {
			return err
		}
		if err := appendResult(dir, r); err != nil {
			return err
		}
		fmt.Printf("%s %s warm p50=%.2fms p95=%.2fms rows=%d bytes=%d\n", cfg.Name, rc.name, r.P50Ms, r.P95Ms, r.Rows, r.Bytes)
		if opts.coldSamples > 0 && e.pgCtl != "" && e.pgData != "" {
			cold, err := runColdSamples(ctx, e, work, base, rc, opts.coldSamples)
			if err != nil {
				return err
			}
			if err := appendResult(dir, cold); err != nil {
				return err
			}
			fmt.Printf("%s %s cold p50=%.2fms p95=%.2fms (n=%d)\n", cfg.Name, rc.name, cold.P50Ms, cold.P95Ms, cold.Samples)
		}
	}
	return nil
}

// runColdSamples restarts PostgreSQL before each sample, so the first execution
// starts with empty shared buffers. The operating system page cache is not
// dropped, so these samples are partially warm, and the result says so.
func runColdSamples(ctx context.Context, e *env, work string, base result, rc readCase, n int) (result, error) {
	dsn, err := dsnFor(e.adminDSN, work)
	if err != nil {
		return base, err
	}
	samples := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		if err := runCmd(e.pgCtl, "-D", e.pgData, "-m", "fast", "-w", "restart"); err != nil {
			return base, err
		}
		conn, err := connectConn(ctx, dsn)
		if err != nil {
			return base, err
		}
		start := time.Now()
		out, err := execRead(ctx, conn, rc)
		samples = append(samples, float64(time.Since(start).Microseconds())/1000)
		conn.Close(ctx)
		if err != nil {
			return base, fmt.Errorf("%s cold: %w", rc.name, err)
		}
		base.Rows = out.rows
		base.Bytes = out.bytes
	}
	r := base
	r.Workload = rc.name
	r.Mode = "cold"
	r.Note = "first execution after a PostgreSQL restart; operating system page cache not dropped"
	summarizeSamples(&r, samples)
	return r, nil
}
