// Command contribution-bench is a DEV / BENCHMARK-ONLY tool. It seeds a
// synthetic transcript library into a scratch PostgreSQL database and measures
// the contribution browse, picker, family search, snapshot and write paths
// under candidate index configurations.
//
// It is a separate main package, so it is structurally excluded from the
// server binary. It never runs as part of `go test ./...`, which only exercises
// the pure generator. It refuses to connect to a non-local host unless
// -allow-remote is given, and it refuses to seed a database that already holds
// transcripts.
//
// Configuration comes from flags or the environment; no machine path is built
// in:
//
//	BENCH_ADMIN_DATABASE_URL  admin DSN used to create scratch databases
//	BENCH_DB_PREFIX           scratch database name prefix (default village_bench)
//	BENCH_OUT_DIR             output directory for raw results (required)
//	BENCH_SEED                dataset seed (default 20260113)
//	BENCH_PG_CTL, BENCH_PGDATA  optional, enable cold-cache samples by restarting PostgreSQL
//
// See docs/contribution-scale-benchmark.md for the full procedure.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/village/backend/internal/database"
)

const (
	defaultSeed   = uint64(20260113)
	defaultPrefix = "village_bench"
	seedBatchRows = 50000
	systemActor   = database.SystemActorID
)

type env struct {
	adminDSN    string
	prefix      string
	out         string
	seed        uint64
	allowRemote bool
	pgCtl       string
	pgData      string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "contribution-bench:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	cmd, rest := args[0], args[1:]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	e := &env{}
	fs.StringVar(&e.adminDSN, "admin-dsn", os.Getenv("BENCH_ADMIN_DATABASE_URL"), "admin DSN used to create scratch databases")
	fs.StringVar(&e.prefix, "prefix", envOr("BENCH_DB_PREFIX", defaultPrefix), "scratch database name prefix")
	fs.StringVar(&e.out, "out", os.Getenv("BENCH_OUT_DIR"), "output directory for raw results")
	seedText := envOr("BENCH_SEED", strconv.FormatUint(defaultSeed, 10))
	fs.StringVar(&seedText, "seed", seedText, "dataset seed")
	fs.BoolVar(&e.allowRemote, "allow-remote", false, "allow a non-local database host")
	fs.StringVar(&e.pgCtl, "pg-ctl", os.Getenv("BENCH_PG_CTL"), "pg_ctl binary for cold-cache restarts (optional)")
	fs.StringVar(&e.pgData, "pgdata", os.Getenv("BENCH_PGDATA"), "PGDATA directory for cold-cache restarts (optional)")
	dataset := fs.String("dataset", "", "small | reported | large")
	config := fs.String("config", "", "index configuration: R0 R1 R2 R3 H1 R1+H1 R2+H1 R3+H1")
	db := fs.String("db", "", "scratch database name (overrides the default)")
	base := fs.String("base", "", "template database name (seeded, pristine)")
	matrix := fs.String("matrix", "reads,writes", "comma list: reads, writes, build")
	reps := fs.Int("reps", 30, "measured repetitions for warm reads")
	coldSamples := fs.Int("cold-samples", 3, "cold samples per read (needs -pg-ctl and -pgdata)")
	concurrentSeconds := fs.Int("concurrent-seconds", 20, "duration of the concurrent write run")
	replace := fs.Bool("replace", false, "drop the target database first if it exists")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	s, err := parseSeed(seedText)
	if err != nil {
		return err
	}
	e.seed = s
	if cmd == "help" || cmd == "-h" {
		return usage()
	}
	if e.out == "" && cmd != "checksum" {
		return errors.New("an output directory is required: set -out or BENCH_OUT_DIR")
	}
	if e.adminDSN == "" {
		return errors.New("an admin DSN is required: set -admin-dsn or BENCH_ADMIN_DATABASE_URL")
	}
	if err := requireLocal(e.adminDSN, e.allowRemote); err != nil {
		return err
	}
	ctx := context.Background()
	switch cmd {
	case "seed":
		p, err := preset(*dataset)
		if err != nil {
			return err
		}
		name := *db
		if name == "" {
			name = e.dbName(p.Name, "base")
		}
		return cmdSeed(ctx, e, p, name, *replace)
	case "checksum":
		return cmdChecksum(ctx, e, *db)
	case "measure":
		p, err := preset(*dataset)
		if err != nil {
			return err
		}
		cfg, err := lookupConfig(*config)
		if err != nil {
			return err
		}
		baseName := *base
		if baseName == "" {
			baseName = e.dbName(p.Name, "base")
		}
		work := *db
		if work == "" {
			work = e.dbName(p.Name, configSlug(cfg.Name))
		}
		return cmdMeasure(ctx, e, p, cfg, baseName, work, strings.Split(*matrix, ","), measureOpts{
			reps:              *reps,
			coldSamples:       *coldSamples,
			concurrentSeconds: *concurrentSeconds,
		})
	case "build":
		p, err := preset(*dataset)
		if err != nil {
			return err
		}
		baseName := *base
		if baseName == "" {
			baseName = e.dbName(p.Name, "base")
		}
		return cmdBuild(ctx, e, p, baseName, e.dbName(p.Name, "build"))
	case "summarize":
		return cmdSummarize(e)
	default:
		return usage()
	}
}

