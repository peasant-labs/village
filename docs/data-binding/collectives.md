# Collective pages data binding

The mounted collective routes adapt Village reads and writes to Fairtrade's
collective list, detail and settings views. The server owns membership,
visibility, admission and contribution decisions; the frontend displays its
answers and never rebuilds those access rules.

| Surface | Boundary |
| --- | --- |
| Your collective list | `GET /groups` |
| Visible collective table | `GET /groups/visible` |
| Create collective | `POST /groups`; navigate to the returned collective ID only after success |
| Collective detail and settings | `GET /groups/{id}` |
| Accepted transcript page | Paginated `GET /groups/{id}/transcripts` |
| The caller's contributions | `GET /groups/{id}/my-shares` |
| Change a setting | Partial `PATCH /groups/{id}`; omitted name and description remain unchanged |
| Join or leave | `POST /groups/{id}/join`; `DELETE /groups/{id}/members/{userID}` with the chosen retraction option |
| Member administration | Existing add-member and role-change routes, guarded by server ownership |
| Repository connections | Existing collective repository routes; the backend decides permissions and GitHub effects |

Failed reads are errors with retry controls, including the visible table, detail
and settings. They cannot become a successful empty list or a not-found claim.
Failed creation preserves the entered form, shows the reason and leaves the
current route in place. A successful creation opens the actual returned ID.
Settings writes use the canonical saved response and settle through Fairtrade.

Policy summaries restate the server's saved values and do not enforce them.
Owner-only controls follow the server response. User names retain their case,
counts use tabular figures. The actual collective detail route retains the prior
transcript grouping and contribution controls alongside the canonical body.

Named fixtures mount the registered page components with real auth and query
providers, replacing HTTP only. Production journeys exercise the table, detail
and settings routes. Review screenshots must show their mounted full shell in
both themes from the exact source branch, verify served build provenance, and
remain distinct from the consolidated integration captures.

## Retained transcript and contribution exits

The canonical detail body stays first. Its existing flat rows also feed a quiet
`transcript groups` disclosure: child-session folds, contributor filtering,
repository grouping, and owner select-all/removal remain user initiated. Saved
helper groups use the canonical grouped collective read, opaque member scopes,
and the existing continuation hook; no scope is inferred from a parent ID.
The caller's own contribution panel reads the existing flat and grouped
`my-shares` routes, retaining pending/private rows, withdrawal, child folds,
helper-only contexts, and continuation. A failed read or write is announced and
can be retried. Browse permission and owner controls follow the existing server
response; this adds no wire fields, migrations, or persisted state.

The prior required-name browse and my-share fixtures are restored onto this
mounted production path. The child-fold corpus again covers collective browse,
repository grouping, and own contributions. Actual clicks prove withdrawal and
owner removal requests, rather than asserting that dormant components exist.

Test promotion: the subject is reachable production grouping and mutation
callbacks. A helper unit test cannot observe a page that stopped mounting them.
The harness mounts the actual routes, auth provider, and queries, replacing only
HTTP with the existing fixture world. It spawns no processes, copies no
production code, and creates no files or databases; each fixture cleans up its
React tree and HTTP stub, and separate Vitest workers own their worlds. It runs
from a clean checkout in the ordinary frontend suite (four focused files run in
about three seconds). A removed mount, continuation, or mutation callback must
fail its named user-visible assertion. The harness can be reduced if these exits
move to another canonical route and its mounted fixtures retain the same
observations. Exact-head both-theme shell and canonical side-by-side captures
remain a separate review gate.
