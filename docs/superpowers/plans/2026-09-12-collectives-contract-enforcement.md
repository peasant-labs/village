# Collectives Contract Enforcement and Route Drift Gate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the Village API document village serves the one it enforces for the collectives mutations, derive the frontend's collectives types from the published contract package, and add a CI gate that fails when a mounted `/api/v1` route is absent from the served document.

**Architecture:** A test in the router package walks the real chi router and compares it with the served spec plus a manifest of routes known to be undeclared. A handler-package validator compiles request-body schemas from the same spec bytes on first use and the nine collectives mutation handlers validate through it before decoding. The frontend re-exports its collectives types as aliases of the package's generated types under the existing names.

**Tech Stack:** Go 1.25, chi v5, `github.com/santhosh-tekuri/jsonschema/v5` (already an indirect dependency through the schema module), `gopkg.in/yaml.v3`, `github.com/peasant-labs/schema` v0.20.0 (Go) and `@peasant-labs/schema` 0.20.0 (npm), Next 16, vitest.

**Spec:** `docs/superpowers/specs/2026-09-12-collectives-contract-enforcement-design.md`

## Global Constraints

- Work only in the worktree `/Users/pigeonzow/Documents/GitHub/polyrepo/village/village-99--feat--contract-enforcement-and-drift-gate`. Never touch `village/develop`.
- Backend commands run from `<worktree>/backend`; frontend commands run from `<worktree>/frontend` after `source ~/.nvm/nvm.sh && nvm use default`.
- Every test case lives in a `testdata/*.yaml` fixture with a required-name manifest in the test code, except the route manifest, where the gate itself is the deletion protection.
- Schema-invalid bodies on the nine collectives operations answer **400** with the prefix `request body failed contract validation: `. Invalid JSON keeps the existing `Invalid request body` 400. Publish and annotation keep their 422.
- No path, URL, or absolute location may appear in a validation message. No transcript content is logged.
- No handler-only validation rule: the enforced schemas come from `schema.VillageAPISpecJSON()`, the same bytes the server serves.
- No taxonomy tokens (issue numbers as labels, slice names) in shipped code or comments. Describe by substance.
- Commit messages end with the attribution lines given in the session's system reminder.
- Docker is not running locally: `go test -tags=integration` cannot run here. Run the unit suite (`go test -race ./...`) and rely on CI for integration. Say so in the PR body.

---

### Task 1: Move both schema pins to v0.20.0 and bump the served-version guard

**Files:**
- Modify: `backend/go.mod`, `backend/go.sum` (already modified in the worktree, uncommitted)
- Modify: `frontend/package.json`, `pnpm-lock.yaml` (already modified in the worktree, uncommitted)
- Modify: `backend/internal/handler/openapi_test.go:346`

**Interfaces:**
- Produces: `schema.NewJSONSchemaCompiler()` and `schema.VillageAPIVersion == "0.18.0"` available to later tasks.

- [ ] **Step 1: Confirm the worktree already carries the pin changes**

Run from the worktree root:
```bash
git status --short
grep -n 'peasant-labs/schema' backend/go.mod frontend/package.json
```
Expected: `M backend/go.mod`, `M backend/go.sum`, `M frontend/package.json`, `M pnpm-lock.yaml`; both pins read `0.20.0`. If `go.work.sum` shows as modified, run `git checkout -- go.work.sum`.

- [ ] **Step 2: Run the version guard to see it fail**

Run from `backend/`:
```bash
go test -run TestPinnedContractVersion_MatchesExpected ./internal/handler/
```
Expected: FAIL with `pinned schema module reports VillageAPIVersion "0.18.0", want "0.16.0"`.

- [ ] **Step 3: Bump the guard**

In `backend/internal/handler/openapi_test.go` change line 346:
```go
const wantVillageAPIVersion = "0.18.0"
```

- [ ] **Step 4: Run the guard and the build**

Run from `backend/`:
```bash
go test -run TestPinnedContractVersion_MatchesExpected ./internal/handler/ && go build ./... && go vet ./...
```
Expected: PASS, build and vet clean.

- [ ] **Step 5: Typecheck the frontend on the new pin**

Run from `frontend/`:
```bash
source ~/.nvm/nvm.sh && nvm use default >/dev/null && pnpm exec tsc --noEmit
```
Expected: no output (clean).

- [ ] **Step 6: Commit**

```bash
git add backend/go.mod backend/go.sum frontend/package.json pnpm-lock.yaml backend/internal/handler/openapi_test.go
git commit -m "chore(deps): re-pin schema v0.20.0 and serve Village API 0.18.0

The module's canonical JSON-schema compiler and the digest's per-commit
change counts both arrive with this tag. The consumer-side version guard
moves with the pin.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_011vyps39XMVMMrQmc6u1FeJ"
```

---

### Task 2: Route drift comparison, fixture-driven

**Files:**
- Create: `backend/internal/router/contract_drift_test.go`
- Create: `backend/internal/router/testdata/contract_drift_cases.yaml`

**Interfaces:**
- Produces: `type route struct{ Method, Path string }`, `func contractDriftFindings(mounted, declared, manifest []route) []string` (sorted findings, empty when clean), `func routeKey(method, path string) string` (parameter names erased). Task 3 consumes all three.

- [ ] **Step 1: Write the fixture**

Create `backend/internal/router/testdata/contract_drift_cases.yaml`:
```yaml
# Cases for the route drift comparison. Each case hands the comparison three lists:
# what the server mounts, what the served contract declares, and what the manifest of
# known-undeclared routes names. `findings` is the exact sorted list the comparison
# must return; an empty list means the gate passes.

- name: mounted route absent from the contract and the manifest is a finding
  why: >
    The drift this gate exists to catch. A route the server answers but the
    contract never describes is invisible to every client and validated by
    nothing.
  mounted:
    - {method: GET, path: /api/v1/groups/{id}}
    - {method: POST, path: /api/v1/groups/{id}/shares}
  declared:
    - {method: GET, path: /api/v1/groups/{id}}
  manifest: []
  findings:
    - "POST /api/v1/groups/{id}/shares is mounted but the served contract does not declare it; declare it in the schema module and re-pin, or add it to the undocumented-routes manifest with a reason"

- name: manifest row no longer mounted is a finding
  why: >
    A row for a route that was removed is dead weight that would let a future
    route reuse the exemption by accident.
  mounted: []
  declared: []
  manifest:
    - {method: GET, path: /api/v1/tags}
  findings:
    - "GET /api/v1/tags is in the undocumented-routes manifest but the server no longer mounts it; delete the row"

- name: manifest row the contract now declares is a finding
  why: >
    The manifest may only shrink. Once the contract declares a route, its row
    must go, so the manifest never masks a real drift later.
  mounted:
    - {method: GET, path: /api/v1/tags}
  declared:
    - {method: GET, path: /api/v1/tags}
  manifest:
    - {method: GET, path: /api/v1/tags}
  findings:
    - "GET /api/v1/tags is in the undocumented-routes manifest but the served contract now declares it; delete the row"

- name: duplicate manifest row is a finding
  why: >
    Two rows for one route hide an edit that removed the wrong one.
  mounted:
    - {method: GET, path: /api/v1/tags}
  declared: []
  manifest:
    - {method: GET, path: /api/v1/tags}
    - {method: GET, path: /api/v1/tags}
  findings:
    - "GET /api/v1/tags appears twice in the undocumented-routes manifest; keep one row"

- name: mounted route the contract declares passes
  why: >
    The normal state for every collectives route after the contract landed.
  mounted:
    - {method: PATCH, path: /api/v1/groups/{id}/shares/{transcriptID}}
  declared:
    - {method: PATCH, path: /api/v1/groups/{id}/shares/{transcriptID}}
  manifest: []
  findings: []

- name: manifest row still mounted and still undeclared passes
  why: >
    The exemption works while the route is genuinely undeclared.
  mounted:
    - {method: GET, path: /api/v1/tags}
  declared: []
  manifest:
    - {method: GET, path: /api/v1/tags}
  findings: []

- name: path parameter names do not matter
  why: >
    The router says {id} where the contract says {groupId}. The same segment
    position is the same route; only the names differ.
  mounted:
    - {method: GET, path: /api/v1/users/me/collectives/{groupId}/submissions}
  declared:
    - {method: GET, path: /api/v1/users/me/collectives/{id}/submissions}
  manifest: []
  findings: []

- name: declared route that is not mounted is not a finding
  why: >
    Routes the contract declares ahead of their handlers are a separate
    concern with its own fixture; this gate only asks whether what is served
    is described.
  mounted: []
  declared:
    - {method: GET, path: /api/v1/pulls/{owner}/{name}/{number}}
  manifest: []
  findings: []
```

- [ ] **Step 2: Write the failing test**

Create `backend/internal/router/contract_drift_test.go`:
```go
package router

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

//go:embed testdata/contract_drift_cases.yaml
var contractDriftCasesYAML []byte

// route is one method and path pair. Path parameter names are erased before
// comparison: the router's {id} and the contract's {groupId} at the same
// segment name the same route.
type route struct {
	Method string `yaml:"method"`
	Path   string `yaml:"path"`
}

type contractDriftCase struct {
	Name     string   `yaml:"name"`
	Why      string   `yaml:"why"`
	Mounted  []route  `yaml:"mounted"`
	Declared []route  `yaml:"declared"`
	Manifest []route  `yaml:"manifest"`
	Findings []string `yaml:"findings"`
}

// requiredContractDriftCases names the cases that must exist. It lives here
// rather than in the fixture so deleting fixture rows cannot delete the
// manifest that protects them.
var requiredContractDriftCases = []string{
	"mounted route absent from the contract and the manifest is a finding",
	"manifest row no longer mounted is a finding",
	"manifest row the contract now declares is a finding",
	"duplicate manifest row is a finding",
	"mounted route the contract declares passes",
	"manifest row still mounted and still undeclared passes",
	"path parameter names do not matter",
	"declared route that is not mounted is not a finding",
}

func decodeSingleYAMLDocument[T any](data []byte) (T, error) {
	var out T
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&out); err != nil {
		return out, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return out, fmt.Errorf("fixture must contain exactly one YAML document")
	}
	return out, nil
}

func loadContractDriftCases(t *testing.T) []contractDriftCase {
	t.Helper()
	cases, err := decodeSingleYAMLDocument[[]contractDriftCase](contractDriftCasesYAML)
	if err != nil {
		t.Fatalf("load the contract drift fixture: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		if present[c.Name] {
			t.Fatalf("the contract drift fixture repeats case %q", c.Name)
		}
		present[c.Name] = true
	}
	for _, required := range requiredContractDriftCases {
		if !present[required] {
			t.Fatalf("the contract drift fixture omits required case %q; restore it rather than removing it from the manifest", required)
		}
	}
	return cases
}

func TestContractDriftFindings_Fixture(t *testing.T) {
	for _, c := range loadContractDriftCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			got := contractDriftFindings(c.Mounted, c.Declared, c.Manifest)
			want := append([]string{}, c.Findings...)
			sort.Strings(want)
			if len(got) == 0 && len(want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("findings mismatch\n got: %s\nwant: %s", strings.Join(got, "\n      "), strings.Join(want, "\n      "))
			}
		})
	}
}

var _ = regexp.MustCompile // keep the import used until Step 4 adds routeKey
```

