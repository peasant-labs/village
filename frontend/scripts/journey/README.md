# Village journey harness (Playwright)

Scripted, agent-runnable **feature flows** that produce motion evidence and
machine-readable validation. This is the interaction/motion arm the screenshot
harness in `scripts/visual/` does not cover; it does not replace that harness or
its pixel/provenance gates.

A journey boots the app and a **composed** mock REST backend, drives a real
feature, and asserts the semantic surface (ARIA roles), the design-system
contract (computed tokens), routes, and WCAG. A failing run keeps its trace and
video; every run records the ARIA tree and axe report.

## Run

```sh
# both themes, artifacts under scripts/journey/.artifacts
pnpm journey

# CI shape: JSON report + screenshots, no HTML report copy
pnpm journey:ci

# a still frame per test is captured by default; disable with:
JOURNEY_SCREENSHOT=off pnpm journey

# a trace per test (openable inline from the report); failures always keep one
JOURNEY_TRACE=1 pnpm journey
# equivalent CLI override: pnpm exec playwright test --config playwright.journey.config.mjs --trace on

# a video per test (the heavy one; opt-in)
JOURNEY_VIDEO=1 pnpm journey

# capture viewport (default 720p); raise it to match the Puppeteer gates
JOURNEY_VIEWPORT=1396x939 pnpm journey

# open the HTML report
pnpm journey:report
```

Note: `--trace <mode>` is the only artifact flag on the Playwright CLI;
screenshots and video are `use` config options, controlled here by the
`JOURNEY_SCREENSHOT` / `JOURNEY_VIDEO` env switches.

The harness starts the app and a single composed mock itself (`webServer` in
`playwright.journey.config.mjs`) on ports `3010` (app) and `8799` (mock), so no
manual setup is needed. The composed mock (`scripts/journey/mock.mjs`) serves the
browse fixtures in-process and the transcript-detail fixtures from
`scripts/visual/mock-rest.mjs` behind an internal port: one command, no mock
selection and no port matrix. Override ports with `JOURNEY_APP_PORT` /
`JOURNEY_MOCK_PORT` if they collide.

A journey declares the world it needs with `setScenario(request, 'empty')`
(`lib/scenario.mjs`), which calls the mock's `POST /__mock/scenario`; reset it to
`'default'` when done.

Chrome is the NixOS-packaged browser: set `CHROME_PATH`, or accept the default
profile path. No Playwright browser download is required, which is why this works
on NixOS without `playwright install`.

## Artifacts

| Path | What |
|---|---|
| `report.json` | Machine-readable per-test results for agents |
| `html/` | Human report (`pnpm journey:report`) |
| `results/<test>/trace.zip` | Deterministic filmstrip + DOM snapshots (open with `npx playwright show-trace <zip>`) |
| `results/<test>/video.webm` | Live clip (failures, or `JOURNEY_VIDEO=1`) |
| `results/<test>/attachments/` | The ARIA tree (`explore-aria.yml`) and axe report (`axe.json`) |

## Size

Playwright's HTML reporter copies every attachment into `html/data/` (that is
builtin; `attachmentsBaseURL` changes only the link, not the copy), so a run's
media exists twice: under `results/<test>/` and inside the report. Measured for
the 14-test, two-theme catalog at 1280x720 with screenshots and video:

| Part | Size |
|---|---|
| report bundle (`html/`) | ~3.3 MB |
| raw results (`results/`) | ~2.9 MB |
| whole `.artifacts/` | ~6.5 MB |
| screenshots (14 PNG) | ~1.34 MB |
| video (18 webm at 800x450) | ~1.34 MB |
| `index.html` (test/step metadata) | ~0.5 MB |

Traces dominate when forced on: one trace is several MB, so keep `JOURNEY_TRACE`
off for a full-catalog run. Agents do not need the HTML report (`report.json`
plus the persisted `axe.json`/ARIA files are ~1 KB), so a CI run can drop the
HTML reporter, or `JOURNEY_SCREENSHOT=off`, to avoid the duplicated media.

## CI and PR evidence

`.github/workflows/journey.yml` runs `pnpm journey:ci` on every pull request,
uploads `scripts/journey/.artifacts/` as a workflow artifact, and posts the
evidence as a comment per run through `scripts/journey/ci-post-evidence.mjs`:

- a title and the workflow run / commit ids;
- one sticky comment: the newest run expanded, each previous run appended and
  collapsed so the history stays in one place;
- one collapsible block per journey, headed with **passed** or **failed**; failed
  journeys render expanded and passing journeys collapse. Inside are the dark
  image, dark clip, light image and light clip, each alone in its own paragraph.

Media is uploaded to GitHub's user-attachments endpoint to get URLs, so a single
comment carries all images and clips without hitting `gh`'s 50-file cap. GitHub
renders a video player only when the video link is alone in its paragraph or
cell; any text sharing the line turns it into a plain link. Clips are converted
to H.264 mp4 where the bundled ffmpeg is available, and full traces stay in the
workflow artifact.

