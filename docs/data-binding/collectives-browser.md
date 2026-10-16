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
one is missing. The existing `journey:ci` remains the dev-mode catalog; it excludes
this focused built test and is not substituted for production evidence.

The wrapper binds a loopback fixture API to a kernel-allocated port, compiles the
full `NEXT_PUBLIC_API_URL` including `/api/v1`, builds Next, then runs `next start`
with that same configuration. It records source commit, source file/diff hashes,
lock hash, `.next/BUILD_ID`, asset hashes, and verifies served HTML/build ID and
every referenced script/style before Playwright starts. No frontend network
interception replaces application routing or API-client behavior.

Both rendered themes exercise a real main-nav click, every legacy URL with query
values, canonical active state and representative mounted bodies. The journey
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

The `.collectives-built-lock` serializes access to this worktree's `.next` output;
use separate worktrees for concurrent builds. The wrapper owns detached child
process groups and closes its in-process HTTP fixture in `finally` and on signals.
TERM is followed by bounded KILL only for a group whose process environment still
contains the run's random ownership marker. After SIGKILL, the next invocation
checks the old owner's Linux process start identity and reaps only groups with
that exact marker, not arbitrary listeners or reused PIDs. No live credentials,
database, user XDG directory or fixed fixture port are used. Sanitized evidence is
retained; there are no temporary credential stores to remove.

Mutation expectations: dropping a redirect/query value fails the real HTTP/URL
assertions; leaving a caller on the old path or dropping `/api/v1` fails request
assertions; removing canonical nav/body fails mounted assertions; stale assets
fail provenance before screenshots. The alias cases can retire only after the
release-window requirement passes. Simplify this runner if a shared built-app
runner later protects the same behavior, not while only a dev server covers it.