- [ ] **Step 3: Run it to see it fail**

Run from `backend/`:
```bash
go test -run TestContractDriftFindings_Fixture ./internal/router/
```
Expected: build failure `undefined: contractDriftFindings`.

- [ ] **Step 4: Implement the comparison**

Replace the last line of `contract_drift_test.go` (`var _ = regexp.MustCompile ...`) with:
```go
var pathParameter = regexp.MustCompile(`\{[^}]*\}`)

// routeKey identifies a route by method and by path with parameter names erased.
func routeKey(method, path string) string {
	return strings.ToUpper(method) + " " + pathParameter.ReplaceAllString(path, "{}")
}

// contractDriftFindings compares the routes the server mounts with the routes
// the served contract declares and with the manifest of routes known to be
// undeclared. Each finding is one sentence naming the route and the fix. The
// result is sorted so fixtures can state it exactly.
func contractDriftFindings(mounted, declared, manifest []route) []string {
	declaredSet := map[string]bool{}
	for _, r := range declared {
		declaredSet[routeKey(r.Method, r.Path)] = true
	}
	mountedSet := map[string]bool{}
	for _, r := range mounted {
		mountedSet[routeKey(r.Method, r.Path)] = true
	}
	manifestSet := map[string]bool{}
	var findings []string
	for _, r := range manifest {
		key := routeKey(r.Method, r.Path)
		if manifestSet[key] {
			findings = append(findings, fmt.Sprintf("%s %s appears twice in the undocumented-routes manifest; keep one row", r.Method, r.Path))
			continue
		}
		manifestSet[key] = true
		if !mountedSet[key] {
			findings = append(findings, fmt.Sprintf("%s %s is in the undocumented-routes manifest but the server no longer mounts it; delete the row", r.Method, r.Path))
		}
		if declaredSet[key] {
			findings = append(findings, fmt.Sprintf("%s %s is in the undocumented-routes manifest but the served contract now declares it; delete the row", r.Method, r.Path))
		}
	}
	for _, r := range mounted {
		key := routeKey(r.Method, r.Path)
		if !declaredSet[key] && !manifestSet[key] {
			findings = append(findings, fmt.Sprintf("%s %s is mounted but the served contract does not declare it; declare it in the schema module and re-pin, or add it to the undocumented-routes manifest with a reason", r.Method, r.Path))
		}
	}
	sort.Strings(findings)
	return findings
}
```

- [ ] **Step 5: Run the fixture test**

Run from `backend/`:
```bash
go test -run TestContractDriftFindings_Fixture -v ./internal/router/ 2>&1 | grep -E '^(--- |=== RUN|ok|FAIL|PASS)' | grep -v '=== RUN'
```
Expected: eight `--- PASS` subtests, then `PASS`.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/router/contract_drift_test.go backend/internal/router/testdata/contract_drift_cases.yaml
git commit -m "test(router): fixture-driven route drift comparison

Compares mounted routes with the served contract and a manifest of routes
known to be undeclared; parameter names are erased so a renamed placeholder
is not a false drift.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_011vyps39XMVMMrQmc6u1FeJ"
```

---

### Task 3: The route drift gate and the manifest of undeclared routes

**Files:**
- Modify: `backend/internal/router/contract_drift_test.go`
- Create: `backend/internal/router/testdata/undocumented_routes.yaml`

**Interfaces:**
- Consumes: `route`, `routeKey`, `contractDriftFindings` from Task 2.
- Produces: `func mountedAPIRoutes(t *testing.T) []route` (Task 5 adds one more assertion in this file).

- [ ] **Step 1: Add the gate test (fails until the manifest exists)**

Append to `backend/internal/router/contract_drift_test.go`. Add these imports to the import block: `"encoding/json"`, `"net/http"`, `"github.com/go-chi/chi/v5"`, `"github.com/peasant-labs/redact"`, `"github.com/peasant-labs/schema"`, `"github.com/peasant-labs/village/backend/internal/config"`.

```go
//go:embed testdata/undocumented_routes.yaml
var undocumentedRoutesYAML []byte

type manifestRoute struct {
	Method string `yaml:"method"`
	Path   string `yaml:"path"`
	Reason string `yaml:"reason"`
}

type undocumentedRoutesFile struct {
	Routes []manifestRoute `yaml:"routes"`
}

func loadUndocumentedRoutes(t *testing.T) []route {
	t.Helper()
	file, err := decodeSingleYAMLDocument[undocumentedRoutesFile](undocumentedRoutesYAML)
	if err != nil {
		t.Fatalf("load the undocumented-routes manifest: %v", err)
	}
	routes := make([]route, 0, len(file.Routes))
	for _, r := range file.Routes {
		if strings.TrimSpace(r.Reason) == "" {
			t.Fatalf("%s %s in the undocumented-routes manifest has no reason; say why the contract does not declare it yet", r.Method, r.Path)
		}
		routes = append(routes, route{Method: r.Method, Path: r.Path})
	}
	return routes
}

