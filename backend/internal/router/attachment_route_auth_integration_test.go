//go:build integration

package router

import (
	"context"
	_ "embed"
	"net/http/httptest"
	"os"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/redact"

	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
)

//go:embed testdata/attachment_route_auth.yaml
var attachmentRouteAuthYAML []byte

// requiredAttachmentRouteAuthNames is the name manifest for the fixture: every
// route exists to pin one policy decision, so losing a row must name itself.
var requiredAttachmentRouteAuthNames = []string{
	"confirm requires a session",
	"detach requires a session",
	"get pull request attachment is reachable anonymously",
	"prompt requests require a session",
	"reading settings requires a session",
	"updating settings requires a session",
}

type attachmentRouteAuthCase struct {
	Name      string `yaml:"name"`
	Method    string `yaml:"method"`
	Path      string `yaml:"path"`
	Anonymous int    `yaml:"anonymous"`
}

type attachmentRouteAuthFile struct {
	Routes []attachmentRouteAuthCase `yaml:"routes"`
}

// TestAttachmentRouteAuth drives the production router with no session and
// asserts the policy each route declares: the read route must stay reachable
// anonymously (it answers 404 for an attachment that does not exist, never 401),
// and the mutating routes must refuse before doing anything.
func TestAttachmentRouteAuth(t *testing.T) {
	file, err := decodeSingleYAMLDocument[attachmentRouteAuthFile](attachmentRouteAuthYAML)
	if err != nil {
		t.Fatalf("load the attachment route auth fixture: %v", err)
	}
	seen := map[string]bool{}
	for _, row := range file.Routes {
		if seen[row.Name] {
			t.Fatalf("the attachment route auth fixture repeats %q", row.Name)
		}
		seen[row.Name] = true
	}
	assertExactNames(t, "attachment_route_auth", seen, requiredAttachmentRouteAuthNames)

	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run the mounted router auth test")
	}
	ctx := context.Background()
	pool, err := database.PoolConfig(databaseURL)
	if err != nil {
		t.Fatalf("build the pool config: %v", err)
	}
	db, err := pgxpool.NewWithConfig(ctx, pool)
	if err != nil {
		t.Skipf("cannot reach the test database: %v", err)
	}
	defer db.Close()
	if err := database.RunMigrations(db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	titles, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatalf("construct the title pipeline: %v", err)
	}
	handler := New(&config.Config{JWTSecret: "attachment-route-auth", FrontendURL: "https://app.example.com"}, db, nil, titles)

	for _, row := range file.Routes {
		t.Run(row.Name, func(t *testing.T) {
			req := httptest.NewRequest(row.Method, row.Path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != row.Anonymous {
				t.Fatalf("anonymous %s %s answered %d, want %d", row.Method, row.Path, rec.Code, row.Anonymous)
			}
		})
	}
}

// assertExactNames holds a fixture to exact membership against its manifest in
// both directions, so a removed route is named and an added one cannot slip in
// unprotected.
func assertExactNames(t *testing.T, fixture string, present map[string]bool, required []string) {
	t.Helper()
	declared := map[string]bool{}
	for _, name := range required {
		declared[name] = true
	}
	var missing, undeclared []string
	for name := range declared {
		if !present[name] {
			missing = append(missing, name)
		}
	}
	for name := range present {
		if !declared[name] {
			undeclared = append(undeclared, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(undeclared)
	if len(missing) > 0 {
		t.Fatalf("testdata/%s.yaml no longer carries %v, which its manifest declares: restore the row rather than deleting the name.", fixture, missing)
	}
	if len(undeclared) > 0 {
		t.Fatalf("testdata/%s.yaml carries %v, which its manifest does not declare: add each new name in the same change.", fixture, undeclared)
	}
}
