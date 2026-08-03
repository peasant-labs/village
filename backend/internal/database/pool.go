package database

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolMaxConnsEnv is the env var that overrides the connection-pool size. It
// exists so a bulk push (many concurrent transcript/annotation publishes from a
// client running --concurrency N) is not bottlenecked on the village's default
// pool size. See docs in backend/README.md.
const PoolMaxConnsEnv = "POOL_MAX_CONNS"

// connStrPoolMaxConnsKey is the pgx option key that sizes the pool when set
// directly inside the database connection string (DATABASE_URL), e.g.
// "postgres://…/db?pool_max_conns=16".
const connStrPoolMaxConnsKey = "pool_max_conns"

// DefaultPoolMaxConns is the built-in pool size used when neither POOL_MAX_CONNS
// nor a pool_max_conns option in the connection string is provided:
// max(10, 2*NumCPU).
//
// Sizing rationale: PostgreSQL advisory locks belong to a physical session, so a
// locked publish keeps one pooled connection across its source probe, preflight,
// S3 upload, and short persistence transactions; no transaction spans S3. A
// one-connection pool safely serializes publishes rather than deadlocking. The
// default follows the common Postgres rule of thumb (~2x cores) purely to leave
// ample headroom for the simultaneous short INSERTs of a bulk cold push —
// including the annotation bulk-upsert batches, which hold a connection a little
// longer than a single-row insert. It ships working out-of-the-box; no operator
// step is required.
func DefaultPoolMaxConns() int32 {
	if n := 2 * runtime.NumCPU(); n > 10 {
		return int32(n)
	}
	return 10
}

// PoolConfig parses the database connection string (DATABASE_URL) and resolves
// the pool's MaxConns with this precedence:
//
//  1. the POOL_MAX_CONNS env var, if set to a positive integer — wins;
//  2. an explicit pool_max_conns option in the connection string — respected
//     (the env default must not silently override an operator's deliberate value);
//  3. otherwise the built-in DefaultPoolMaxConns() = max(10, 2*NumCPU).
//
// Everything else (timeouts, health checks, the rest of the connection string)
// is left to pgxpool.ParseConfig's defaults.
func PoolConfig(connStr string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("parse database connection string: %w", err)
	}

	switch envN := envPoolMaxConns(); {
	case envN > 0:
		// (1) explicit env override wins.
		cfg.MaxConns = envN
	case strings.Contains(connStr, connStrPoolMaxConnsKey):
		// (2) the connection string set it; ParseConfig already applied that
		// value — leave it.
	default:
		// (3) built-in default.
		cfg.MaxConns = DefaultPoolMaxConns()
	}

	return cfg, nil
}

// envPoolMaxConns returns the POOL_MAX_CONNS value as a positive int32, or 0 when
// unset / blank / non-positive / unparseable (in which case it is ignored).
func envPoolMaxConns() int32 {
	v := strings.TrimSpace(os.Getenv(PoolMaxConnsEnv))
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0
	}
	return int32(n)
}