// mountedAPIRoutes builds the production router with no database, blob store,
// or GitHub App (construction touches none of them) and walks every route
// mounted under /api/v1.
func mountedAPIRoutes(t *testing.T) []route {
	t.Helper()
	titles, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatalf("construct the title pipeline: %v", err)
	}
	handler := New(&config.Config{FrontendURL: "https://app.example.com"}, nil, nil, titles)
	routes, ok := handler.(chi.Routes)
	if !ok {
		t.Fatalf("router is %T, not chi.Routes", handler)
	}
	var out []route
	err = chi.Walk(routes, func(method, path string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(path, "/api/v1/") {
			out = append(out, route{Method: method, Path: path})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the router: %v", err)
	}
	return out
}

// declaredContractRoutes reads the operations from the same bytes the server
// serves at /api/v1/openapi.json.
func declaredContractRoutes(t *testing.T) []route {
	t.Helper()
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(schema.VillageAPISpecJSON(), &doc); err != nil {
		t.Fatalf("parse the served contract: %v", err)
	}
	var out []route
	for path, operations := range doc.Paths {
		for method := range operations {
			switch strings.ToUpper(method) {
			case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
				out = append(out, route{Method: strings.ToUpper(method), Path: path})
			}
		}
	}
	return out
}

// TestContractDriftGate_MountedRoutesAreDeclaredOrListed is the CI gate: every
// route the server mounts under /api/v1 is either declared by the served
// contract or listed, with a reason, in the undocumented-routes manifest. The
// manifest can only shrink: a row for a route the contract now declares, or
// for a route no longer mounted, is itself a failure.
func TestContractDriftGate_MountedRoutesAreDeclaredOrListed(t *testing.T) {
	mounted := mountedAPIRoutes(t)
	declared := declaredContractRoutes(t)
	manifest := loadUndocumentedRoutes(t)
	for _, finding := range contractDriftFindings(mounted, declared, manifest) {
		t.Error(finding)
	}
	mountedSet := map[string]bool{}
	for _, r := range mounted {
		mountedSet[routeKey(r.Method, r.Path)] = true
	}
	for _, r := range declared {
		if !mountedSet[routeKey(r.Method, r.Path)] {
			t.Logf("declared but not mounted (informational): %s %s", r.Method, r.Path)
		}
	}
}
```

- [ ] **Step 2: Run the gate to see it fail**

Run from `backend/`:
```bash
go test -run TestContractDriftGate ./internal/router/ 2>&1 | head -5
```
Expected: build failure on the missing embed (`pattern testdata/undocumented_routes.yaml: no matching files found`).

- [ ] **Step 3: Write the manifest**

Create `backend/internal/router/testdata/undocumented_routes.yaml`:
```yaml
# Routes the server mounts under /api/v1 that no version of the Village API
# contract declares yet. The route drift gate accepts a mounted route only when
# the served contract declares it or this file names it. A row here must still
# be mounted and must still be absent from the contract, otherwise the gate
# fails, so this list can only shrink. Declaring these routes is tracked in the
# schema repository; when a declaration lands and the pin moves, delete the row.

routes:
  - {method: GET, path: /api/v1/openapi.json, reason: serves the contract document itself}
  - {method: POST, path: /api/v1/auth/logout, reason: session logout, predates the contract}
  - {method: GET, path: /api/v1/auth/me, reason: signed-in account read, predates the contract}
  - {method: DELETE, path: /api/v1/auth/me, reason: account deletion, predates the contract}
  - {method: PATCH, path: /api/v1/auth/me/settings, reason: discoverability setting; the contract declares a separate users/me/settings route for the preview flag, and folding the two is a pending decision}
  - {method: PATCH, path: /api/v1/auth/me/username, reason: username choice, predates the contract}
  - {method: POST, path: /api/v1/auth/api-keys, reason: API key creation, predates the contract}
  - {method: GET, path: /api/v1/auth/api-keys, reason: API key list, predates the contract}
  - {method: DELETE, path: /api/v1/auth/api-keys/{id}, reason: API key revocation, predates the contract}
  - {method: GET, path: /api/v1/auth/orgs, reason: caller's organisations, predates the contract}
  - {method: PATCH, path: /api/v1/auth/orgs/{orgLogin}/visibility, reason: organisation visibility toggle, predates the contract}
  - {method: GET, path: /api/v1/auth/github, reason: OAuth entry point, redirects to the provider}
  - {method: GET, path: /api/v1/auth/github/callback, reason: OAuth callback, redirects to the frontend}
  - {method: GET, path: /api/v1/auth/gitlab, reason: OAuth entry point, redirects to the provider}
  - {method: GET, path: /api/v1/auth/gitlab/callback, reason: OAuth callback, redirects to the frontend}
  - {method: GET, path: /api/v1/auth/huggingface, reason: OAuth entry point, redirects to the provider}
  - {method: GET, path: /api/v1/auth/huggingface/callback, reason: OAuth callback, redirects to the frontend}
  - {method: GET, path: /api/v1/auth/codeberg, reason: OAuth entry point, redirects to the provider}
  - {method: GET, path: /api/v1/auth/codeberg/callback, reason: OAuth callback, redirects to the frontend}
  - {method: GET, path: /api/v1/auth/sourcehut, reason: OAuth entry point, redirects to the provider}
  - {method: GET, path: /api/v1/auth/sourcehut/callback, reason: OAuth callback, redirects to the frontend}
  - {method: GET, path: /api/v1/orgs/search, reason: organisation search, predates the contract}
  - {method: GET, path: /api/v1/orgs/{login}, reason: organisation page, predates the contract}
  - {method: GET, path: /api/v1/tags, reason: tag list, predates the contract}
  - {method: GET, path: /api/v1/tags/popular, reason: popular tags, predates the contract}
  - {method: GET, path: /api/v1/transcripts/{id}/annotations, reason: annotation list read, predates the contract}
  - {method: POST, path: /api/v1/transcripts/{id}/annotations, reason: manual annotation creation, predates the contract}
  - {method: GET, path: /api/v1/transcripts/{id}/attestations, reason: attestation list, predates the contract}
  - {method: POST, path: /api/v1/transcripts/{id}/attestations, reason: attestation creation, predates the contract}
  - {method: DELETE, path: /api/v1/transcripts/{id}/attestations/{attestationID}, reason: attestation deletion, predates the contract}
  - {method: GET, path: /api/v1/transcripts/{id}/commits, reason: transcript commit list, predates the contract}
  - {method: DELETE, path: /api/v1/transcripts/{id}, reason: transcript deletion, predates the contract}
  - {method: POST, path: /api/v1/transcripts/publish/batch, reason: answers 501 today; whether to declare or remove it is a pending decision}
  - {method: GET, path: /api/v1/users/{username}, reason: public profile, predates the contract}
  - {method: GET, path: /api/v1/users/{username}/orgs, reason: public organisations, predates the contract}
  - {method: GET, path: /api/v1/users/{username}/projects/{projectHash}, reason: project page, predates the contract}
  - {method: PATCH, path: /api/v1/users/me/projects/{projectHash}, reason: project display name override, predates the contract}
  - {method: DELETE, path: /api/v1/users/me/projects/{projectHash}/display-name, reason: project display name reset, predates the contract}
```

- [ ] **Step 4: Run the gate**

Run from `backend/`:
```bash
go test -run 'TestContractDrift' -v ./internal/router/ 2>&1 | grep -E '^(--- |ok|FAIL|PASS)|declared but not mounted'
```
Expected: `--- PASS` for both tests, exactly eight `declared but not mounted` informational lines (the seven attachment routes and the transcript-group members route), then `PASS`. If a finding prints, the manifest has a typo: fix the row, never the gate.

- [ ] **Step 5: Prove the gate goes red, then restore**

Temporarily add a line inside the `/api/v1` route group in `backend/internal/router/router.go`, right after the `openapi.json` line:
```go
		r.Get("/drift-probe", h.Health)
```
Run:
```bash
go test -run TestContractDriftGate ./internal/router/ 2>&1 | grep -E 'drift-probe|FAIL'
```
Expected: `GET /api/v1/drift-probe is mounted but the served contract does not declare it; ...` and `FAIL`. Then delete the probe line and re-run: PASS. Confirm `git diff backend/internal/router/router.go` is empty.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/router/contract_drift_test.go backend/internal/router/testdata/undocumented_routes.yaml
git commit -m "test(router): gate mounted routes against the served contract

Walks the production router and fails when a mounted /api/v1 route is
neither declared by the served Village API document nor named, with a
reason, in the manifest of routes the contract does not describe yet. A
manifest row for a route the contract now declares, or for a route no
longer mounted, fails the gate too, so the manifest only shrinks.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_011vyps39XMVMMrQmc6u1FeJ"
```

---

### Task 4: Contract body validator compiled from the served spec

**Files:**
- Create: `backend/internal/handler/contract_body.go`
- Modify: `backend/internal/handler/openapi.go:28-31` (interface) and the `moduleValidator` block
- Create: `backend/internal/handler/contract_body_test.go`
- Create: `backend/internal/handler/testdata/contract_body_operations.yaml`

**Interfaces:**
- Consumes: `schema.NewJSONSchemaCompiler()`, `schema.VillageAPISpecJSON()` (Task 1), `ErrSchemaInvalid`, `payloadValidator` (existing).
- Produces:
  - `type ContractOperation struct{ Method, Path string }` with `String()`.
  - Nine unexported vars `opCreateGroup`, `opUpdateGroup`, `opAddGroupMember`, `opUpdateGroupMemberRole`, `opLinkGroupRepository`, `opBatchShareProject`, `opBatchReviewShares`, `opReviewShare`, `opShareTranscript`.
  - `func ContractEnforcedOperations() []ContractOperation`.
  - `PayloadValidator.ValidateBody(method, path string, raw []byte) error`.
  - `var ErrContractBodyUndeclared error`.
  - `func renderContractViolation(err error) string`.

- [ ] **Step 1: Write the fixture**

Create `backend/internal/handler/testdata/contract_body_operations.yaml`:
```yaml
# One row per operation whose request body the handlers validate against the
# served Village API contract. `conforming` must pass the contract. Each
# malformed body names the substring the rendered violation must contain. The
# rendered text is "<instance pointer>: <message>" per failing leaf, joined by
# "; ", with the root pointer rendered as "/".

- name: create group
  method: POST
  path: /api/v1/groups
  conforming: '{"name":"Night Shift","description":"late sessions","acceptance_mode":"open","data_access":"members_only"}'
  malformed:
    - name: name missing
      body: '{"description":"no name"}'
      expect: "/: missing properties: 'name'"
    - name: name is a number
      body: '{"name":7}'
      expect: "/name: expected string, but got number"
    - name: acceptance mode outside the closed set
      body: '{"name":"x","acceptance_mode":"whenever"}'
      expect: '/acceptance_mode: value must be one of "open", "verified_only", "curated"'

- name: update group
  method: PATCH
  path: /api/v1/groups/{id}
  conforming: '{"name":"Renamed","display_members":true}'
  malformed:
    - name: data access outside the closed set
      body: '{"data_access":"everyone"}'
      expect: '/data_access: value must be one of "members_only", "contributors", "public"'
    - name: display members is a string
      body: '{"display_members":"yes"}'
      expect: "/display_members: expected"

- name: add group member
  method: POST
  path: /api/v1/groups/{id}/members
  conforming: '{"username":"octocat"}'
  malformed:
    - name: username missing
      body: '{}'
      expect: "/: missing properties: 'username'"

- name: update group member role
  method: PATCH
  path: /api/v1/groups/{id}/members/{userID}/role
  conforming: '{"role":"member"}'
  malformed:
    - name: role outside the assignable set
      body: '{"role":"owner"}'
      expect: '/role: value must be one of "contributor", "member"'

- name: link group repository
  method: POST
  path: /api/v1/groups/{id}/repositories
  conforming: '{"owner":"acme","name":"repo","installation_id":42}'
  malformed:
    - name: installation id is a string
      body: '{"owner":"acme","name":"repo","installation_id":"42"}'
      expect: "/installation_id: expected integer, but got string"
    - name: owner missing
      body: '{"name":"repo","installation_id":42}'
      expect: "/: missing properties: 'owner'"

- name: batch share project
  method: POST
  path: /api/v1/groups/{id}/shares
  conforming: '{"project_hash":"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2","visibility_confirmed":true}'
  malformed:
    - name: project hash is not a hash
      body: '{"project_hash":"not-a-hash","visibility_confirmed":true}'
      expect: "/project_hash: "
    - name: visibility confirmation missing
      body: '{"project_hash":"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"}'
      expect: "/: missing properties: 'visibility_confirmed'"

- name: batch review shares
  method: PATCH
  path: /api/v1/groups/{id}/shares
  conforming: '{"transcript_ids":["3f0d8b1e-2c4a-4f6e-9a1b-7c2d3e4f5a6b"],"status":"approved"}'
  malformed:
    - name: status outside the decision set
      body: '{"transcript_ids":["3f0d8b1e-2c4a-4f6e-9a1b-7c2d3e4f5a6b"],"status":"maybe"}'
      expect: '/status: value must be one of "approved", "rejected"'
    - name: transcript ids is a string
      body: '{"transcript_ids":"3f0d8b1e-2c4a-4f6e-9a1b-7c2d3e4f5a6b","status":"approved"}'
      expect: "/transcript_ids: expected array, but got string"

- name: review share
  method: PATCH
  path: /api/v1/groups/{id}/shares/{transcriptID}
  conforming: '{"status":"rejected"}'
  malformed:
    - name: status missing
      body: '{}'
      expect: "/: missing properties: 'status'"

- name: share transcript with groups
  method: POST
  path: /api/v1/transcripts/{id}/share
  conforming: '{"group_ids":["3f0d8b1e-2c4a-4f6e-9a1b-7c2d3e4f5a6b"]}'
  malformed:
    - name: group ids missing
      body: '{"groups":[]}'
      expect: "/: missing properties: 'group_ids'"
    - name: group id is not a uuid
      body: '{"group_ids":["nope"]}'
      expect: "/group_ids/0: "
```

If a substring in this fixture does not match the compiler's actual wording when the test first runs, read the failure output, fix the fixture's `expect` to the substring the compiler really produces, and keep the row. Never loosen a row to the empty string.

- [ ] **Step 2: Write the failing validator tests**

Create `backend/internal/handler/contract_body_test.go`:
```go
package handler

import (
	_ "embed"
	"errors"
	"net/http"
	"strings"
	"testing"
)

//go:embed testdata/contract_body_operations.yaml
var contractBodyOperationsYAML []byte

type contractBodyMalformed struct {
	Name   string `yaml:"name"`
	Body   string `yaml:"body"`
	Expect string `yaml:"expect"`
}

type contractBodyOperationCase struct {
	Name       string                  `yaml:"name"`
	Method     string                  `yaml:"method"`
	Path       string                  `yaml:"path"`
	Conforming string                  `yaml:"conforming"`
	Malformed  []contractBodyMalformed `yaml:"malformed"`
}

// requiredContractBodyOperations names the operations the fixture must cover,
// keyed by method and path. It lives here so deleting fixture rows cannot
// delete the manifest that protects them.
var requiredContractBodyOperations = []string{
	"POST /api/v1/groups",
	"PATCH /api/v1/groups/{id}",
	"POST /api/v1/groups/{id}/members",
	"PATCH /api/v1/groups/{id}/members/{userID}/role",
	"POST /api/v1/groups/{id}/repositories",
	"POST /api/v1/groups/{id}/shares",
	"PATCH /api/v1/groups/{id}/shares",
	"PATCH /api/v1/groups/{id}/shares/{transcriptID}",
	"POST /api/v1/transcripts/{id}/share",
}

func loadContractBodyOperations(t *testing.T) []contractBodyOperationCase {
	t.Helper()
	cases, err := decodeFixtureRows[contractBodyOperationCase](contractBodyOperationsYAML)
	if err != nil {
		t.Fatalf("load the contract body fixture: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		key := c.Method + " " + c.Path
		if present[key] {
			t.Fatalf("the contract body fixture repeats operation %q", key)
		}
		if len(c.Malformed) == 0 {
			t.Fatalf("operation %q has no malformed body; every enforced operation needs at least one", key)
		}
		if strings.TrimSpace(c.Conforming) == "" {
			t.Fatalf("operation %q has no conforming body", key)
		}
		for _, m := range c.Malformed {
			if strings.TrimSpace(m.Expect) == "" {
				t.Fatalf("operation %q malformed case %q expects nothing; name the violation substring", key, m.Name)
			}
		}
		present[key] = true
	}
	for _, required := range requiredContractBodyOperations {
		if !present[required] {
			t.Fatalf("the contract body fixture omits required operation %q; restore it rather than removing it from the manifest", required)
		}
	}
	return cases
}

func TestContractBody_FixtureMatchesEnforcedOperations(t *testing.T) {
	inFixture := map[string]bool{}
	for _, c := range loadContractBodyOperations(t) {
		inFixture[c.Method+" "+c.Path] = true
	}
	for _, op := range ContractEnforcedOperations() {
		if !inFixture[op.String()] {
			t.Errorf("enforced operation %s has no fixture row", op)
		}
	}
	if got, want := len(ContractEnforcedOperations()), len(inFixture); got != want {
		t.Errorf("enforced operations = %d, fixture rows = %d; the two lists must name the same operations", got, want)
	}
}

func TestContractBody_ValidatorRejectsMalformedAndAcceptsConforming(t *testing.T) {
	v := moduleValidator{}
	for _, c := range loadContractBodyOperations(t) {
		t.Run(c.Name, func(t *testing.T) {
			if err := v.ValidateBody(c.Method, c.Path, []byte(c.Conforming)); err != nil {
				t.Fatalf("conforming body rejected: %v", err)
			}
			for _, m := range c.Malformed {
				err := v.ValidateBody(c.Method, c.Path, []byte(m.Body))
				if err == nil {
					t.Errorf("%s: malformed body accepted", m.Name)
					continue
				}
				if !errors.Is(err, ErrSchemaInvalid) {
					t.Errorf("%s: error is not ErrSchemaInvalid: %v", m.Name, err)
				}
				if !strings.Contains(err.Error(), m.Expect) {
					t.Errorf("%s: violation %q does not contain %q", m.Name, err.Error(), m.Expect)
				}
				for _, forbidden := range []string{"file://", "contract://", "village-api", "/Users/", "\\"} {
					if strings.Contains(err.Error(), forbidden) {
						t.Errorf("%s: violation leaks a location: %q", m.Name, err.Error())
					}
				}
			}
		})
	}
}

func TestContractBody_UndeclaredOperationFailsClosed(t *testing.T) {
	v := moduleValidator{}
	err := v.ValidateBody(http.MethodPost, "/api/v1/groups/{id}/join", []byte(`{}`))
	if !errors.Is(err, ErrContractBodyUndeclared) {
		t.Fatalf("expected ErrContractBodyUndeclared, got %v", err)
	}
	err = v.ValidateBody(http.MethodGet, "/api/v1/groups", []byte(`{}`))
	if !errors.Is(err, ErrContractBodyUndeclared) {
		t.Fatalf("expected ErrContractBodyUndeclared for an operation without a body, got %v", err)
	}
}

func TestContractBody_InvalidJSONIsSchemaInvalid(t *testing.T) {
	v := moduleValidator{}
	err := v.ValidateBody(http.MethodPost, "/api/v1/groups", []byte(`{"name":`))
	if !errors.Is(err, ErrSchemaInvalid) {
		t.Fatalf("expected ErrSchemaInvalid for invalid JSON, got %v", err)
	}
}
```

- [ ] **Step 3: Run to see them fail**

Run from `backend/`:
```bash
go test -run 'TestContractBody' ./internal/handler/ 2>&1 | head -5
```
Expected: build failure `undefined: ContractEnforcedOperations` (and `ValidateBody`).

- [ ] **Step 4: Extend the validator interface**

In `backend/internal/handler/openapi.go`, replace the `PayloadValidator` interface with:
```go
// PayloadValidator validates a decoded request body against the contract schema.
// A non-nil error means the body is invalid and the handler must reject it: 422 on
// the publish and annotation paths, 400 on the operations ValidateBody covers.
type PayloadValidator interface {
	ValidatePublish(raw []byte) error
	ValidateAnnotation(raw []byte) error
	// ValidateBody validates raw against the JSON request-body schema the served
	// Village API document declares for the operation at method and path. It
	// returns ErrContractBodyUndeclared (wrapped) when the document declares no
	// JSON body for that operation, and ErrSchemaInvalid (wrapped) when the body
	// is not valid JSON or does not satisfy the schema.
	ValidateBody(method, path string, raw []byte) error
}
```

- [ ] **Step 5: Implement the validator**

Create `backend/internal/handler/contract_body.go`:
```go
package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/peasant-labs/schema"
)

// ContractOperation names one operation of the served Village API by method
// and path exactly as the contract spells them. Handlers pass their operation
// explicitly rather than reading a route pattern from the request, because
// the integration suite invokes handlers directly and those requests carry no
// chi route pattern.
type ContractOperation struct {
	Method string
	Path   string
}

func (op ContractOperation) String() string { return op.Method + " " + op.Path }

var (
	opCreateGroup           = ContractOperation{Method: "POST", Path: "/api/v1/groups"}
	opUpdateGroup           = ContractOperation{Method: "PATCH", Path: "/api/v1/groups/{id}"}
	opAddGroupMember        = ContractOperation{Method: "POST", Path: "/api/v1/groups/{id}/members"}
	opUpdateGroupMemberRole = ContractOperation{Method: "PATCH", Path: "/api/v1/groups/{id}/members/{userID}/role"}
	opLinkGroupRepository   = ContractOperation{Method: "POST", Path: "/api/v1/groups/{id}/repositories"}
	opBatchShareProject     = ContractOperation{Method: "POST", Path: "/api/v1/groups/{id}/shares"}
	opBatchReviewShares     = ContractOperation{Method: "PATCH", Path: "/api/v1/groups/{id}/shares"}
	opReviewShare           = ContractOperation{Method: "PATCH", Path: "/api/v1/groups/{id}/shares/{transcriptID}"}
	opShareTranscript       = ContractOperation{Method: "POST", Path: "/api/v1/transcripts/{id}/share"}
)

// ContractEnforcedOperations lists every operation whose request body the
// handlers validate against the served contract. The router drift gate
// asserts each is mounted at exactly this method and path; the handler tests
// assert each has a compiled body schema and a fixture row.
func ContractEnforcedOperations() []ContractOperation {
	return []ContractOperation{
		opCreateGroup,
		opUpdateGroup,
		opAddGroupMember,
		opUpdateGroupMemberRole,
		opLinkGroupRepository,
		opBatchShareProject,
		opBatchReviewShares,
		opReviewShare,
		opShareTranscript,
	}
}

// ErrContractBodyUndeclared reports a lookup for an operation the served
// contract gives no JSON request body. Reaching it from a handler is a wiring
// error, never a client error.
var ErrContractBodyUndeclared = errors.New("the served contract declares no JSON request body for this operation")

// contractSpecURL is the resource name the compiler files the served document
// under. It is not a file URL, so no local path can appear in a violation.
const contractSpecURL = "contract://village-api/openapi.json"

var contractPathParameter = regexp.MustCompile(`\{[^}]*\}`)

// contractOperationKey identifies an operation by method and by path with
// parameter names erased, so a handler's {id} and the contract's {groupId}
// meet.
func contractOperationKey(method, path string) string {
	return strings.ToUpper(method) + " " + contractPathParameter.ReplaceAllString(path, "{}")
}

type contractBodySchemas struct {
	byOperation map[string]*jsonschema.Schema
	err         error
}

var (
	contractBodiesOnce sync.Once
	contractBodies     contractBodySchemas
)

// loadContractBodySchemas parses the served document once and compiles the
// JSON request-body schema of every operation that declares one, through the
// contract module's canonical compiler. Served and enforced are one byte
// source. Multipart bodies (publish) have no JSON schema here.
func loadContractBodySchemas() contractBodySchemas {
	raw := schema.VillageAPISpecJSON()
	var doc struct {
		Paths map[string]map[string]struct {
			RequestBody *struct {
				Content map[string]struct {
					Schema json.RawMessage `json:"schema"`
				} `json:"content"`
			} `json:"requestBody"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return contractBodySchemas{err: fmt.Errorf("parse the served contract: %w", err)}
	}
	compiler := schema.NewJSONSchemaCompiler()
	if err := compiler.AddResource(contractSpecURL, bytes.NewReader(raw)); err != nil {
		return contractBodySchemas{err: fmt.Errorf("register the served contract with the compiler: %w", err)}
	}
	table := map[string]*jsonschema.Schema{}
	for path, operations := range doc.Paths {
		for method, operation := range operations {
			if operation.RequestBody == nil {
				continue
			}
			content, ok := operation.RequestBody.Content["application/json"]
			if !ok || len(content.Schema) == 0 {
				continue
			}
			var ref struct {
				Ref string `json:"$ref"`
			}
			_ = json.Unmarshal(content.Schema, &ref)
			location := ref.Ref
			if location == "" {
				location = "#/paths/" + jsonPointerEscape(path) + "/" + method + "/requestBody/content/application~1json/schema"
			}
			compiled, err := compiler.Compile(contractSpecURL + location)
			if err != nil {
				return contractBodySchemas{err: fmt.Errorf("compile the %s %s request body schema: %w", strings.ToUpper(method), path, err)}
			}
			table[contractOperationKey(method, path)] = compiled
		}
	}
	return contractBodySchemas{byOperation: table}
}