func usage() error {
	return errors.New(`usage: contribution-bench <seed|checksum|measure|build|summarize> [flags]
  seed       -dataset small|reported|large [-db NAME] [-replace]
  checksum   -db NAME
  measure    -dataset D -config C [-base NAME] [-db NAME] [-matrix reads,writes]
  build      -dataset D [-base NAME]
  summarize  (reads OUT_DIR results and writes summary.md)
flags: -admin-dsn (or BENCH_ADMIN_DATABASE_URL), -out (or BENCH_OUT_DIR), -seed, -prefix`)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseSeed(s string) (uint64, error) {
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("seed %q must be an unsigned integer: %w", s, err)
	}
	return v, nil
}

func preset(name string) (Preset, error) {
	p, ok := presets[name]
	if !ok {
		return Preset{}, fmt.Errorf("dataset must be one of small, reported, large; got %q", name)
	}
	return p, nil
}

func (e *env) dbName(dataset, suffix string) string {
	return e.prefix + "_" + dataset + "_" + suffix
}

// dsnFor returns the admin DSN with its database path replaced.
func dsnFor(adminDSN, dbName string) (string, error) {
	u, err := url.Parse(adminDSN)
	if err != nil {
		return "", fmt.Errorf("admin DSN is not a URL: %w", err)
	}
	u.Path = "/" + dbName
	return u.String(), nil
}

// requireLocal refuses a non-local host unless allowRemote is set. The check
// parses the URL, so a userinfo spoof such as localhost@remote resolves to the
// remote host and is refused.
func requireLocal(dsn string, allowRemote bool) error {
	if allowRemote {
		return nil
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return fmt.Errorf("DSN is not a URL: %w", err)
	}
	host := u.Hostname()
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("refusing non-local database host %q; pass -allow-remote only for a disposable benchmark database", host)
}

func connectPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheDescribe
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	cfg.MaxConns = 16
	return pgxpool.NewWithConfig(ctx, cfg)
}

func connectConn(ctx context.Context, dsn string) (*pgx.Conn, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeCacheDescribe
	cfg.RuntimeParams["timezone"] = "UTC"
	return pgx.ConnectConfig(ctx, cfg)
}

// createDatabase drops the target when replace is set, then creates it empty.
// It refuses to reuse an existing database, so a seed never mixes with old data.
func createDatabase(ctx context.Context, e *env, name string, replace bool) error {
	admin, err := connectConn(ctx, e.adminDSN)
	if err != nil {
		return fmt.Errorf("connect admin: %w", err)
	}
	defer admin.Close(ctx)
	var exists bool
	if err := admin.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists); err != nil {
		return err
	}
	if exists {
		if !replace {
			return fmt.Errorf("database %q already exists; pass -replace to drop and recreate it", name)
		}
		if _, err := admin.Exec(ctx, fmt.Sprintf("DROP DATABASE %s WITH (FORCE)", pgx.Identifier{name}.Sanitize())); err != nil {
			return fmt.Errorf("drop %q: %w", name, err)
		}
	}
	_, err = admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s TEMPLATE template0 ENCODING 'UTF8'", pgx.Identifier{name}.Sanitize()))
	return err
}

