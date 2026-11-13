package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// chunkSizes are the client chunk sizes under comparison.
var chunkSizes = []int{100, 250, 500, 1000}

// contributionRows is the size of the primary first-time contribution
// selection. The selection is the first rows of one project, in id order, so
// every configuration sees the same rows.
const contributionRows = 800

// writeReps is the number of repetitions for chunk and visibility writes.
const writeReps = 10

func loadManifest(out, dataset string) (Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(filepath.Join(out, dataset, "manifest.json"))
	if err != nil {
		return m, fmt.Errorf("read manifest (run seed first): %w", err)
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	return m, nil
}

func uuidBytes(s string) [16]byte {
	var out [16]byte
	hexs := strings.ReplaceAll(s, "-", "")
	for i := 0; i < 16; i++ {
		fmt.Sscanf(hexs[2*i:2*i+2], "%02x", &out[i])
	}
	return out
}

func pgid(b [16]byte) pgtype.UUID {
	return pgtype.UUID{Bytes: b, Valid: true}
}

func writeText(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

// writeProjectName is the project the write workloads draw from: the 3,000-row
// project, or the 250-row project in the smoke dataset.
func writeProjectName(dataset string) string {
	if dataset == "small" {
		return "project-250"
	}
	return "project-3000"
}

// selectWriteSet returns the private, never-submitted transcripts of one project
// in id order. The sets are disjoint slices of that order: contribution rows,
// concurrent contribution rows, and visibility rows.
func selectWriteSet(ctx context.Context, pool *pgxpool.Pool, owner [16]byte, projectHash string, n int) ([][16]byte, error) {
	rows, err := pool.Query(ctx, `
		SELECT t.id FROM transcripts t
		WHERE t.owner_id = $1 AND t.project_hash = $2 AND t.visibility = 'private'
		  AND NOT EXISTS (SELECT 1 FROM transcript_share_attempts a WHERE a.transcript_id = t.id)
		ORDER BY t.id LIMIT $3`, owner, projectHash, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids [][16]byte
	for rows.Next() {
		var id [16]byte
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) < n {
		return nil, fmt.Errorf("need %d private unsubmitted transcripts in the write project, found %d", n, len(ids))
	}
	return ids, nil
}

// setGUCs sets the transaction-local markers the triggers require: the system
// actor for the governance audit, and the writer marker for encrypted storage
// mutations. The derivation marker stays off except where a reset deletes
// derived share rows.
func setGUCs(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true), set_config('app.transcript_writer_version', '1', true), set_config('app.share_state_derivation', 'off', true)", systemActor)
	return err
}

func currentLSN(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	var lsn string
	err := pool.QueryRow(ctx, "SELECT pg_current_wal_lsn()::text").Scan(&lsn)
	return lsn, err
}

func walSince(ctx context.Context, pool *pgxpool.Pool, lsn string) (int64, error) {
	var n int64
	err := pool.QueryRow(ctx, "SELECT pg_wal_lsn_diff(pg_current_wal_lsn(), $1::pg_lsn)::bigint", lsn).Scan(&n)
	return n, err
}

// walFlushCounters returns WAL write and sync counts from pg_stat_wal. The
// forced flush and the read run on one backend, so the counters are current.
func walFlushCounters(ctx context.Context, pool *pgxpool.Pool) (int64, int64, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_stat_force_next_flush()"); err != nil {
		return 0, 0, err
	}
	var w, s int64
	err = conn.QueryRow(ctx, "SELECT wal_write, wal_sync FROM pg_stat_wal").Scan(&w, &s)
	return w, s, err
}

// contributeChunk is one client chunk of a first-time private-to-shared
// contribution. Each statement is the generated query the handler runs, in the
// same order as writeBatchShare. The approximations are listed in the results
// document: the group policy read and the per-transcript publish lock are
// simplified, and the handler's outer collective authority transaction is not
// reproduced.
func contributeChunk(ctx context.Context, pool *pgxpool.Pool, owner, group [16]byte, projectHash string, ids [][16]byte) (time.Duration, int64, error) {
	lsn, err := currentLSN(ctx, pool)
	if err != nil {
		return 0, 0, err
	}
	start := time.Now()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(ctx)
	if err := setGUCs(ctx, tx); err != nil {
		return 0, 0, err
	}
	var policy string
	if err := tx.QueryRow(ctx, "SELECT acceptance_mode FROM groups WHERE id = $1", pgid(group)).Scan(&policy); err != nil {
		return 0, 0, err
	}
	q := sqlc.New(tx)
	for _, id := range ids {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", "transcript-publish:"+uuidString(id)); err != nil {
			return 0, 0, err
		}
		cur, err := q.GetTranscriptByID(ctx, pgid(id))
		if err != nil {
			return 0, 0, err
		}
		if cur.OwnerID.Bytes != owner || cur.ProjectHash != projectHash {
			return 0, 0, fmt.Errorf("transcript %s is outside the selected project", uuidString(id))
		}
		latest, err := q.GetLatestShareAttempt(ctx, sqlc.GetLatestShareAttemptParams{TranscriptID: pgid(id), GroupID: pgid(group)})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return 0, 0, err
		}
		if err == nil && (latest.Status == "pending" || latest.Status == "approved") {
			continue
		}
		if err := q.ShareTranscriptWithStatus(ctx, sqlc.ShareTranscriptWithStatusParams{TranscriptID: pgid(id), GroupID: pgid(group), Status: "pending"}); err != nil {
			return 0, 0, fmt.Errorf("open attempt for %s: %w", uuidString(id), err)
		}
		pre, err := q.GetTranscriptGovernanceForUpdate(ctx, pgid(id))
		if err != nil {
			return 0, 0, err
		}
		if pre.Visibility != "private" {
			continue
		}
		if _, err := q.UpdateTranscriptMetadata(ctx, sqlc.UpdateTranscriptMetadataParams{
			ID:          pgid(id),
			Title:       pre.Title,
			Description: pre.Description,
			Visibility:  "shared",
			LicenseID:   pre.LicenseID,
		}); err != nil {
			return 0, 0, fmt.Errorf("share %s: %w", uuidString(id), err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	elapsed := time.Since(start)
	wal, err := walSince(ctx, pool, lsn)
	return elapsed, wal, err
}

// resetContributions returns the selected rows to their pre-contribution state
// by deleting the attempts and derived share rows the contribution created and
// flipping visibility back to private. It runs between repetitions and is never
// inside a measured interval. It is a benchmark reset, not a product path.
func resetContributions(ctx context.Context, pool *pgxpool.Pool, group [16]byte, ids [][16]byte) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true), set_config('app.transcript_writer_version', '1', true), set_config('app.share_state_derivation', 'on', true)", systemActor); err != nil {
		return err
	}
	list := idList(ids)
	if _, err := tx.Exec(ctx, "DELETE FROM transcript_shares WHERE group_id = $1 AND transcript_id = ANY($2::text[]::uuid[])", pgid(group), list); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM transcript_share_attempts WHERE group_id = $1 AND transcript_id = ANY($2::text[]::uuid[])", pgid(group), list); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "UPDATE transcripts SET visibility = 'private' WHERE id = ANY($1::text[]::uuid[]) AND visibility <> 'private'", list); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func idList(ids [][16]byte) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = uuidString(id)
	}
	return out
}