func jsonPointerEscape(segment string) string {
	return strings.ReplaceAll(strings.ReplaceAll(segment, "~", "~0"), "/", "~1")
}

// ValidateBody implements PayloadValidator over the served document's
// request-body schemas.
func (moduleValidator) ValidateBody(method, path string, raw []byte) error {
	contractBodiesOnce.Do(func() { contractBodies = loadContractBodySchemas() })
	if contractBodies.err != nil {
		return contractBodies.err
	}
	compiled, ok := contractBodies.byOperation[contractOperationKey(method, path)]
	if !ok {
		return fmt.Errorf("%w: %s %s", ErrContractBodyUndeclared, strings.ToUpper(method), path)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("%w: the body is not valid JSON", ErrSchemaInvalid)
	}
	if err := compiled.Validate(value); err != nil {
		return fmt.Errorf("%w: %s", ErrSchemaInvalid, renderContractViolation(err))
	}
	return nil
}

// renderContractViolation turns the compiler's error tree into
// "<instance pointer>: <message>" per failing leaf, joined by "; ", with the
// root pointer rendered as "/". Schema locations and resource URLs are left
// out so the text names the client's field, never the server's files.
func renderContractViolation(err error) string {
	var violation *jsonschema.ValidationError
	if !errors.As(err, &violation) {
		return err.Error()
	}
	seen := map[string]bool{}
	var leaves []string
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			where := e.InstanceLocation
			if where == "" {
				where = "/"
			}
			line := where + ": " + e.Message
			if !seen[line] {
				seen[line] = true
				leaves = append(leaves, line)
			}
			return
		}
		for _, cause := range e.Causes {
			walk(cause)
		}
	}
	walk(violation)
	sort.Strings(leaves)
	return strings.Join(leaves, "; ")
}
```

- [ ] **Step 6: Run the validator tests**

Run from `backend/`:
```bash
go test -run 'TestContractBody' -v ./internal/handler/ 2>&1 | grep -E '^(--- |    contract_body_test|ok|FAIL|PASS)' | head -60
```
Expected: all PASS. If a malformed row fails on wording, the output prints the real violation text: update that row's `expect` in the fixture to a substring of it (keep the field pointer) and re-run. If `Compile` fails on the custom URL scheme, change `contractSpecURL` to `"https://contract.invalid/village-api/openapi.json"` (a reserved, never-resolvable host) and add that host to the forbidden-substring list in the test.

- [ ] **Step 7: Run the whole handler unit package**

Run from `backend/`:
```bash
go test -race ./internal/handler/ 2>&1 | tail -3
```
Expected: `ok`.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/handler/contract_body.go backend/internal/handler/openapi.go backend/internal/handler/contract_body_test.go backend/internal/handler/testdata/contract_body_operations.yaml
git commit -m "feat(handler): validate request bodies against the served contract

Compiles each operation's JSON request-body schema from the same bytes the
server serves at /api/v1/openapi.json, through the contract module's
compiler, so served and enforced cannot drift. Violations render as the
client's field pointer and the compiler's message, never a schema location.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_011vyps39XMVMMrQmc6u1FeJ"
```

