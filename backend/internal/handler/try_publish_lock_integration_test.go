//go:build integration

package handler

import (
	"context"
	_ "embed"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed testdata/try-publish-lock.yaml
var tryPublishLockYAML []byte

type tryPublishLockCase struct {
	Name     string `yaml:"name"`
	MaxConns int32  `yaml:"max_conns"`
	State    string `yaml:"state"`
	Ran      bool   `yaml:"ran"`
}

var requiredTryPublishLockCases = []string{
	"free-lock-runs-the-rewrite-on-one-connection",
	"held-lock-is-skipped-without-waiting",
	"saturated-pool-is-skipped",
}

func TestWithTryPublishLockRealPostgres(t *testing.T) {
	cases, err := decodeFixtureRows[tryPublishLockCase](tryPublishLockYAML)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{}
	for _, name := range requiredTryPublishLockCases {
		required[name] = false
	}
	for _, c := range cases {
		if seen, known := required[c.Name]; !known || seen {
			t.Fatalf("unknown or duplicate try publish lock case %q", c.Name)
		}
		required[c.Name] = true
	}
	for name, seen := range required {
		if !seen {
			t.Fatalf("missing try publish lock case %q", name)
		}
	}

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			ctx := context.Background()
			pool := publishLockPool(t, c.MaxConns)
			t.Cleanup(pool.Close)
			owner, localID := pgtype.UUID{Bytes: uuid.New(), Valid: true}, uuid.NewString()
			// Warm every connection so the pool starts with idle ones.
			warm := make([]*pgxpool.Conn, 0, c.MaxConns)
			for i := int32(0); i < c.MaxConns; i++ {
				conn, err := pool.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				warm = append(warm, conn)
			}
			for _, conn := range warm {
				conn.Release()
			}

			switch c.State {
			case "free":
			case "lock-held":
				holder, err := pool.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Release()
				key := sessionPublishLockKey(owner, localID)
				if _, err := holder.Exec(ctx, "SELECT pg_advisory_lock(hashtextextended($1, 0))", key); err != nil {
					t.Fatal(err)
				}
				defer func() { _, _ = holder.Exec(ctx, "SELECT pg_advisory_unlock(hashtextextended($1, 0))", key) }()
			case "pool-saturated":
				holder, err := pool.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Release()
			default:
				t.Fatalf("fixture state %q is unknown", c.State)
			}

			h := &Handler{pool: pool}
			var ran bool
			var acquiredInside, idleInside int32
			started := time.Now()
			got, err := h.withTryPublishLock(ctx, owner, localID, func(*pgxpool.Conn) error {
				ran = true
				stat := pool.Stat()
				acquiredInside, idleInside = stat.AcquiredConns(), stat.IdleConns()
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if got != c.Ran || ran != c.Ran {
				t.Fatalf("withTryPublishLock reported %v and ran the rewrite %v, want %v", got, ran, c.Ran)
			}
			if elapsed := time.Since(started); elapsed > 2*time.Second {
				t.Fatalf("withTryPublishLock took %s; an optional rewrite must not wait for a lock", elapsed)
			}
			if c.Ran && (acquiredInside != 1 || idleInside != c.MaxConns-1) {
				t.Fatalf("the rewrite ran with %d connections acquired and %d idle, want exactly one taken", acquiredInside, idleInside)
			}
		})
	}
}