// republishOnce replaces the blob of one transcript, as a republish does: the
// blob key and size change, and the writer marker trigger runs.
func republishOnce(ctx context.Context, pool *pgxpool.Pool, id [16]byte, n int) (time.Duration, int64, error) {
	lsn, err := currentLSN(ctx, pool)
	if err != nil {
		return 0, 0, err
	}
	start := time.Now()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(ctx)
	if err := setGUCs(ctx, tx); err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec(ctx, "UPDATE transcripts SET blob_key = $2, blob_size_bytes = $3, content_hash = $4, updated_at = now() WHERE id = $1",
		pgid(id), fmt.Sprintf("transcripts/%s-r%d.bin", uuidString(id), n), int64(2000+n), fmt.Sprintf("%064x", n)); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	elapsed := time.Since(start)
	wal, err := walSince(ctx, pool, lsn)
	return elapsed, wal, err
}

// toggleVisibility flips one transcript between private and shared through the
// same governance read and metadata update the PATCH path uses.
func toggleVisibility(ctx context.Context, pool *pgxpool.Pool, id [16]byte) (time.Duration, int64, error) {
	lsn, err := currentLSN(ctx, pool)
	if err != nil {
		return 0, 0, err
	}
	start := time.Now()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(ctx)
	if err := setGUCs(ctx, tx); err != nil {
		return 0, 0, err
	}
	q := sqlc.New(tx)
	pre, err := q.GetTranscriptGovernanceForUpdate(ctx, pgid(id))
	if err != nil {
		return 0, 0, err
	}
	next := "shared"
	if pre.Visibility != "private" {
		next = "private"
	}
	if _, err := q.UpdateTranscriptMetadata(ctx, sqlc.UpdateTranscriptMetadataParams{
		ID: pgid(id), Title: pre.Title, Description: pre.Description, Visibility: next, LicenseID: pre.LicenseID,
	}); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	elapsed := time.Since(start)
	wal, err := walSince(ctx, pool, lsn)
	return elapsed, wal, err
}