---

### Task 5: Enforce the nine collectives mutations through the validator

**Files:**
- Modify: `backend/internal/handler/contract_body.go` (add the two request helpers)
- Modify: `backend/internal/handler/groups.go:42`, `:345`, `:474`, `:554`, `:625`, `:788`
- Modify: `backend/internal/handler/collective_repos.go:112`
- Modify: `backend/internal/handler/group_shares.go:213` and `decodeBatchShareRequest`
- Modify: `backend/internal/handler/transcripts.go:1122-1175` (`ShareTranscript`, `shareTranscriptLocked`)
- Modify: `backend/internal/handler/contract_body_test.go` (handler-level tests)
- Modify: `backend/internal/router/contract_drift_test.go` (enforced operations are mounted)

**Interfaces:**
- Consumes: `ContractOperation`, the nine `op*` vars, `ContractEnforcedOperations()`, `payloadValidator()`, `ErrContractBodyUndeclared`, `ErrSchemaInvalid` (Task 4); `newTestHandler`, `mockQuerier`, `memberStub`, `withUserID`, `newRepoHandler`, `newFakeGitHub`, `testGroupID` (existing test helpers).
- Produces: `func (h *Handler) readContractBody(w http.ResponseWriter, r *http.Request, op ContractOperation) ([]byte, bool)` and `func (h *Handler) decodeContractBody(w http.ResponseWriter, r *http.Request, op ContractOperation, dst any) bool`.

- [ ] **Step 1: Write the failing handler-level test**

Append to `backend/internal/handler/contract_body_test.go`. Add imports `"context"`, `"net/http/httptest"`, `"github.com/go-chi/chi/v5"`, `"github.com/google/uuid"`, `"github.com/jackc/pgx/v5/pgtype"`, `"github.com/peasant-labs/village/backend/internal/database/sqlc"`.

```go
// contractBodyTestUser is the signed-in caller every handler-level case uses.
var contractBodyTestUser = uuid.MustParse("7d5c2a10-9b3e-4c8f-a1d2-3e4f5a6b7c8d")

// contractBodyRouter mounts the nine enforced handlers at their production
// patterns under /api/v1 so chi.URLParam works as in production. Every
// database lookup that precedes the body decode is stubbed to succeed as an
// owner; every lookup after the decode is left unstubbed, so the mock panics
// (and the test fails) if a malformed body reaches the database.
func contractBodyRouter(t *testing.T) http.Handler {
	t.Helper()
	q := &mockQuerier{
		getGroupMember: memberStub("owner"),
		getTranscriptByID: func(ctx context.Context, id pgtype.UUID) (sqlc.Transcript, error) {
			return sqlc.Transcript{ID: id, OwnerID: pgtype.UUID{Bytes: contractBodyTestUser, Valid: true}, LocalID: "local"}, nil
		},
	}
	fake := &fakeGitHub{}
	newFakeGitHub(t, fake)
	h := newRepoHandler(t, q, fake)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(withUserID(req.Context(), contractBodyTestUser)))
		})
	})
	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/groups", h.CreateGroup)
		r.Patch("/groups/{id}", h.UpdateGroup)
		r.Post("/groups/{id}/members", h.AddGroupMember)
		r.Patch("/groups/{id}/members/{userID}/role", h.PromoteMember)
		r.Post("/groups/{id}/repositories", h.LinkRepository)
		r.Post("/groups/{id}/shares", h.BatchShareProject)
		r.Patch("/groups/{id}/shares", h.BatchReviewShares)
		r.Patch("/groups/{id}/shares/{transcriptID}", h.ReviewShare)
		r.Post("/transcripts/{id}/share", h.ShareTranscript)
	})
	return r
}

// contractBodyTarget turns a contract path into a concrete request target.
func contractBodyTarget(path string) string {
	replacer := strings.NewReplacer(
		"{id}", testGroupID,
		"{userID}", "5a6b7c8d-9e0f-4a1b-8c2d-3e4f5a6b7c8d",
		"{transcriptID}", "3f0d8b1e-2c4a-4f6e-9a1b-7c2d3e4f5a6b",
	)
	return replacer.Replace(path)
}

func TestContractBody_HandlersAnswer400BeforeTouchingTheDatabase(t *testing.T) {
	router := contractBodyRouter(t)
	for _, c := range loadContractBodyOperations(t) {
		for _, m := range c.Malformed {
			t.Run(c.Name+"/"+m.Name, func(t *testing.T) {
				req := httptest.NewRequest(c.Method, contractBodyTarget(c.Path), strings.NewReader(m.Body))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
				}
				body := rec.Body.String()
				if !strings.Contains(body, "request body failed contract validation: ") {
					t.Fatalf("body lacks the contract prefix: %s", body)
				}
				if !strings.Contains(body, strings.ReplaceAll(m.Expect, `"`, `\"`)) && !strings.Contains(body, m.Expect) {
					t.Fatalf("body %q does not name the violation %q", body, m.Expect)
				}
			})
		}
	}
}

func TestContractBody_HandlersKeepInvalidJSONWording(t *testing.T) {
	router := contractBodyRouter(t)
	for _, c := range loadContractBodyOperations(t) {
		t.Run(c.Name, func(t *testing.T) {
			req := httptest.NewRequest(c.Method, contractBodyTarget(c.Path), strings.NewReader(`{"name":`))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Invalid request body") {
				t.Fatalf("status = %d body = %s; want 400 Invalid request body", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestContractBody_OversizedBodyAnswers413(t *testing.T) {
	router := contractBodyRouter(t)
	huge := `{"name":"` + strings.Repeat("x", maxContractBodyBytes) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/groups", strings.NewReader(huge))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestContractBody_NilValidatorFailsClosed(t *testing.T) {
	orig := payloadValidator
	payloadValidator = func() PayloadValidator { return nil }
	t.Cleanup(func() { payloadValidator = orig })
	router := contractBodyRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/groups", strings.NewReader(`{"name":"x"}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body: %s)", rec.Code, rec.Body.String())
	}
}
```

Check `testGroupID` exists in `collective_repos_test.go` (it does: it is used by `TestLinkRepository_NotConfigured`). If its value is not a valid UUID string, use `"0b6f1c2d-3e4f-4a5b-8c6d-7e8f9a0b1c2d"` in the replacer instead.

- [ ] **Step 2: Run to see it fail**

Run from `backend/`:
```bash
go test -run 'TestContractBody_Handlers|TestContractBody_Oversized|TestContractBody_NilValidator' ./internal/handler/ 2>&1 | head -8
```
Expected: build failure `undefined: maxContractBodyBytes`.

- [ ] **Step 3: Add the request helpers**

Append to `backend/internal/handler/contract_body.go` (add imports `"io"` and `"net/http"`):
```go
// maxContractBodyBytes caps a JSON request body on the enforced operations.
// A batch of transcript ids is a few dozen bytes per id; a megabyte is far
// beyond any real selection and stops a client from streaming an unbounded
// body into the validator.
const maxContractBodyBytes = 1 << 20

// readContractBody reads the JSON body under the size cap and validates it
// against the served contract's schema for op. On any refusal it has already
// written the response and returns false. Invalid JSON keeps the existing
// "Invalid request body" answer; a contract violation answers 400 with the
// rendered violation; an operation the contract gives no body answers 500
// because that is a wiring error, not a client error.
func (h *Handler) readContractBody(w http.ResponseWriter, r *http.Request, op ContractOperation) ([]byte, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxContractBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("request body exceeds the %d byte limit", maxContractBodyBytes))
			return nil, false
		}
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return nil, false
	}
	if !json.Valid(raw) {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return nil, false
	}
	v := payloadValidator()
	if v == nil {
		writeError(w, http.StatusServiceUnavailable, "request validation unavailable")
		return nil, false
	}
	if err := v.ValidateBody(op.Method, op.Path, raw); err != nil {
		if errors.Is(err, ErrContractBodyUndeclared) {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("contract wiring error: the served contract declares no request body for %s", op))
			return nil, false
		}
		writeError(w, http.StatusBadRequest, "request body failed contract validation: "+strings.TrimPrefix(err.Error(), ErrSchemaInvalid.Error()+": "))
		return nil, false
	}
	return raw, true
}

