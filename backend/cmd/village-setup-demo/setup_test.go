package main

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestDemoCredentials_JSONShape asserts the emitted credentials.json carries
// exactly the keys the peasant client reads (api_key/key_id/user_id/username/
// village_url) — a schema-shape guard that needs no database.
func TestDemoCredentials_JSONShape(t *testing.T) {
	c := demoCredentials{
		APIKey:     "peasant_deadbeef",
		KeyID:      "11111111-1111-1111-1111-111111111111",
		UserID:     "22222222-2222-2222-2222-222222222222",
		Username:   demoUsername,
		VillageURL: "http://localhost:8080",
	}

	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	want := []string{"api_key", "key_id", "user_id", "username", "village_url"}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("credentials.json missing key %q (have %v)", k, keysOf(got))
		}
	}
	if len(got) != len(want) {
		t.Errorf("credentials.json has unexpected keys: got %v, want exactly %v", keysOf(got), want)
	}

	var back demoCredentials
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
	if back != c {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", back, c)
	}
}

// TestValidateLocalDatabaseURL is the core fence: only loopback hosts pass, and
// the host is parsed (not substring-matched) so a userinfo spoof is rejected.
func TestValidateLocalDatabaseURL(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"localhost", "postgres://test:test@localhost:5432/db?sslmode=disable", false},
		{"127.0.0.1", "postgres://test:test@127.0.0.1:5432/db", false},
		{"ipv6 loopback", "postgres://test:test@[::1]:5432/db", false},
		{"localhost no port", "postgres://localhost/db", false},
		// Userinfo spoof: the REAL host is prod-db; net/url.Hostname() must see it.
		{"userinfo spoof localhost@prod", "postgres://localhost@prod-db:5432/app", true},
		{"userinfo spoof with creds", "postgres://localhost:pw@10.0.0.5/app", true},
		{"remote host", "postgres://test:test@db.prod.example.com:5432/app", true},
		{"private ip", "postgres://test@10.1.2.3:5432/app", true},
		{"unparseable", "::::not a url::::", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLocalDatabaseURL(tc.raw)
			if tc.wantErr && err == nil {
				t.Errorf("validateLocalDatabaseURL(%q) = nil, want an error", tc.raw)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateLocalDatabaseURL(%q) = %v, want nil", tc.raw, err)
			}
			// An error must be actionable: name the offending host and the rule.
			if tc.wantErr && err != nil {
				msg := err.Error()
				if !strings.Contains(msg, "village-setup-demo") {
					t.Errorf("error not actionable (no tool/context): %q", msg)
				}
			}
		})
	}
}

// TestServerDoesNotImportSetupDemo gates the dev-only fence structurally: the
// village server's transitive dependency set must NOT include the
// village-setup-demo package. (It is a main package, so Go cannot import it
// anyway — this catches any future refactor that tried to.)
func TestServerDoesNotImportSetupDemo(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH; skipping import-graph gate")
	}
	const serverPkg = "github.com/peasant-labs/village/backend/cmd/server"
	const demoPkg = "github.com/peasant-labs/village/backend/cmd/village-setup-demo"

	cmd := exec.Command("go", "list", "-deps", serverPkg)
	cmd.Dir = filepath.Join("..", "..") // backend module root, from cmd/village-setup-demo
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps %s: %v\n%s", serverPkg, err, out)
	}
	deps := string(out)

	// Sanity: the gate is not vacuous — the server must actually depend on a known
	// internal package.
	if !strings.Contains(deps, "github.com/peasant-labs/village/backend/internal/router") {
		t.Fatalf("server dep list looks wrong (no internal/router); gate would be vacuous:\n%s", deps)
	}
	if strings.Contains(deps, demoPkg) {
		t.Errorf("village server transitively imports the dev-only %s — it must never be in the prod build", demoPkg)
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
