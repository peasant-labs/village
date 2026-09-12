# Serve, enforce, and generate the collectives contract; add the route drift gate

Design record for the change that closes peasant-labs/village#99, the village half of
peasant-labs/schema#96. The permanent operating documentation lives in `AGENTS.md` ("Shared wire
contract and licensing") and `TESTING.md`; this file records the decisions and why.

## Goal

Village serves the Village API document from the `github.com/peasant-labs/schema` module. This
change makes the served document the enforced one for the collectives mutations, derives the
frontend's collectives types from the same contract, and adds a gate that fails when a mounted
`/api/v1` route is absent from the served document. Both schema pins move to v0.20.0.

## Route drift gate

`backend/internal/router/contract_drift_test.go` builds the production router with nil
dependencies, walks every route at or below `/api/v1`, and compares it with the served document
and with `testdata/undocumented_routes.yaml`, the manifest of routes the contract does not
describe yet (38 rows; tracked in peasant-labs/schema#120, done at zero rows). Path parameters
compare by position, not name. The gate fails on a mounted route that is neither declared nor
listed, on a manifest row that is no longer mounted, on a row the contract now declares, and on a
duplicate row. Declared-but-unmounted routes are informational. A second test asserts every
enforced operation below is mounted at exactly its method and path.

## Contract body enforcement

`backend/internal/handler/contract_body.go` compiles, on first use, the JSON request-body schema
of every operation in the served document, at the schema's own location in the document, through
the module's `NewJSONSchemaCompiler()`. Served and enforced are one byte source.

Handlers name their operation with a `ContractOperation` constant rather than reading chi's route
pattern, because the integration suite invokes handlers directly and those requests carry no
pattern. The nine enforced operations are the collectives create and update, member add and role
change, repository link, batch share, batch review, single review, and transcript share.

`readContractBody` reads the body under a 1 MiB cap and validates it; `decodeContractBody` then
decodes. Answers:

- 413 over the cap; 400 `Invalid request body` for invalid JSON or bytes after the value;
- 400 `request body failed contract validation: <field pointer>: <reason>` for a schema
  violation, rendered from the compiler's leaf messages only, so no schema location or URL
  reaches a client;
- 500 for an operation the contract gives no body (a wiring error the router test catches);
- 503 for a missing validator or one that could not compile the served contract.

The decoded object equals the validated object. Go's JSON decoder matches keys without regard to
case, so the validator refuses any key that differs from a declared field only in case, and the
body handed to the decoder is re-encoded with only the declared fields (`contractBody.Declared`),
numbers kept as `json.Number` so an int64 survives exactly. `contractBody.Undeclared` lists the
dropped keys for handlers that refuse them rather than ignore them: the batch share handler does,
because a misspelled `visibility_confirmed` must not read as unconfirmed. The share route reads
and validates its body before taking the publish lock and hands the locked function the declared
bytes.

Two observable tightenings follow, both consistent with the served contract: a share request
whose `group_ids` carries a value outside the contract's UUID pattern is refused whole, where the
handler used to skip the element; and trailing bytes after a JSON value are refused.

## Frontend types

The collectives, share, contribution, and repository types in `frontend/src/lib/types.ts`,
`review/types.ts`, and `contribute/types.ts` are aliases of the generated `Village*` types from
`@peasant-labs/schema`, under their historical names, kept one release. Two documented helpers
widen what the frontend actually reads, since responses go through `JSON.parse` and not the
package's parsers: `AsPlainString` for the branded `project_hash`, `AsJSONNumber` for the three
int64 group stats the package types as `bigint`. `Group` is a composition of the three contract
shapes the app used one name for, with the two pull-request check settings optional until the
server stores them.

The create and update collective bodies are built by `queries/groupRequests.ts`, which runs the
contract's own parsers before sending. The create request does not accept a null organisation
where the update request does (peasant-labs/schema#121), so a blank organisation is omitted.

`frontend/src/lib/contractTypes.test.ts` loads `src/testdata/contract-type-aliases.yaml` strictly
and asserts each alias's exact declaration, that each generated type exists in the package, and
that no hand-written interface or object type re-declares a contract name outside the listed
out-of-scope names.

## Ratified decisions

- Manifest now; declaring the 38 routes is a parallel schema follow-up, not a prerequisite.
- Schema-invalid bodies on the collectives routes answer 400, not 422; publish and annotation
  keep 422.
- Aliases keep the existing names in place.
- Both pins move to v0.20.0 in this change.
