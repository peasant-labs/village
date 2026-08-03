package database

import (
	"runtime"
	"testing"
)

const testConnStr = "postgres://u:p@localhost:5432/db?sslmode=disable"

func TestDefaultPoolMaxConns(t *testing.T) {
	want := int32(2 * runtime.NumCPU())
	if want < 10 {
		want = 10
	}
	if got := DefaultPoolMaxConns(); got != want {
		t.Errorf("DefaultPoolMaxConns() = %d, want %d (max(10, 2*NumCPU))", got, want)
	}
	if DefaultPoolMaxConns() < 10 {
		t.Errorf("DefaultPoolMaxConns() must never be below the floor of 10")
	}
}

func TestPoolConfig_DefaultWhenUnset(t *testing.T) {
	t.Setenv(PoolMaxConnsEnv, "")
	cfg, err := PoolConfig(testConnStr)
	if err != nil {
		t.Fatalf("PoolConfig: %v", err)
	}
	if cfg.MaxConns != DefaultPoolMaxConns() {
		t.Errorf("MaxConns = %d, want default %d", cfg.MaxConns, DefaultPoolMaxConns())
	}
}

func TestPoolConfig_EnvOverride(t *testing.T) {
	t.Setenv(PoolMaxConnsEnv, "37")
	cfg, err := PoolConfig(testConnStr)
	if err != nil {
		t.Fatalf("PoolConfig: %v", err)
	}
	if cfg.MaxConns != 37 {
		t.Errorf("MaxConns = %d, want 37 (POOL_MAX_CONNS override)", cfg.MaxConns)
	}
}

func TestPoolConfig_ConnStrValueRespectedWhenEnvUnset(t *testing.T) {
	t.Setenv(PoolMaxConnsEnv, "")
	cfg, err := PoolConfig(testConnStr + "&pool_max_conns=7")
	if err != nil {
		t.Fatalf("PoolConfig: %v", err)
	}
	// An explicit pool_max_conns in the connection string must be respected,
	// NOT silently replaced by the default.
	if cfg.MaxConns != 7 {
		t.Errorf("MaxConns = %d, want 7 (explicit connection-string pool_max_conns respected)", cfg.MaxConns)
	}
}

func TestPoolConfig_EnvWinsOverConnStr(t *testing.T) {
	t.Setenv(PoolMaxConnsEnv, "21")
	cfg, err := PoolConfig(testConnStr + "&pool_max_conns=7")
	if err != nil {
		t.Fatalf("PoolConfig: %v", err)
	}
	if cfg.MaxConns != 21 {
		t.Errorf("MaxConns = %d, want 21 (env wins over connection-string value)", cfg.MaxConns)
	}
}

func TestPoolConfig_InvalidEnvFallsBack(t *testing.T) {
	for _, bad := range []string{"0", "-3", "abc", "  "} {
		t.Run(bad, func(t *testing.T) {
			t.Setenv(PoolMaxConnsEnv, bad)
			cfg, err := PoolConfig(testConnStr)
			if err != nil {
				t.Fatalf("PoolConfig: %v", err)
			}
			if cfg.MaxConns != DefaultPoolMaxConns() {
				t.Errorf("invalid POOL_MAX_CONNS=%q: MaxConns = %d, want default %d",
					bad, cfg.MaxConns, DefaultPoolMaxConns())
			}
		})
	}
}

func TestPoolConfig_BadConnStr(t *testing.T) {
	if _, err := PoolConfig("://not a connection string"); err == nil {
		t.Error("PoolConfig on a malformed connection string must return an error")
	}
}
