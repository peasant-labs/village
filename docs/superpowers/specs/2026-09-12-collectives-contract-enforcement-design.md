# Serve, enforce, and generate the collectives contract; add the route drift gate

Issue: peasant-labs/village#99 (the village child of schema epic peasant-labs/schema#96).
Branch: `village-99--feat--contract-enforcement-and-drift-gate`, off `origin/develop` at `82f0d92`.

## Goal

Village already serves the Village API document from the schema module. This change makes the
served document the enforced one for the collectives surface, derives the frontend's collectives
types from the same contract, and adds a gate that fails CI when a mounted `/api/v1` route is
absent from the served document. It also moves both schema pins to v0.20.0 so the pull request
attachment work (#106) is a pure route-mounting change.

## Non-goals

- Declaring the 38 mounted routes the spec has never described (auth, orgs, tags, attestations,
  annotation and commit lists, public profiles, project pages, batch publish, the spec route
  itself). That is a schema-repository change tracked separately; this change records those
  routes in a manifest the gate reads.
- Enforcing bodies outside the nine collectives, share, repository, and review mutations. The
  publish and annotation validators keep their existing paths; the owner transcript PATCH keeps
  its own validation path.
- Mounting the seven pull request attachment routes. That is #106.
- Any UI change. Both-theme screenshots are not required for this pull request, and the pull
  request body says so.

## Established facts

- The schema half of epic 96 shipped in schema v0.1.3. Village has since re-pinned to v0.18.0
  (Village API 0.16.0). The latest schema tag is v0.20.0 (Village API 0.18.0), which carries the
  per-commit change counts the digest work needs.
- Diffing the router against spec 0.18.0: all 28 collectives routes are declared; 38 other mounted
  routes are not; 8 declared routes are not mounted (the seven attachment routes and the
  transcript-group members route).
- `schema.NewJSONSchemaCompiler()` exists from schema v0.19.0. Compiling a request-body schema
  straight from `schema.VillageAPISpecJSON()` by JSON pointer works and yields actionable messages
  (`missing properties: 'name'`, `value must be one of "contributor", "member"`).
- `chi.RouteContext(r.Context()).RoutePattern()` returns the full mounted pattern
  (`/api/v1/groups/{id}/shares/{transcriptID}`), so handlers never hardcode a spec path.
- The npm root export of `@peasant-labs/schema` carries every Village DTO and enum by name
  (`VillageGroup`, `VillageShareEvent`, `VillageShareStatus`, ...).
- On v0.20.0 the backend unit suite passes except the consumer-side version guard, which
  correctly reports the move from 0.16.0 to 0.18.0. The frontend typecheck and test suite pass.
- Docker is not running on the development machine, so the real-Postgres integration suite is
  verified by CI on the pull request, not locally.

## Design

### 1. Pins

- `backend/go.mod`: `github.com/peasant-labs/schema v0.20.0` (plus `go.sum`).
- `frontend/package.json`: `@peasant-labs/schema` `0.20.0` (plus `pnpm-lock.yaml`).
- `backend/internal/handler/openapi_test.go`: `wantVillageAPIVersion = "0.18.0"`.
- `go.work.sum` is not committed; prior re-pins did not touch it.

### 2. Route drift gate

Location: `backend/internal/router/contract_drift_test.go` plus fixtures under
`backend/internal/router/testdata/`. Test-only code; nothing ships in the binary.

Mechanics:

1. Build the real router with `router.New(&config.Config{}, nil, nil, titles)`. Construction does
   not touch the database (the GitHub client logs "not configured" and stays nil).
2. Walk it with `chi.Walk` and collect every `(method, pattern)` under `/api/v1`.
3. Parse `schema.VillageAPISpecJSON()` and collect every `(method, path)` under `paths`.
4. Load the manifest `testdata/undocumented_routes.yaml`: rows of `method`, `path`, `reason`.
5. Compare with a pure function `contractDriftFindings(mounted, declared, manifest) []string`.
   Path parameters are compared by position, not name, so `{id}` and `{groupId}` at the same
   position are the same route.

The gate fails on any of:

- a mounted route that is neither declared nor in the manifest (the drift #99 exists to catch);
- a manifest row that is no longer mounted (stale row);
- a manifest row that the spec now declares (the row must be deleted, so the manifest only
  shrinks);
- a duplicate manifest row.

Declared-but-unmounted routes are logged for information and do not fail the gate; that
direction is #106's fixture.

The manifest needs no separate required-name list: removing a row for a route that is still
mounted and still undeclared makes the gate fail, which is the protection.

The comparison function is fixture-tested from `testdata/contract_drift_cases.yaml`, one named
case per failure condition plus the passing cases and the parameter-name case. The test carries a
required-name list of those cases. This is the permanent form of the issue's "proven red"
acceptance line.

### 3. Contract body enforcement

Location: `backend/internal/handler/contract_body.go` (new), `openapi.go` (interface change),
and the nine handlers.

`PayloadValidator` gains:

```go
ValidateBody(method, routePattern string, raw []byte) error
```

`moduleValidator` implements it over a lazily built, process-wide table:

- On first use, parse `schema.VillageAPISpecJSON()` once, add it to
  `schema.NewJSONSchemaCompiler()` under a non-file URL (so no local path can appear in a
  message), and for every operation with an `application/json` request body compile the schema at
  its JSON-pointer location (`#/paths/<escaped path>/<method>/requestBody/content/
  application~1json/schema`). Compiling by location handles both `$ref` and inline schemas.
  Multipart bodies (publish) are skipped.
- Key the table by `METHOD path` exactly as chi reports the pattern.
- A lookup miss returns `ErrContractBodyUndeclared`. A validation failure returns
  `ErrSchemaInvalid` wrapped around a rendered violation.

Violation rendering: walk the compiler's error tree to its leaves and emit
`<instance pointer>: <message>` entries joined by `; `, where the root pointer renders as `/`.
No schema URLs, file paths, or absolute locations appear in the text.

Handler helper:

```go
func (h *Handler) decodeContractBody(w http.ResponseWriter, r *http.Request, dst any) bool
```

1. Read the body through `http.MaxBytesReader` with a 1 MiB cap. Over the cap answers 413; any
   other read error answers 400 `Invalid request body`.
2. Take the pattern from `chi.RouteContext(r.Context()).RoutePattern()`.
3. Call `payloadValidator().ValidateBody(r.Method, pattern, raw)`.
   - `ErrContractBodyUndeclared` (or an empty pattern) answers 500 with a wiring message naming
     the method and pattern. This is a programming error, never a user error, and the enforced-
     operations fixture below catches it in tests.
   - Any other error answers **400** with `request body failed contract validation: <rendered>`.
     The contract declares 400, not 422, for these operations, and the handlers answer 400 for
     bad input today. Publish and annotation keep their 422.
4. `json.Unmarshal(raw, dst)`; a failure answers 400 `Invalid request body`.

The nine handlers replace `json.NewDecoder(r.Body).Decode(&req)` with the helper:

| method | pattern | handler |
|---|---|---|
| POST | `/api/v1/groups` | `CreateGroup` |
| PATCH | `/api/v1/groups/{id}` | `UpdateGroup` |
| POST | `/api/v1/groups/{id}/members` | `AddGroupMember` |
| PATCH | `/api/v1/groups/{id}/members/{userID}/role` | `PromoteMember` |
| POST | `/api/v1/groups/{id}/repositories` | `LinkRepository` |
| POST | `/api/v1/groups/{id}/shares` | `BatchShareProject` |
| PATCH | `/api/v1/groups/{id}/shares` | `BatchReviewShares` |
| PATCH | `/api/v1/groups/{id}/shares/{transcriptID}` | `ReviewShare` |
| POST | `/api/v1/transcripts/{id}/share` | `ShareTranscript` |

Existing handler-level checks (for example "Name is required") stay in place. They become
unreachable for shape errors the contract already rejects, and they still guard semantics the
contract does not express. Nothing user-facing is deleted.

The served document and the enforced schemas are the same bytes from the same module, so this
adds no handler-only validation rule, in keeping with `AGENTS.md`.

### 4. Frontend types from the package

Location: `frontend/src/lib/types.ts` and wherever the collectives request bodies are typed
(`frontend/src/lib/api.ts` or the query modules).

Every hand-written type on the collectives, share, contribution, and repository surface whose
shape the contract declares becomes an alias to the generated type under its existing name:

```ts
/** @deprecated Import VillageGroup from "@peasant-labs/schema". Alias kept for one release. */
export type Group = VillageGroup;
```

The expected mapping (the typecheck is the oracle; names are confirmed during implementation):
`Group`, `VisibleGroup`, `UserGroupShare`, `GroupTranscriptStats`, `GroupModelBreakdown`,
`GroupContributor`, `GroupMember`, `GroupTranscript`, `CollectiveSearchResult`,
`CollectiveSearchResponse`, `LinkedRepository`, `RepositoryCommit`, `RepositoryCommitsResponse`,
`ContributedCollective`, `TranscriptCollective`, `ShareEventStatus`, `ShareEventActor`,
`ShareEvent`, `CollectiveSubmissionPair`, and the request bodies for the nine mutations.
`ProjectCollectiveRollupEntry` belongs to an undeclared route and stays hand-written.

The 43 files that import from `@/lib/types` do not change.

Divergence policy when the typecheck disagrees with the alias:

- The frontend reads a field the backend serves but the contract lacks: contract bug. Keep a
  documented local extension (`VillageX & { field }`) with a comment naming the schema issue
  filed for it.
- The frontend declares a field the backend never serves: frontend bug. Drop it.
- Optional versus nullable mismatches: adapt the consumer.

A small vitest guard reads `types.ts` and the package's exported `Village*` names and fails if
any `export interface` re-declares a shape the package exports. That keeps "no hand-written
duplicate" true after this change.

### 5. Documentation

- `AGENTS.md`, "Shared wire contract and licensing": three lines naming the drift gate, the
  manifest rule (a row may only be removed when the route is declared or unmounted), and the
  contract body path the collectives mutations use.
- `TESTING.md`: rows for the drift gate and the body-validation fixtures.

## Testing

Every case lives in a fixture, and the test code carries a required-name list for each fixture
except the route manifest, where the gate itself is the deletion protection.

Backend, unit (no database):

- `router/testdata/undocumented_routes.yaml`: the 38 routes. The gate is the protection.
- `router/testdata/contract_drift_cases.yaml`: the comparison function's cases.
- `handler/testdata/contract_body_operations.yaml`: one row per enforced operation with at least
  one malformed body naming the expected violation text and one conforming body. Two tests read
  it: the validator alone (malformed rejects, conforming passes, every row compiles), and each
  handler mounted on a bare chi router at its production pattern, with a nil pool and a user
  injected into the request context by a test middleware (each malformed body answers 400 with
  the prefix before any database call; a nil-pool panic would fail the test loudly).
- The existing `openapi_test.go` guard on the pinned version, bumped.

Backend, integration (CI only, real Postgres): the existing collectives, share, batch-share, and
batch-review fixtures exercise the new decode path unchanged. No new integration tests.

Frontend: `tsc --noEmit`, `pnpm test` (vitest plus the mutation guards), `pnpm lint`,
`pnpm build`, and the new duplicate-type guard.

## Deliverables

One pull request on this branch, `Closes #99`, stating that no UI changed and screenshots are not
required, and that the integration suite ran in CI. After merge: sync `develop`, build, remove the
worktree, delete the remote branch; comment on schema epic 96 that its last child landed; open the
schema issue for declaring the 38 manifest routes, naming the manifest reaching zero rows as its
completion check.

## Decisions ratified with the user (2026-09-12)

- Manifest now; declaring the 38 routes is a parallel schema follow-up, not a prerequisite.
- Schema-invalid bodies on the collectives routes answer 400, not 422.
- The frontend shim is alias-in-place under the existing names.
- Both pins move to v0.20.0 in this change rather than in #106.