**Inline media:** `gh --attach` rejects GitHub App installation tokens ("unsupported authentication type"; gh allows OAuth/PAT/fine-grained-PAT only, and the endpoint needs repo write access). Set the **`JOURNEY_GITHUB_USER_PAT`** org secret to a machine-account fine-grained PAT (Issues: Read and write, Pull requests: Read) and the posting step uses it, so the comment carries inline screenshots and video and is authored by that account. Without it the step falls back to the App token and posts a text comment linking the artifact.

Required organization setup (once): a GitHub App with **Issues: Read and write**
and **Pull requests: Read and write** (Metadata: Read is implicit), installed on the
organization with access to this repository. Store the App's client id in the
`JOURNEY_APP_CLIENT_ID` org **variable** and its private key in the
`JOURNEY_APP_PRIVATE_KEY` org **secret**. Using an
App (not the default `GITHUB_TOKEN`) gives a stable bot identity instead of
`github-actions[bot]`. Pull request workflows from forks do not receive
repository secrets, so fork PRs would need `pull_request_target` or
`workflow_run` instead.

## Layout

| File | Role |
|---|---|
| `playwright.journey.config.mjs` | Two theme projects, `webServer`, reporters, artifact dir |
| `lib/determinism.mjs`, `lib/determinism-constants.mjs` | Frozen clock + seeded PRNG (vendored from fairtrade) |
| `lib/assertions.mjs` | axe, computed-token, and theme assertions (vendored from fairtrade) |
| `lib/fixtures.mjs` | Authenticated, theme-pinned, deterministic context (app-specific) |
| `lib/vendor-guard.mjs` | The vendoring guard: compares every vendored body against its pinned digest, and against the live fairtrade file when `FAIRTRADE_CHECKOUT` is set. Run it as a tool to re-pin |
| `lib/vendor-guard.test.mjs` | Fails when a vendored body drifts from its pin, when a pin is missing or malformed, or when a file in `lib/` is unclassified |
| `mock.mjs` | Composed backend: browse + project in-process, transcript detail proxied, scenario control |
| `lib/project-fixtures.mjs` | The project page's orphan-sessions fixtures (app-specific) |
| `lib/scenario.mjs` | Sets the composed mock's scenario from a journey |
| `*.journey.mjs` | One file per fundamental feature |

## Adding a journey

Add `<feature>.journey.mjs` next to `explore.journey.mjs` using the shared
`test`/`expect` from `lib/fixtures.mjs`, assert roles and computed tokens rather
than class strings, and keep every case in fixtures/data, not inline tables.

## Status and next steps

Covers Explore, the project page's orphan-sessions group, and the transcript
viewer. The app-agnostic modules in `lib/`
(`determinism-constants.mjs`, `determinism.mjs`, `assertions.mjs`) are vendored
byte-faithfully from `fairtrade-design-system/scripts/journey/lib/`.
`lib/fixtures.mjs` and `lib/scenario.mjs` stay app-specific. The composed mock
currently serves explore + project + transcript + auth; the home and collective
surfaces can be folded in the same way. Served-bytes build provenance, as the
production shoots assert, is a later slice, since a dev server cannot provide it.

### The vendoring pin

`pnpm check:journey-vendoring` **always** runs and **always** compares every
vendored body against the sha256 pinned for it in
`lib/vendor-digests.testdata.yaml`. The pin is taken over the canonical body,
every byte after the VENDORED banner, so rewording that banner needs no re-pin.
There is no skip path and no environment in which the suite passes without
comparing: the pin is committed, so CI and any machine without a fairtrade clone
enforce the invariant, which is the point.

`FAIRTRADE_CHECKOUT` is an **opt-in extra** check, not the gate. Set it to a
fairtrade checkout and each vendored body is also compared against the live
canonical file. Upstream drift and a corrupt copy are separate findings with
separate drift classes (`upstream-canonical` says the copy here is intact;
`vendored-copy` says the copy is the side that is wrong), because collapsing them
sends the reader to re-pin a corrupt copy and make the gate green over a false
invariant. A checkout that is a consumer tree, or that lacks the file, is refused
by name too (`upstream-not-canonical`, `upstream-missing`).

The inventory is closed: every file in `lib/` is classified `vendored`,
`app-local` or `guard` in that corpus, so a new module cannot land unchecked.
Re-vendor first, then re-pin:

```sh
# 1. copy the upstream file over the vendored one and prepend the VENDORED banner
# 2. print the digest the re-vendored body now carries
node frontend/scripts/journey/lib/vendor-guard.mjs
# 3. write it into the sha256 field of that row, then
pnpm --dir frontend check:journey-vendoring
```

The cases are fixtures, never inline: the closed inventory, the four drift shapes
and the eleven executable mutations (each applied to a real copy of the tree, in
both the pin-only and the checkout modes) live in
`lib/vendor-digests.testdata.yaml` and `lib/vendor-digests.testdata.manifest.yaml`.