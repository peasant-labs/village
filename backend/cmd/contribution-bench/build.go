package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// buildCandidates are the index sets whose build cost is measured, each built
// on a database that holds only the existing indexes. R3 is built as R1 then R2.
var buildCandidates = [][]string{
	{"bench_r1"},
	{"bench_r2"},
	{"bench_h1"},
	{"bench_r1", "bench_r2"},
}

// cmdBuild measures CREATE INDEX on the seeded dataset. Each build runs in its
// own time window with a single-row writer alongside it, so the writer's worst
// latency shows how long writes are blocked. The build statement is the plain
// form the migration runner would execute in its transaction, not CONCURRENTLY.
func cmdBuild(ctx context.Context, e *env, p Preset, base, work string) error {
	dir := filepath.Join(e.out, p.Name, "build")
	if err := copyDatabase(ctx, e, base, work); err != nil {
		return err
	}
	defer dropDatabase(ctx, e, work)
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
	rows, err := pool.Query(ctx, "SELECT id FROM transcripts WHERE owner_id = $1 ORDER BY id LIMIT 2000", owner)
	if err != nil {
		return err
	}
	var ids [][16]byte
	for rows.Next() {
		var id [16]byte
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) == 0 {
		return fmt.Errorf("no owner rows for the build writer")
	}

	for _, cand := range buildCandidates {
		name := strings.Join(cand, "+")
		if err := dropBenchIndexes(ctx, pool); err != nil {
			return err
		}
		stop, wait := startWriter(ctx, pool, ids)
		start := time.Now()
		var buildErr error
		for _, idx := range cand {
			if _, err := pool.Exec(ctx, indexDDL[idx]); err != nil {
				buildErr = fmt.Errorf("build %s: %w", idx, err)
				break
			}
		}
		buildMs := float64(time.Since(start).Microseconds()) / 1000
		close(stop)
		ops, maxMs := wait()
		if buildErr != nil {
			return buildErr
		}
		var size int64
		for _, idx := range cand {
			var n int64
			if err := pool.QueryRow(ctx, "SELECT pg_relation_size($1)", idx).Scan(&n); err != nil {
				return err
			}
			size += n
		}
		var tableBytes int64
		if err := pool.QueryRow(ctx, "SELECT pg_relation_size('transcripts')").Scan(&tableBytes); err != nil {
			return err
		}
		r := result{
			Dataset:     p.Name,
			Config:      name,
			Workload:    "build_" + name,
			Mode:        "build",
			Samples:     1,
			P50Ms:       buildMs,
			BuildMs:     buildMs,
			IndexBytes:  size,
			WriterOps:   ops,
			WriterMaxMs: maxMs,
			Note: fmt.Sprintf("plain CREATE INDEX inside one statement; transcripts heap %d bytes; margin to the 300000 ms migration timeout: %.1f%%",
				tableBytes, 100*(1-buildMs/300000)),
		}
		if err := appendResult(dir, r); err != nil {
			return err
		}
		fmt.Printf("build %s: %.1f ms, index %d bytes, writer ops %d max %.1f ms\n", name, buildMs, size, ops, maxMs)
		if err := dropBenchIndexes(ctx, pool); err != nil {
			return err
		}
	}
	return nil
}

func dropBenchIndexes(ctx context.Context, pool *pgxpool.Pool) error {
	for _, name := range benchIndexNames {
		if _, err := pool.Exec(ctx, "DROP INDEX IF EXISTS "+name); err != nil {
			return err
		}
	}
	return nil
}

// startWriter runs one single-row updater until stop is closed. It returns a
// wait function that yields the operation count and the worst latency.
func startWriter(ctx context.Context, pool *pgxpool.Pool, ids [][16]byte) (chan struct{}, func() (int, float64)) {
	stop := make(chan struct{})
	var mu sync.Mutex
	var ops int
	var maxMs float64
	done := make(chan struct{})
	go func() {
		defer close(done)
		for n := 0; ; n++ {
			select {
			case <-stop:
				return
			default:
			}
			start := time.Now()
			_, err := pool.Exec(ctx, "UPDATE transcripts SET updated_at = now() WHERE id = $1", ids[n%len(ids)])
			d := float64(time.Since(start).Microseconds()) / 1000
			mu.Lock()
			if err == nil {
				ops++
				if d > maxMs {
					maxMs = d
				}
			}
			mu.Unlock()
		}
	}()
	wait := func() (int, float64) {
		<-done
		mu.Lock()
		defer mu.Unlock()
		return ops, maxMs
	}
	return stop, wait
}
