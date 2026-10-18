# Canonical collective browser routes

## Surface brief

- Signed-in commons readers browse their collectives and open the existing detail,
  settings, contribution and review flows. This is an Operate surface.
- Preserve the current Fairtrade commons composition, copy, themes, permissions,
  and `home | collectives` navigation. This change only canonicalizes addresses.
- All five `/collectives` pages retain their former bodies. Inbound `/groups`
  pages redirect on the server, preserving repeated, empty and encoded query values.
- Keep the legacy pages for one complete actual release window. Development time
  is not that window, and canonical rollout does not authorize their removal.

## Release dependencies

The schema package remains on its existing published version until the canonical
collective type aliases are released. This compatible preparation uses the existing
wire aliases, without copied wire definitions or a local package replacement.
Adopt the published canonical exports in the release integration change.

The canonical backend aliases must be deployed and verified before releasing this
client. Browser fixture success is not backend authorization, SQL, error-parity,
or deployment evidence. The backend owns those checks. Storage identifiers,
helper-group routes, and the GitHub install `group_id` parameter do not change.

## Built production journey

From a clean checkout with the repository Node/pnpm toolchain:

```sh
pnpm --dir frontend install --frozen-lockfile
pnpm --dir frontend run journey:collectives-built
```

Use `CHROME_PATH` for a preinstalled Chromium, or the pinned Playwright browser
installed in CI's container. The command does not download a browser or skip when
one is missing. Linux `/proc` and util-linux `flock` are required (included in the
Nix development shell and checked explicitly by CI). No portability to another
platform is claimed. The existing `journey:ci` remains the dev-mode catalog; it excludes
this focused built test and is not substituted for production evidence.

The wrapper binds a loopback fixture API to a kernel-allocated port, compiles the
full `NEXT_PUBLIC_API_URL` including `/api/v1`, builds Next, then runs `next start`
with that same configuration. It records source commit, source file/diff hashes,
lock hash, `.next/BUILD_ID`, asset hashes, and verifies served HTML/build ID and
every referenced script/style before Playwright starts. No frontend network
interception replaces application routing or API-client behavior.

Both rendered themes exercise a real main-nav click, every legacy URL with query
values, canonical active state and representative mounted bodies. Named YAML
assertions protect detail transcript data, settings controls, the empty contribution
and review bodies, their disabled actions, and real canonical back exits. A second
remote fixture state exposes a grouped-only contribution exit: the actual mounted
hook must consume its published, validated envelope rather than hiding a decoding
error behind a heading or an empty flat result. Grouped pending reads independently
validate their different published payload shape. The journey
checks computed type/radius, real `/api/v1/collectives` requests, and axe, and saves
full-page screenshots plus machine reports under `review-capture/collectives-built-*`.
Inspect the images before treating them as review evidence. PR screenshots still
need durable hosting from the exact reviewed build.

### Test promotion and lifecycle

Subject: production Next server redirects, built-in API base, and mounted canonical
navigation/body. Unit tests directly import the five real legacy pages; they cannot
observe compiled public environment variables, Next's HTTP redirects, or served
assets. Existing mounted unit tests continue to cover settings, auth, members,
repositories, review and contribution behavior. The focused browser fixture mocks
only the remote API dependency, never the frontend under test.

Cost: one build (600 seconds maximum), startup (30 seconds), one worker, no retries,
60-second tests and a 300-second suite. There is no new full catalog, screenshot
comparison framework, configuration matrix, or copied production policy engine.
CI runs the exact same command with frozen dependencies and explicit Chromium.

The `.collectives-built-lock.gate` inode is never deleted. A util-linux kernel
`flock`, retained by a small pipe-driven keeper, serializes the complete stale
identity read, owned-group reap, metadata replacement and build lifetime. A
contender waits at most half a second, then fails without changing the winner's
metadata. Pipe EOF after owner SIGKILL releases the kernel lock. Under that lock,
the next invocation checks the old owner's Linux process start identity and reaps
only groups with its exact random marker, never arbitrary listeners or reused PIDs.
Use separate worktrees for concurrent builds.

The immutable pid/start/marker record is atomically renamed into place before
any workload can spawn. Child launch never truncates or rewrites it. An interrupted
unpublished temporary identity is safe to discard under the kernel lock because
no workload can precede publication. Every launched child retains one settlement
promise, shared by execution and cleanup. The wrapper closes its in-process HTTP
fixture in `finally` and on signals. Marker-proven groups receive TERM, up to two
seconds to exit, then KILL and another bounded two seconds. Cleanup reaps all direct
children and verifies no marker-owned live groups remain before removing metadata
or releasing build ownership. If that proof fails, recovery metadata remains and
the command fails with corrective guidance. Polling is bounded by those deadlines;
each snapshot inspects only current Linux process records. No live credentials,
database, user XDG directory or fixed fixture port are used. Sanitized evidence is
retained; there are no temporary credential stores to remove.

Direct named YAML tests import that same lifecycle and fixture builder: a
TERM-resistant disposable child proves settlement and absence before cleanup
returns, two actual concurrent processes compete over a stale owner, and an
interrupted sibling bookkeeping file cannot damage immutable recovery identity.
Recovery must leave a different marker's child alive. These tests need no Next
build, database, browser, source copy, or second cleanup implementation; disposable
children have a 15-second self-exit deadline if their test parent is killed. Each
test owns a random temporary directory and removes it after bounded cleanup.
They run in the existing unit command on clean Nix/CI checkouts. This small process
test promotion is necessary because a direct browser assertion cannot observe
atomic ownership races or child reaping. It can simplify only when equivalent
process-lifetime coverage replaces it.

Mutation expectations: dropping a redirect/query value fails the real HTTP/URL
assertions; leaving a caller on the old path or dropping `/api/v1` fails request
assertions; removing canonical nav/body fails mounted assertions; stale assets
fail provenance before screenshots. Removing the actual contribution empty body
while retaining its heading and disabled action is also rejected by the real built
journey's body-text assertion in both themes. The alias cases can retire only after the
release-window requirement passes. Simplify this runner if a shared built-app
runner later protects the same behavior, not while only a dev server covers it.