// runWrites runs the write matrix on the work database: chunked contribution at
// four chunk sizes on identical selections, republish, visibility updates, and
// a concurrent publish, republish, and contribution run.
func runWrites(ctx context.Context, e *env, p Preset, cfg indexConfig, work string, opts measureOpts, dir string) error {
	dsn, err := dsnFor(e.adminDSN, work)
	if err != nil {
		return err
	}
	pool, err := connectPool(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	m, err := loadManifest(e.out, p.Name)
	if err != nil {
		return err
	}
	owner := uuidBytes(m.OwnerID)
	group := uuidBytes(m.GroupID)
	projName := writeProjectName(p.Name)
	projHash, err := projectHash(m, projName)
	if err != nil {
		return err
	}

	// Selections: contribution rows, concurrent contribution rows, visibility rows.
	// The smoke dataset has too few candidates for the full sizes.
	rowsPerSet, visRows := contributionRows, 100
	if p.Name == "small" {
		rowsPerSet, visRows = 30, 40
	}
	sel, err := selectWriteSet(ctx, pool, owner, projHash, 2*rowsPerSet+visRows)
	if err != nil {
		return err
	}
	chunkIDs := sel[:rowsPerSet]
	concIDs := sel[rowsPerSet : 2*rowsPerSet]
	visIDs := sel[2*rowsPerSet:]
	if err := writeJSON(filepath.Join(dir, "selection.json"), map[string]any{
		"project": projName, "project_hash": projHash,
		"contribution_rows": len(chunkIDs), "concurrent_contribution_rows": len(concIDs), "visibility_rows": len(visIDs),
		"first_contribution_id": uuidString(chunkIDs[0]), "last_contribution_id": uuidString(chunkIDs[len(chunkIDs)-1]),
	}); err != nil {
		return err
	}
	base := result{Dataset: p.Name, Config: cfg.Name}

	for _, size := range chunkSizes {
		var lat, wal []float64
		var runMs, walSyncs []float64
		for rep := 0; rep < writeReps; rep++ {
			_, s0, err := walFlushCounters(ctx, pool)
			if err != nil {
				return err
			}
			runStart := time.Now()
			for i := 0; i < len(chunkIDs); i += size {
				j := i + size
				if j > len(chunkIDs) {
					j = len(chunkIDs)
				}
				d, w, err := contributeChunk(ctx, pool, owner, group, projHash, chunkIDs[i:j])
				if err != nil {
					return fmt.Errorf("chunk size %d: %w", size, err)
				}
				lat = append(lat, float64(d.Microseconds())/1000)
				wal = append(wal, float64(w))
			}
			runMs = append(runMs, float64(time.Since(runStart).Microseconds())/1000)
			_, s1, err := walFlushCounters(ctx, pool)
			if err != nil {
				return err
			}
			walSyncs = append(walSyncs, float64(s1-s0))
			if err := resetContributions(ctx, pool, group, chunkIDs); err != nil {
				return err
			}
		}
		r := base
		r.Workload = fmt.Sprintf("contribute_chunk_size_%d_of_%d_rows", size, len(chunkIDs))
		r.Mode = "write"
		r.Rows = int64(len(chunkIDs))
		summarizeSamples(&r, lat)
		r.WalBytesAvg = mean(wal)
		r.WalSyncs = int64(mean(walSyncs))
		r.Note = fmt.Sprintf("chunk latency samples; run total p50 %.1f ms over %d reps; %d-row run per rep", median(runMs), writeReps, len(chunkIDs))
		if err := appendResult(dir, r); err != nil {
			return err
		}
		fmt.Printf("%s chunk %d: p50=%.1fms p95=%.1fms n=%d\n", cfg.Name, size, r.P50Ms, r.P95Ms, r.Samples)
	}

	// Republish one transcript at a time, with the blob replaced each time.
	var rlat, rwal []float64
	for i := 0; i < writeReps*20; i++ {
		d, w, err := republishOnce(ctx, pool, visIDs[i%len(visIDs)], i)
		if err != nil {
			return fmt.Errorf("republish: %w", err)
		}
		rlat = append(rlat, float64(d.Microseconds())/1000)
		rwal = append(rwal, float64(w))
	}
	r := base
	r.Workload = "republish_one_transcript"
	r.Mode = "write"
	summarizeSamples(&r, rlat)
	r.WalBytesAvg = mean(rwal)
	if err := appendResult(dir, r); err != nil {
		return err
	}
	fmt.Printf("%s republish: p50=%.2fms p95=%.2fms wal=%.0f\n", cfg.Name, r.P50Ms, r.P95Ms, r.WalBytesAvg)

	// Visibility updates, private and shared alternating.
	var vlat, vwal []float64
	for i := 0; i < writeReps*20; i++ {
		d, w, err := toggleVisibility(ctx, pool, visIDs[i%len(visIDs)])
		if err != nil {
			return fmt.Errorf("visibility update: %w", err)
		}
		vlat = append(vlat, float64(d.Microseconds())/1000)
		vwal = append(vwal, float64(w))
	}
	r = base
	r.Workload = "visibility_update_one_transcript"
	r.Mode = "write"
	summarizeSamples(&r, vlat)
	r.WalBytesAvg = mean(vwal)
	if err := appendResult(dir, r); err != nil {
		return err
	}
	fmt.Printf("%s visibility: p50=%.2fms p95=%.2fms wal=%.0f\n", cfg.Name, r.P50Ms, r.P95Ms, r.WalBytesAvg)

	// Concurrent publish, republish, contribution and visibility update.
	conc, err := runConcurrent(ctx, pool, m, owner, group, projHash, concIDs, visIDs, opts.concurrentSeconds)
	if err != nil {
		return err
	}
	for op, samples := range conc.lat {
		r := base
		r.Workload = "concurrent_" + op
		r.Mode = "write"
		summarizeSamples(&r, samples)
		r.Note = fmt.Sprintf("run of %ds with publish, republish, contribution and visibility workers; errors=%d; lock-waiting sessions max=%d mean=%.2f over %d samples",
			opts.concurrentSeconds, conc.errs[op], conc.lockMax, conc.lockMean, conc.lockSamples)
		if err := appendResult(dir, r); err != nil {
			return err
		}
		fmt.Printf("%s concurrent %s: p50=%.2fms p95=%.2fms n=%d errs=%d\n", cfg.Name, op, r.P50Ms, r.P95Ms, r.Samples, conc.errs[op])
	}
	return nil
}

type concurrentResult struct {
	lat         map[string][]float64
	errs        map[string]int
	lockMax     int
	lockMean    float64
	lockSamples int
}

// runConcurrent runs publish, republish, contribution and visibility workers
// against one database for a fixed duration. A sampler records how many
// sessions are waiting on locks. Errors caused by the end of the run are not
// counted as failures.
func runConcurrent(ctx context.Context, pool *pgxpool.Pool, m Manifest, owner, group [16]byte, projHash string, concIDs, visIDs [][16]byte, seconds int) (concurrentResult, error) {
	res := concurrentResult{lat: map[string][]float64{}, errs: map[string]int{}}
	var mu sync.Mutex
	record := func(op string, d time.Duration, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return
			}
			res.errs[op]++
			return
		}
		res.lat[op] = append(res.lat[op], float64(d.Microseconds())/1000)
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	var wg sync.WaitGroup

	// Sampler for lock waits.
	var lockMu sync.Mutex
	var lockSum, lockMax, lockN int
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(50 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-t.C:
				var n int
				if err := pool.QueryRow(context.Background(), "SELECT count(*)::int FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'").Scan(&n); err != nil {
					continue
				}
				lockMu.Lock()
				lockSum += n
				lockN++
				if n > lockMax {
					lockMax = n
				}
				lockMu.Unlock()
			}
		}
	}()

	// Contribution workers take disjoint chunks of 100 rows each.
	const workers = 2
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := w * 100; i < len(concIDs); i += workers * 100 {
				if runCtx.Err() != nil {
					return
				}
				j := i + 100
				if j > len(concIDs) {
					j = len(concIDs)
				}
				d, _, err := contributeChunk(runCtx, pool, owner, group, projHash, concIDs[i:j])
				record("contribution_chunk_100", d, err)
			}
		}(w)
	}

	// Republish workers cycle over the owner's first rows.
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for n := 0; runCtx.Err() == nil; n++ {
				id := visIDs[(n+w)%len(visIDs)]
				d, _, err := republishOnce(runCtx, pool, id, 100000+w*1000000+n)
				record("republish", d, err)
			}
		}(w)
	}

	// Visibility worker toggles the visibility rows.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; runCtx.Err() == nil; n++ {
			d, _, err := toggleVisibility(runCtx, pool, visIDs[n%len(visIDs)])
			record("visibility_update", d, err)
		}
	}()

	// Publish worker inserts new transcripts for the same owner and project.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; runCtx.Err() == nil; n++ {
			start := time.Now()
			tx, err := pool.Begin(runCtx)
			if err == nil {
				err = setGUCs(runCtx, tx)
			}
			if err == nil {
				_, err = tx.Exec(runCtx, `
					INSERT INTO transcripts (id, owner_id, local_id, visibility, model_provider, blob_key, schema_version,
					    project_hash, session_origin, wrapped_data_key, encryption_algorithm, key_version)
					VALUES ($1, $2, $3, 'private', 'anthropic', $4, '2', $5, 'user', $6, 'aes-256-gcm-random-nonce-v1', 1)`,
					derivedUUID(m.Seed, "concurrent-publish", n), owner, fmt.Sprintf("bench-concurrent-%d", n),
					fmt.Sprintf("transcripts/bench-concurrent-%d.bin", n), projHash, make([]byte, 32))
			}
			if err == nil {
				err = tx.Commit(runCtx)
			}
			if tx != nil {
				tx.Rollback(context.Background())
			}
			record("publish_insert", time.Since(start), err)
		}
	}()

	wg.Wait()
	lockMu.Lock()
	res.lockMax = lockMax
	if lockN > 0 {
		res.lockMean = float64(lockSum) / float64(lockN)
	}
	res.lockSamples = lockN
	lockMu.Unlock()
	return res, nil
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	c := append([]float64(nil), xs...)
	sort.Float64s(c)
	return c[len(c)/2]
}