// decodeContractBody is readContractBody followed by a decode into dst.
func (h *Handler) decodeContractBody(w http.ResponseWriter, r *http.Request, op ContractOperation, dst any) bool {
	raw, ok := h.readContractBody(w, r, op)
	if !ok {
		return false
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return false
	}
	return true
}
```

- [ ] **Step 4: Wire the six handlers in groups.go**

At each of the six decode sites, replace the three-line block
```go
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
```
with the one-line call for that handler:

| handler (line) | replacement |
|---|---|
| `CreateGroup` (42) | `if !h.decodeContractBody(w, r, opCreateGroup, &req) { return }` |
| `UpdateGroup` (345) | `if !h.decodeContractBody(w, r, opUpdateGroup, &req) { return }` |
| `AddGroupMember` (474) | `if !h.decodeContractBody(w, r, opAddGroupMember, &req) { return }` |
| `ReviewShare` (554) | `if !h.decodeContractBody(w, r, opReviewShare, &req) { return }` |
| `BatchReviewShares` (625) | `if !h.decodeContractBody(w, r, opBatchReviewShares, &req) { return }` |
| `PromoteMember` (788) | `if !h.decodeContractBody(w, r, opUpdateGroupMemberRole, &req) { return }` |

Write each replacement on three lines (`if ... {`, `return`, `}`) to match gofmt. Leave every check that follows the decode (for example `if req.Name == ""`) in place. Run `go build ./...` afterwards; if it reports `"encoding/json" imported and not used` for `groups.go`, delete that import line.

- [ ] **Step 5: Wire LinkRepository**

In `backend/internal/handler/collective_repos.go` at line 112 replace the decode block with:
```go
	var req linkRepoRequest
	if !h.decodeContractBody(w, r, opLinkGroupRepository, &req) {
		return
	}
```

- [ ] **Step 6: Wire BatchShareProject and move its decoder onto raw bytes**

In `backend/internal/handler/group_shares.go` replace
```go
	req, refusal := decodeBatchShareRequest(r)
	if refusal != "" {
		writeError(w, http.StatusBadRequest, refusal)
		return
	}
```
with
```go
	raw, ok := h.readContractBody(w, r, opBatchShareProject)
	if !ok {
		return
	}
	req, refusal := decodeBatchShareRequest(raw)
	if refusal != "" {
		writeError(w, http.StatusBadRequest, refusal)
		return
	}