func cmdSeed(ctx context.Context, e *env, p Preset, name string, replace bool) error {
	started := time.Now()
	if err := createDatabase(ctx, e, name, replace); err != nil {
		return err
	}
	dsn, err := dsnFor(e.adminDSN, name)
	if err != nil {
		return err
	}
	pool, err := connectPool(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()

	// The schema comes from the server's own migration runner, not from a copy
	// of the DDL, so the benchmark runs against exactly the shipped schema.
	if err := database.RunMigrations(pool); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	var latest int
	if err := pool.QueryRow(ctx, "SELECT max(version) FROM schema_migrations").Scan(&latest); err != nil {
		return err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true), set_config('app.transcript_writer_version', '1', true)", systemActor); err != nil {
		return err
	}

	m, err := seedTransaction(ctx, tx, p, e.seed)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit seed: %w", err)
	}
	m.Dataset = p.Name

	sum, err := checksumDatabase(ctx, pool, m.OwnerID)
	if err != nil {
		return err
	}
	dir := filepath.Join(e.out, p.Name)
	if err := writeJSON(filepath.Join(dir, "manifest.json"), m); err != nil {
		return err
	}
	sum["database"] = name
	sum["schema_version"] = latest
	sum["seed_seconds"] = time.Since(started).Seconds()
	if err := writeJSON(filepath.Join(dir, "checksum-"+name+".json"), sum); err != nil {
		return err
	}
	fmt.Printf("seeded %s: %d transcripts, schema version %d, %.1fs\n", name, m.Transcripts, latest, time.Since(started).Seconds())
	return nil
}

// seedTransaction writes the owner, collective, membership, transcripts and
// share attempts. Transcript rows go through COPY, so the row triggers (writer
// marker, governance audit, derived share state) run for every row, exactly as
// they would for a real insert. Nothing is disabled.
func seedTransaction(ctx context.Context, tx pgx.Tx, p Preset, seed uint64) (Manifest, error) {
	owner := derivedUUID(seed, "owner", 0)
	creator := derivedUUID(seed, "creator", 0)
	member := derivedUUID(seed, "member", 0)
	group := derivedUUID(seed, "group", 0)
	for i, u := range []struct {
		id   [16]byte
		name string
	}{{owner, "bench-owner"}, {creator, "bench-creator"}, {member, "bench-member"}} {
		if _, err := tx.Exec(ctx,
			"INSERT INTO users (id, github_id, github_username, provider, provider_user_id) VALUES ($1, $2, $3, 'github', $4)",
			u.id, int64(9000001+i), u.name, strconv.Itoa(9000001+i)); err != nil {
			return Manifest{}, fmt.Errorf("insert synthetic user: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, "INSERT INTO groups (id, name, created_by) VALUES ($1, 'bench-collective', $2)", group, creator); err != nil {
		return Manifest{}, err
	}
	if _, err := tx.Exec(ctx, "INSERT INTO group_members (group_id, user_id, role, origin) VALUES ($1, $2, 'owner', 'creator')", group, creator); err != nil {
		return Manifest{}, err
	}
	if _, err := tx.Exec(ctx, "INSERT INTO group_members (group_id, user_id, role, origin) VALUES ($1, $2, 'member', 'self_join')", group, member); err != nil {
		return Manifest{}, err
	}

	transcriptCols := []string{"id", "owner_id", "local_id", "title", "visibility", "model_provider", "model_name",
		"session_start", "published_at", "blob_key", "blob_size_bytes", "schema_version", "parent_session_id",
		"git_branch", "git_remote", "project_hash", "project_name", "session_origin", "wrapped_data_key",
		"encryption_algorithm", "key_version", "turn_count"}
	attemptCols := []string{"transcript_id", "group_id", "event_num", "status", "recorded_at", "decided_at", "decided_by"}

	var batch []Row
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		rows := make([][]any, 0, len(batch))
		attempts := make([][]any, 0)
		for _, r := range batch {
			rows = append(rows, []any{
				r.ID, r.Owner, r.LocalID, r.Title, r.Visibility, r.Provider, r.Model,
				r.SessionStart, r.PublishedAt, r.BlobKey, r.BlobSize, "2", r.ParentSessionID,
				r.GitBranch, r.GitRemote, r.ProjectHash, r.ProjectName, r.Origin, r.WrappedKey,
				"aes-256-gcm-random-nonce-v1", int32(1), r.TurnCount,
			})
			if r.Attempt != "none" {
				var decidedAt *time.Time
				var decidedBy *[16]byte
				if r.Attempt != "pending" {
					d := r.PublishedAt.Add(24 * time.Hour)
					decidedAt = &d
					decidedBy = &creator
				}
				attempts = append(attempts, []any{r.ID, group, int32(1), r.Attempt, r.PublishedAt, decidedAt, decidedBy})
			}
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"transcripts"}, transcriptCols, pgx.CopyFromRows(rows)); err != nil {
			return fmt.Errorf("copy transcripts: %w", err)
		}
		if len(attempts) > 0 {
			if _, err := tx.CopyFrom(ctx, pgx.Identifier{"transcript_share_attempts"}, attemptCols, pgx.CopyFromRows(attempts)); err != nil {
				return fmt.Errorf("copy share attempts: %w", err)
			}
		}
		batch = batch[:0]
		return nil
	}
	m, err := Generate(p, seed, func(r Row) error {
		batch = append(batch, r)
		if len(batch) >= seedBatchRows {
			return flush()
		}
		return nil
	})
	if err != nil {
		return m, err
	}
	if err := flush(); err != nil {
		return m, err
	}
	return m, nil
}

