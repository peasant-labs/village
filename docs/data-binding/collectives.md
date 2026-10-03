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

Bulk removal retains the canonical inline confirmation. Selection and the
confirmed batch bind the current account and credential; each sequential DELETE
checks that binding before dispatch and after its response. A changed account or
credential stops the remaining batch, without applying its old failure to the new
viewer. Flat detail, paged transcript, and own-contribution reads include viewer
identity below their existing invalidation prefixes; pagination retains previous
rows only for the same viewer. Credentials stay out of query keys and storage.

Five named mounted removal fixtures hold the first real DELETE response while
changing identity or credentials, independently for success and failure. They
observe the actual confirmation, exact HTTP authorization, absence of a second
DELETE, and current-viewer private-row isolation after a real `/auth/me` refetch.
The dispatch case changes credentials synchronously after confirmation, proving
the mutation function rechecks the binding even when React Query defers it.
The existing in-memory route harness supplies these observations without new
processes or lifetime resources.

The three flat collective reads also bind the viewer and credential before
dispatch and before accepting the response. Each mounted binding has an opaque
React namespace and local numeric epoch below the viewer in its cache key.
Render only compares a private equality closure and adjusts its own guarded
state; it never writes module globals or waits for an effect to change the key.
A changed viewer or credential derives a fresh key immediately. A remount has
a new namespace, including the same-user, same-credential case. Credentials
remain in memory closures, outside query keys and persisted cache data.

This deliberately gives separate bindings separate cache entries rather than
coalescing duplicate mounted consumers of the same endpoint. The actual detail
mount keeps one flat binding, pagination retains its same-binding placeholder,
and existing mutation invalidation prefixes still invalidate every scope.
Four named mounted fixtures observe rejected dispatch, discarded late data,
recovery back to the original credential, and held remount reads with the same
QueryClient. Both guard-removal mutations still fail at their named HTTP/cache
assertions; removing the mount namespace fails the remount cache assertions.

The owner also retains GitHub handle search from the settings page. The
canonical settings body stays intact; an additive search exit opens Fairtrade's
Dialog with the existing typeahead. Picking a result only selects its handle;
invite sends the existing member POST. A failed invitation stays announced in
the dialog with retry, and names retain their original case. Named mounted
fixtures and a both-theme production journey cover search, explicit confirmation,
the exact request body, and recovery.