```
and change the decoder's signature and first lines to:
```go
func decodeBatchShareRequest(raw []byte) (batchShareRequest, string) {
	var req batchShareRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
```
Add `"bytes"` to the file's imports. Update the decoder's doc comment first sentence to "decodeBatchShareRequest decodes and validates the already contract-checked body." The unknown-field refusal stays: the contract's object is open, and this handler is stricter on purpose.

- [ ] **Step 7: Read the share body before the publish lock**

In `backend/internal/handler/transcripts.go`, change `ShareTranscript` so the body is read and validated after the owner check and before `withPublishLocks`, and passed into the locked function:
```go
	if transcript.OwnerID != user.PgID() {
		writeError(w, http.StatusForbidden, "Not the transcript owner")
		return
	}
	// Read and validate the body before taking the publish lock, so a
	// malformed request never holds the lock.
	raw, ok := h.readContractBody(w, r, opShareTranscript)
	if !ok {
		return
	}
	if err := h.withPublishLocks(r.Context(), user.PgID(), transcript.LocalID, nil, func(conn *pgxpool.Conn) error {
		h.shareTranscriptLocked(w, r, conn, raw)
		return nil
	}); err != nil {
```
Change the locked function's signature to `func (h *Handler) shareTranscriptLocked(w http.ResponseWriter, r *http.Request, conn *pgxpool.Conn, raw []byte)` and replace its decode block with:
```go
	var req struct {
		GroupIDs []string `json:"group_ids"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
```
Search the file for any other caller of `shareTranscriptLocked` (`grep -n shareTranscriptLocked internal/handler/*.go`); there should be exactly the one.

- [ ] **Step 8: Assert the enforced operations are mounted**

Append to `backend/internal/router/contract_drift_test.go` (add import `"github.com/peasant-labs/village/backend/internal/handler"`):
```go
// TestContractEnforcedOperations_AreMounted proves each operation the
// handlers validate bodies for is served at exactly that method and path, so
// an operation constant cannot drift from the router.
func TestContractEnforcedOperations_AreMounted(t *testing.T) {
	mounted := map[string]bool{}
	for _, r := range mountedAPIRoutes(t) {
		mounted[routeKey(r.Method, r.Path)] = true
	}
	for _, op := range handler.ContractEnforcedOperations() {
		if !mounted[routeKey(op.Method, op.Path)] {
			t.Errorf("enforced operation %s is not mounted at that method and path", op)
		}
	}
}
```

- [ ] **Step 9: Build, format, and run the unit suites**

Run from `backend/`:
```bash
gofmt -l . ; go build ./... && go vet ./... && go test -race ./internal/handler/ ./internal/router/ 2>&1 | tail -5
```
Expected: `gofmt` prints nothing; both packages `ok`. If a handler-level case fails because a stubbed lookup is missing, read the panic (`<Method>: not stubbed`) and add that stub to `contractBodyRouter`'s mock, never remove the case.

- [ ] **Step 10: Run the whole backend unit suite**

```bash
go test -race ./... 2>&1 | grep -vE '^(ok|\?)' ; echo "---"; go test -race ./... 2>&1 | grep -c '^ok'
```
Expected: no FAIL lines; the `ok` count matches the package count (13 packages reported `ok` on the baseline).

- [ ] **Step 11: Commit**

```bash
git add backend/internal/handler backend/internal/router
git commit -m "feat(handler): enforce the collectives mutation bodies through the contract

The nine collectives, share, repository, and review mutations read their
body under a size cap, validate it against the served contract, and only
then decode. Schema violations answer 400 with the field and the reason;
invalid JSON keeps its existing answer. The share route reads its body
before taking the publish lock so a malformed request never holds it.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_011vyps39XMVMMrQmc6u1FeJ"
```

---

### Task 6: Frontend collectives types from the published package

**Files:**
- Modify: `frontend/src/lib/types.ts:208-300`, `:326-380`, `:503-640`
- Modify: `frontend/src/lib/review/types.ts`
- Modify: `frontend/src/lib/contribute/types.ts`
- Create: `frontend/src/lib/contractTypes.test.ts`

**Interfaces:**
- Consumes: the root export of `@peasant-labs/schema` 0.20.0 (type names `VillageUserGroup`, `VillageVisibleGroup`, `VillageUserGroupShare`, `VillageGroupTranscriptStats`, `VillageGroupModelBreakdown`, `VillageGroupContributor`, `VillageGroupMember`, `VillageGroupTranscript`, `VillageCollectiveSearchResult`, `VillageCollectiveSearchResponse`, `VillageLinkedRepository`, `VillageLinkedRepositoriesResponse`, `VillageRepositoryCommit`, `VillageRepositoryCommitsResponse`, `VillageContributedCollective`, `VillageTranscriptCollective`, `VillageShareStatus`, `VillageShareEventActor`, `VillageShareEvent`, `VillageCollectiveSubmission`, `VillagePendingShare`, `VillageReviewDecision`, `VillageBatchReviewRequest`, `VillageBatchReviewResponse`, `VillageContributableTranscript`, `VillageContributableResponse`, `VillageBatchShareRequest`, `VillageContributionStatus`, `VillageBatchShareResponse`; runtime exports `zVillage*`).
- Produces: the same exported names the 43 consumers import today, now as aliases.

- [ ] **Step 1: Write the failing guard test**

Create `frontend/src/lib/contractTypes.test.ts`:
```ts
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import * as contract from "@peasant-labs/schema";

/**
 * The frontend's collectives, share, contribution, and repository wire types
 * are aliases of the published contract package's generated types, kept
 * under their historical names for one release. This guard fails when a
 * name on this list is re-declared by hand, or when an alias points at a
 * type the package does not export.
 */
const aliases: Record<string, Record<string, string>> = {
  "./types.ts": {
    Group: "VillageUserGroup",
    VisibleGroup: "VillageVisibleGroup",
    UserGroupShare: "VillageUserGroupShare",
    GroupTranscriptStats: "VillageGroupTranscriptStats",
    GroupModelBreakdown: "VillageGroupModelBreakdown",
    GroupContributor: "VillageGroupContributor",
    GroupMember: "VillageGroupMember",
    GroupTranscript: "VillageGroupTranscript",
    CollectiveSearchResult: "VillageCollectiveSearchResult",
    CollectiveSearchResponse: "VillageCollectiveSearchResponse",
    LinkedRepository: "VillageLinkedRepository",
    LinkedRepositoriesResponse: "VillageLinkedRepositoriesResponse",
    RepositoryCommit: "VillageRepositoryCommit",
    RepositoryCommitsResponse: "VillageRepositoryCommitsResponse",
    ContributedCollective: "VillageContributedCollective",
    TranscriptCollective: "VillageTranscriptCollective",
    ShareEventStatus: "VillageShareStatus",
    ShareEventActor: "VillageShareEventActor",
    ShareEvent: "VillageShareEvent",
    CollectiveSubmissionPair: "VillageCollectiveSubmission",
  },
  "./review/types.ts": {
    PendingShare: "VillagePendingShare",
    ReviewDecision: "VillageReviewDecision",
    BatchReviewRequest: "VillageBatchReviewRequest",
    BatchReviewResponse: "VillageBatchReviewResponse",
  },
  "./contribute/types.ts": {
    ContributableTranscript: "VillageContributableTranscript",
    ContributableResponse: "VillageContributableResponse",
    BatchShareRequest: "VillageBatchShareRequest",
    BatchShareStatus: "VillageContributionStatus",
    BatchShareResponse: "VillageBatchShareResponse",
  },
};

/**
 * Hand-written interfaces that share a name with a contract type but belong
 * to routes outside the collectives surface (transcript and profile reads).
 * Deriving them is separate work. Each entry must still be declared by hand
 * and still exported by the package, so this list can only shrink.
 */
const knownHandWrittenOutsideScope: Record<string, string[]> = {
  "./types.ts": ["User", "Transcript", "TranscriptListResponse", "Tag"],
  "./review/types.ts": [],
  "./contribute/types.ts": [],
};

function source(relative: string): string {
  return readFileSync(fileURLToPath(new URL(relative, import.meta.url)), "utf8");
}

describe("collectives wire types come from the contract package", () => {
  for (const [file, mapping] of Object.entries(aliases)) {
    describe(file, () => {
      const text = source(file);
      for (const [local, generated] of Object.entries(mapping)) {
        it(`${local} is an alias of ${generated}`, () => {
          const alias = new RegExp(`^export type ${local} = ${generated};$`, "m");
          expect(text, `${file} must declare "export type ${local} = ${generated};"`).toMatch(alias);
          const redeclared = new RegExp(`^export (interface|type) ${local}\\b(?! = ${generated};)`, "m");
          expect(text, `${file} re-declares ${local} by hand`).not.toMatch(redeclared);
        });
        it(`${generated} exists in the package`, () => {
          const runtimeName = `z${generated}`;
          expect((contract as Record<string, unknown>)[runtimeName], `${runtimeName} is not exported by @peasant-labs/schema`).toBeDefined();
        });
      }
      it("declares no interface whose name the package already exports as a Village type", () => {
        const exported = new Set(
          Object.keys(contract)
            .filter((name) => /^zVillage[A-Z]/.test(name))
            .map((name) => name.slice("zVillage".length)),
        );
        const declared = [...text.matchAll(/^export interface ([A-Za-z]+)\b/gm)].map((m) => m[1]);
        const allowed = new Set(knownHandWrittenOutsideScope[file] ?? []);
        const duplicates = declared.filter((name) => exported.has(name) && !allowed.has(name));
        expect(duplicates, `hand-written duplicates of contract types: ${duplicates.join(", ")}`).toEqual([]);
        for (const name of allowed) {
          expect(declared, `${name} is listed as a known hand-written duplicate but is no longer declared in ${file}; delete it from the list`).toContain(name);
          expect(exported.has(name), `${name} is listed as a known hand-written duplicate but the package no longer exports Village${name}; delete it from the list`).toBe(true);
        }
      });
    });
  }
});
```

- [ ] **Step 2: Run it to see it fail**

Run from `frontend/`:
```bash
source ~/.nvm/nvm.sh && nvm use default >/dev/null && NODE_OPTIONS=--no-experimental-webstorage pnpm exec vitest run src/lib/contractTypes.test.ts 2>&1 | tail -15
```
Expected: many failures of the form `must declare "export type Group = VillageUserGroup;"`. If the "known hand-written duplicate" assertions also fail because a listed name is not declared or not exported, fix the `knownHandWrittenOutsideScope` list to what the two sources actually say; the list is a manifest of the current state, not a wish.

- [ ] **Step 3: Alias the types in `types.ts`**

Add at the top of `frontend/src/lib/types.ts`, next to the existing import:
```ts
import type {
  VillageCollectiveSearchResponse,
  VillageCollectiveSearchResult,
  VillageCollectiveSubmission,
  VillageContributedCollective,
  VillageGroupContributor,
  VillageGroupMember,
  VillageGroupModelBreakdown,
  VillageGroupTranscript,
  VillageGroupTranscriptStats,
  VillageLinkedRepositoriesResponse,
  VillageLinkedRepository,
  VillageRepositoryCommit,
  VillageRepositoryCommitsResponse,
  VillageShareEvent,
  VillageShareEventActor,
  VillageShareStatus,
  VillageTranscriptCollective,
  VillageUserGroup,
  VillageUserGroupShare,
  VillageVisibleGroup,
} from "@peasant-labs/schema";
```

Then replace each hand-written declaration with its alias. Keep any doc comment that explains a field's meaning above the alias; delete field-level comments that now live in the contract. The deprecation note is one line per alias, pointing at the package name:

```ts
/** @deprecated Import VillageUserGroup from "@peasant-labs/schema". Alias kept for one release. */
export type Group = VillageUserGroup;

/** @deprecated Import VillageVisibleGroup from "@peasant-labs/schema". Alias kept for one release. */
export type VisibleGroup = VillageVisibleGroup;

/** @deprecated Import VillageUserGroupShare from "@peasant-labs/schema". Alias kept for one release. */
export type UserGroupShare = VillageUserGroupShare;

/** @deprecated Import VillageGroupTranscriptStats from "@peasant-labs/schema". Alias kept for one release. */
export type GroupTranscriptStats = VillageGroupTranscriptStats;

/** @deprecated Import VillageGroupModelBreakdown from "@peasant-labs/schema". Alias kept for one release. */
export type GroupModelBreakdown = VillageGroupModelBreakdown;

/** @deprecated Import VillageGroupContributor from "@peasant-labs/schema". Alias kept for one release. */
export type GroupContributor = VillageGroupContributor;

/** @deprecated Import VillageGroupMember from "@peasant-labs/schema". Alias kept for one release. */
export type GroupMember = VillageGroupMember;

/** @deprecated Import VillageGroupTranscript from "@peasant-labs/schema". Alias kept for one release. */
export type GroupTranscript = VillageGroupTranscript;

/** @deprecated Import VillageCollectiveSearchResult from "@peasant-labs/schema". Alias kept for one release. */
export type CollectiveSearchResult = VillageCollectiveSearchResult;

/** @deprecated Import VillageCollectiveSearchResponse from "@peasant-labs/schema". Alias kept for one release. */
export type CollectiveSearchResponse = VillageCollectiveSearchResponse;

/** @deprecated Import VillageLinkedRepository from "@peasant-labs/schema". Alias kept for one release. */
export type LinkedRepository = VillageLinkedRepository;

/** @deprecated Import VillageLinkedRepositoriesResponse from "@peasant-labs/schema". Alias kept for one release. */
export type LinkedRepositoriesResponse = VillageLinkedRepositoriesResponse;

/** @deprecated Import VillageRepositoryCommit from "@peasant-labs/schema". Alias kept for one release. */
export type RepositoryCommit = VillageRepositoryCommit;

/** @deprecated Import VillageRepositoryCommitsResponse from "@peasant-labs/schema". Alias kept for one release. */
export type RepositoryCommitsResponse = VillageRepositoryCommitsResponse;

/** @deprecated Import VillageContributedCollective from "@peasant-labs/schema". Alias kept for one release. */
export type ContributedCollective = VillageContributedCollective;

/** @deprecated Import VillageTranscriptCollective from "@peasant-labs/schema". Alias kept for one release. */
export type TranscriptCollective = VillageTranscriptCollective;

/** @deprecated Import VillageShareStatus from "@peasant-labs/schema". Alias kept for one release. */
export type ShareEventStatus = VillageShareStatus;

/** @deprecated Import VillageShareEventActor from "@peasant-labs/schema". Alias kept for one release. */
export type ShareEventActor = VillageShareEventActor;

/** @deprecated Import VillageShareEvent from "@peasant-labs/schema". Alias kept for one release. */
export type ShareEvent = VillageShareEvent;

/** @deprecated Import VillageCollectiveSubmission from "@peasant-labs/schema". Alias kept for one release. */
export type CollectiveSubmissionPair = VillageCollectiveSubmission;
```

Keep `assertShareEventStatusExhaustive` and `assertShareEventActorExhaustive` exactly as they are. `ProjectCollectiveRollupEntry`, `UserOrg`, `TranscriptCommit`, `Attestation`, and the transcript types stay hand-written: their routes are in the undocumented-routes manifest.

- [ ] **Step 4: Alias `review/types.ts` and `contribute/types.ts`**

Replace the body of `frontend/src/lib/review/types.ts` (keep its top-of-file comment) with:
```ts
import type {
  VillageBatchReviewRequest,
  VillageBatchReviewResponse,
  VillagePendingShare,
  VillageReviewDecision,
} from "@peasant-labs/schema";

/** @deprecated Import VillagePendingShare from "@peasant-labs/schema". Alias kept for one release. */
export type PendingShare = VillagePendingShare;

/** @deprecated Import VillageReviewDecision from "@peasant-labs/schema". Alias kept for one release. */
export type ReviewDecision = VillageReviewDecision;

/** @deprecated Import VillageBatchReviewRequest from "@peasant-labs/schema". Alias kept for one release. */
export type BatchReviewRequest = VillageBatchReviewRequest;

/** @deprecated Import VillageBatchReviewResponse from "@peasant-labs/schema". Alias kept for one release. */
export type BatchReviewResponse = VillageBatchReviewResponse;
```

Replace the body of `frontend/src/lib/contribute/types.ts` (keep its top-of-file comment; move the single-owner warning that sits on `ContributableTranscript` into a comment above the alias, since `./tree.ts` still relies on it) with:
```ts
import type {
  VillageBatchShareRequest,
  VillageBatchShareResponse,
  VillageContributableResponse,
  VillageContributableTranscript,
  VillageContributionStatus,
} from "@peasant-labs/schema";

/**
 * Rows on the contribute page are the caller's own transcripts only, and the
 * tree relies on that: it folds a started session under its starter by
 * session id alone, which is safe for one owner and WRONG for several,
 * because a session id is unique per owner rather than globally. If the
 * endpoint is ever widened to answer with more than one person's rows, read
 * the owner in `./tree.ts` (see `SINGLE_OWNER_ENDPOINT` there) in the same
 * change.
 *
 * @deprecated Import VillageContributableTranscript from "@peasant-labs/schema". Alias kept for one release.
 */
export type ContributableTranscript = VillageContributableTranscript;

/** @deprecated Import VillageContributableResponse from "@peasant-labs/schema". Alias kept for one release. */
export type ContributableResponse = VillageContributableResponse;

/** @deprecated Import VillageBatchShareRequest from "@peasant-labs/schema". Alias kept for one release. */
export type BatchShareRequest = VillageBatchShareRequest;

/** @deprecated Import VillageContributionStatus from "@peasant-labs/schema". Alias kept for one release. */
export type BatchShareStatus = VillageContributionStatus;

/** @deprecated Import VillageBatchShareResponse from "@peasant-labs/schema". Alias kept for one release. */
export type BatchShareResponse = VillageBatchShareResponse;
```
Delete the now-unused `NameSource` and `SessionOrigin` imports from that file if nothing else in it uses them.

- [ ] **Step 5: Typecheck and apply the divergence policy**

Run from `frontend/`:
```bash
source ~/.nvm/nvm.sh && nvm use default >/dev/null && pnpm exec tsc --noEmit 2>&1 | head -40
```
For each error, decide per the spec:
- A consumer reads a field the backend serves but the contract lacks: keep a documented local extension in the aliasing file, for example `export type Group = VillageUserGroup & { member_count?: number };`, with a one-line comment naming what the contract is missing, and record it for the schema follow-up issue in the PR body.
- A consumer reads a field the backend never serves: fix the consumer.
- A consumer assigns a plain `string` where the contract has an enum (for example a settings form's `acceptance_mode` state): narrow the consumer's state to the enum type imported from `@peasant-labs/schema` (`VillageGroupAcceptanceMode`), or validate with the package's `isVillage...` predicate where one exists. Do not cast with `as`.
- A consumer handles `null` where the contract says the field is always present: leave it; over-defensive code is not an error.
Re-run until clean. The guard test's alias regexes accept only the exact `export type X = VillageY;` line, so if an extension is needed, also update that entry in `contractTypes.test.ts` to the extended form (the test is the manifest of what was decided).

- [ ] **Step 6: Run the guard, the suite, lint, and the build**

Run from `frontend/`:
```bash
source ~/.nvm/nvm.sh && nvm use default >/dev/null && NODE_OPTIONS=--no-experimental-webstorage pnpm exec vitest run src/lib/contractTypes.test.ts 2>&1 | tail -5 && pnpm test 2>&1 | tail -8 && pnpm lint 2>&1 | tail -3 && pnpm build 2>&1 | tail -3
```
Expected: guard passes; `Test Files 36 passed`, `Tests` count at least 331 plus the new guard cases; four `mutation killed` lines; lint clean; build succeeds.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/lib/types.ts frontend/src/lib/review/types.ts frontend/src/lib/contribute/types.ts frontend/src/lib/contractTypes.test.ts
git commit -m "refactor(frontend): derive the collectives wire types from the contract package

The collectives, share, contribution, and repository types become aliases
of the generated types in @peasant-labs/schema under their existing names,
kept for one release, so no consumer changes. A guard fails when one of
them is re-declared by hand.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_011vyps39XMVMMrQmc6u1FeJ"
```

---

### Task 7: Documentation, spec amendment, final verification, pull request

**Files:**
- Modify: `AGENTS.md` (section "Shared wire contract and licensing")
- Modify: `TESTING.md` (layer table)
- Modify: `docs/superpowers/specs/2026-09-12-collectives-contract-enforcement-design.md`

- [ ] **Step 1: Document the gate and the body path in `AGENTS.md`**

After the paragraph ending "do not add an undocumented handler-only validation rule." add:
```markdown
Two tests keep the served document honest. The route drift gate
(`backend/internal/router/contract_drift_test.go`) fails when a mounted
`/api/v1` route is neither declared by the served document nor listed, with a
reason, in `backend/internal/router/testdata/undocumented_routes.yaml`; a row
there may only be removed once the contract declares the route or the route
is unmounted, so the list only shrinks. The collectives, share, repository,
and review mutations read their bodies through `readContractBody` /
`decodeContractBody` (`backend/internal/handler/contract_body.go`), which
validate against the served document's request-body schema before decoding;
a new operation with a JSON body adds its `ContractOperation` there and a row
in `backend/internal/handler/testdata/contract_body_operations.yaml`.
```

- [ ] **Step 2: Add the rows to `TESTING.md`**

In the layer table add, after the "Cross-repo contract" row:
```markdown
| Route drift gate (mounted routes ⊆ served contract ∪ manifest) | none | no | unit | `backend/internal/router/contract_drift_test.go`; manifest `testdata/undocumented_routes.yaml` |
| Contract request-body enforcement (collectives mutations) | none | no | unit | `backend/internal/handler/contract_body_test.go`; fixture `testdata/contract_body_operations.yaml` |
```

- [ ] **Step 3: Amend the spec for what planning changed**

In the spec's section 3, replace the sentence about taking the pattern from chi with:
```markdown
Handlers name their operation explicitly (`opCreateGroup`, ...). The route pattern chi knows
was the first design, but the integration suite invokes handlers directly, so those requests
carry no pattern and every such test would have answered 500. A router test asserts every
enforced operation is mounted at exactly its method and path.
```
Replace the helper signature line with the two helpers `readContractBody(w, r, op)` and `decodeContractBody(w, r, op, dst)`, and add under the handler table: "The share route reads and validates its body before taking the publish lock, so a malformed request never holds the lock; the locked function receives the raw bytes." In section 4, add: "The group, member, role, and repository mutation inputs in the query modules are inline parameter types, not named wire types, and stay as they are; the batch share and batch review inputs already use the named request types, which now alias the contract." In the Testing section, replace "before any database call" with "before the handler decodes; lookups that precede the decode are stubbed, and any lookup after it is unstubbed so the mock panics if a malformed body gets through".

- [ ] **Step 4: Full verification**

Run from `backend/`:
```bash
gofmt -l . ; go vet ./... && go test -race ./... 2>&1 | grep -vE '^(ok|\?)'; echo "backend done"
```
Run from `frontend/`:
```bash
source ~/.nvm/nvm.sh && nvm use default >/dev/null && pnpm exec tsc --noEmit && pnpm test 2>&1 | tail -6 && pnpm lint 2>&1 | tail -2 && pnpm build 2>&1 | tail -2
```
Expected: nothing from gofmt, no backend FAIL lines, frontend clean. Then from the worktree root: `git status --short` shows only the three doc files modified.

- [ ] **Step 5: Scrub for taxonomy tokens**

```bash
git diff origin/develop --name-only | xargs grep -nE '#99|#106|#96|slice|epoch|SLICE-|PROPOSAL-' 2>/dev/null | grep -v 'docs/superpowers/' || echo "clean"
```
Expected: `clean` (the spec and plan under `docs/superpowers/` may reference issues; shipped code and docs may not).

- [ ] **Step 6: Commit and push**

```bash
git add AGENTS.md TESTING.md docs/superpowers/specs/2026-09-12-collectives-contract-enforcement-design.md
git commit -m "docs: record the route drift gate and the contract body path

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_011vyps39XMVMMrQmc6u1FeJ"
git push -u origin village-99--feat--contract-enforcement-and-drift-gate
```

- [ ] **Step 7: Open the pull request**

```bash
gh pr create -R peasant-labs/village --base develop --title "feat: enforce the collectives contract and gate mounted routes against the served spec" --body-file /dev/stdin <<'EOF'
Closes #99. Completes the village half of peasant-labs/schema#96.

## What changes

- **Pins.** Backend and frontend move to schema v0.20.0 (Village API 0.18.0). The consumer-side version guard moves with it.
- **Route drift gate.** A router test walks the production router and fails when a mounted `/api/v1` route is neither declared by the served document nor listed, with a reason, in a manifest of routes the contract does not describe yet. The manifest carries 38 rows today (auth, orgs, tags, attestations, annotation and commit lists, profiles, project pages, batch publish, the spec route itself) and can only shrink: a row for a route the contract now declares, or for a route no longer mounted, fails the gate. Declaring those routes is a schema-repository follow-up whose completion check is this manifest reaching zero rows.
- **Contract body enforcement.** The nine collectives, share, repository, and review mutations read their body under a 1 MiB cap and validate it against the request-body schema compiled from the same bytes the server serves at `/api/v1/openapi.json`, through the contract module's compiler, before decoding. Violations answer 400 as `request body failed contract validation: <field>: <reason>`; invalid JSON keeps `Invalid request body`. The share route now reads its body before taking the publish lock.
- **Frontend types.** The collectives, share, contribution, and repository types are aliases of the generated types in `@peasant-labs/schema` under their existing names, kept one release; a vitest guard fails if any is re-declared by hand.

## Proven red

Mounting an undeclared probe route made the gate report `GET /api/v1/drift-probe is mounted but the served contract does not declare it` and fail; the comparison is also fixture-tested with a mounted-but-undeclared case so that proof is permanent.

## Verification

- Backend: `gofmt`, `go vet`, `go test -race ./...` (unit) green locally.
- Frontend: `tsc --noEmit`, `pnpm test` (vitest plus the mutation guards), `pnpm lint`, `pnpm build` green locally.
- The real-Postgres integration suite could not run locally (Docker is not running on the development machine); it runs in this PR's CI. The existing collectives, share, batch-share, and batch-review fixtures exercise the new decode path.

No UI change; both-theme screenshots are not required.

🤖 Generated with [Claude Code](https://claude.com/claude-code)

https://claude.ai/code/session_011vyps39XMVMMrQmc6u1FeJ
EOF
```

- [ ] **Step 8: Watch CI**

```bash
gh pr checks -R peasant-labs/village --watch
```
If the integration job fails, read the failing test's output with `gh run view <id> --log-failed -R peasant-labs/village`, fix on the branch, and push. Do not merge; the user reviews first.

- [ ] **Step 9: Open the schema follow-up for the undeclared routes**

The user ratified this as a parallel follow-up. Open it against the schema repository so the manifest has an owner:

```bash
gh issue create -R peasant-labs/schema --title "Declare the remaining Village routes the server mounts but the contract does not describe" --body-file /dev/stdin <<'EOF'
## Problem

Village's route drift gate (`backend/internal/router/contract_drift_test.go`, landing with peasant-labs/village#99) accepts a mounted `/api/v1` route only when the served Village API document declares it or `backend/internal/router/testdata/undocumented_routes.yaml` names it. That manifest carries 38 routes today: the OAuth entry points and callbacks for five providers, logout, the signed-in account read/delete/settings/username routes, API keys, organisations and their visibility, organisation search and pages, tags, transcript deletion, attestations, the annotation and commit list reads, manual annotation creation, public profiles and project pages, project display-name override and reset, batch publish, and the spec route itself.

## Scope

Declare those routes in `openapi/village.go` (or a sibling file) with request and response DTOs in the Types catalog, bump `VillageAPIVersion` and `TypesVersion`, regenerate, and tag. Village then re-pins and deletes the manifest rows; the gate fails on any row the contract now declares, so the manifest reaching zero rows is the completion check.

Three decisions come with the declarations and should be made here rather than assumed:

1. `PATCH /api/v1/auth/me/settings` (discoverability) sits beside the contract's `PATCH /api/v1/users/me/settings` (preview flag). Declaring both enshrines a duplicate; folding the old flag into the new route and retiring the old one is the likely answer.
2. `POST /api/v1/transcripts/publish/batch` answers 501 "coming soon" and has never shipped. Declaring a stub is wrong; deleting the route is the honest move.
3. Most responses are raw sqlc row types. One field is untyped, nullability is whatever Postgres says, and the commits response uses camelCase while every other Village DTO is snake_case. Declaring freezes those shapes in an immutable versioned spec; harmonising changes the wire for the frontend.

Calibration: the collectives contract (28 routes) was about 2,300 hand-written lines plus generated artifacts. This surface needs roughly 28 declarations, 38 per-operation fixture rows, and about 21 new DTOs; six response shapes already exist in the catalog.

## Related

- peasant-labs/village#99 (the gate and the manifest), peasant-labs/schema#96 (the collectives contract epic this completes the pattern of).
EOF
```