// checksumDatabase returns order-independent digests of the target owner's
// transcripts and of all share attempts. Two seeds that produce the same
// digests produced the same rows.
func checksumDatabase(ctx context.Context, pool *pgxpool.Pool, ownerText string) (map[string]any, error) {
	out := map[string]any{}
	var n int64
	var digest string
	err := pool.QueryRow(ctx, `
		SELECT count(*), coalesce(md5(string_agg(concat_ws('|', id::text, local_id, coalesce(title, ''), visibility,
		       project_hash, coalesce(parent_session_id, ''), coalesce(session_start::text, ''), published_at::text,
		       coalesce(git_remote, ''), coalesce(git_branch, ''), blob_key, blob_size_bytes::text, session_origin,
		       turn_count::text, model_provider, coalesce(model_name, ''), project_name), E'\n' ORDER BY id)), '')
		FROM transcripts WHERE owner_id = $1::uuid`, ownerText).Scan(&n, &digest)
	if err != nil {
		return nil, fmt.Errorf("transcript checksum: %w", err)
	}
	out["transcripts"] = n
	out["transcripts_md5"] = digest

	var an int64
	var adigest string
	err = pool.QueryRow(ctx, `
		SELECT count(*), coalesce(md5(string_agg(concat_ws('|', a.transcript_id::text, a.group_id::text, a.event_num::text,
		       a.status, a.recorded_at::text, coalesce(a.decided_at::text, ''), coalesce(a.decided_by::text, '')),
		       E'\n' ORDER BY a.transcript_id, a.group_id, a.event_num)), '')
		FROM transcript_share_attempts a`).Scan(&an, &adigest)
	if err != nil {
		return nil, fmt.Errorf("attempt checksum: %w", err)
	}
	out["share_attempts"] = an
	out["share_attempts_md5"] = adigest
	return out, nil
}

func cmdChecksum(ctx context.Context, e *env, db string) error {
	if db == "" {
		return errors.New("checksum needs -db")
	}
	dsn, err := dsnFor(e.adminDSN, db)
	if err != nil {
		return err
	}
	pool, err := connectPool(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	var owner string
	if err := pool.QueryRow(ctx, "SELECT id::text FROM users WHERE github_username = 'bench-owner'").Scan(&owner); err != nil {
		return fmt.Errorf("find synthetic owner: %w", err)
	}
	sum, err := checksumDatabase(ctx, pool, owner)
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(sum, "", "  ")
	fmt.Println(string(b))
	return nil
}

// copyDatabase replaces the work database with a fresh copy of the template.
// The template must have no open connections; this function closes its own.
func copyDatabase(ctx context.Context, e *env, base, work string) error {
	admin, err := connectConn(ctx, e.adminDSN)
	if err != nil {
		return err
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", pgx.Identifier{work}.Sanitize())); err != nil {
		return fmt.Errorf("drop work database: %w", err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", pgx.Identifier{work}.Sanitize(), pgx.Identifier{base}.Sanitize())); err != nil {
		return fmt.Errorf("copy %s to %s: %w", base, work, err)
	}
	return nil
}

func lookupConfig(name string) (indexConfig, error) {
	for _, c := range indexConfigs {
		if c.Name == name {
			return c, nil
		}
	}
	return indexConfig{}, fmt.Errorf("config must be one of R0 R1 R2 R3 H1 R1+H1 R2+H1 R3+H1; got %q", name)
}

func configSlug(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, "+", "_"))
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// runCmd runs an external command, used only for pg_ctl restarts during
// cold-cache sampling.
func runCmd(name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	// Output goes to the caller's stdio, not a pipe. A restarted server keeps
	// inherited pipes open, so capturing output would block until the server
	// exits. Failures are reported through the exit status.
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%s %s: timed out after 90s: %w", name, strings.Join(args, " "), ctx.Err())
		}
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}