// cmdMeasure runs one configuration: a fresh copy of the seeded base database,
// the configuration's indexes, the read matrix, then the write matrix.
func cmdMeasure(ctx context.Context, e *env, p Preset, cfg indexConfig, base, work string, matrix []string, opts measureOpts) error {
	dir := filepath.Join(e.out, p.Name, configSlug(cfg.Name))
	if err := copyDatabase(ctx, e, base, work); err != nil {
		return err
	}
	defer dropDatabase(ctx, e, work)

	dsn, err := dsnFor(e.adminDSN, work)
	if err != nil {
		return err
	}
	conn, err := connectConn(ctx, dsn)
	if err != nil {
		return err
	}
	took, err := applyIndexes(ctx, conn, cfg)
	conn.Close(ctx)
	if err != nil {
		return fmt.Errorf("apply %s: %w", cfg.Name, err)
	}
	fmt.Printf("applied %s in %.1f ms\n", cfg.Name, float64(took.Microseconds())/1000)
	if err := appendResult(dir, result{Dataset: p.Name, Config: cfg.Name, Workload: "apply_indexes_and_analyze", Mode: "setup", Samples: 1, P50Ms: float64(took.Microseconds()) / 1000, Note: "index creation for this configuration, plus ANALYZE"}); err != nil {
		return err
	}

	for _, step := range matrix {
		switch strings.TrimSpace(step) {
		case "reads":
			if err := runReads(ctx, e, p, cfg, work, opts, dir); err != nil {
				return err
			}
		case "writes":
			if err := runWrites(ctx, e, p, cfg, work, opts, dir); err != nil {
				return err
			}
		case "":
		default:
			return fmt.Errorf("unknown matrix step %q", step)
		}
	}
	return nil
}

func dropDatabase(ctx context.Context, e *env, name string) {
	admin, err := connectConn(ctx, e.adminDSN)
	if err != nil {
		return
	}
	defer admin.Close(ctx)
	admin.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", pgx.Identifier{name}.Sanitize()))
}
