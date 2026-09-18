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
uploads `scripts/journey/.artifacts/` as a workflow artifact, and posts inline
evidence to the PR through `scripts/journey/ci-post-evidence.mjs`:

- screenshots first (failing tests' frames, then a few passing dark-theme frames);
- on failure, the failing tests' videos, converted to H.264 mp4;
- one bot comment, replaced on every run (marker `journey-evidence`).

GitHub plays video inline only as a comment attachment, so clips are attached
with `gh pr comment --attach`; full traces stay in the workflow artifact.

**Limitation:** `gh --attach` rejects GitHub App installation tokens
("unsupported authentication type"; gh only allows OAuth/PAT/fine-grained-PAT
for asset uploads, and the endpoint needs repo write access). So with the App
token the script falls back to a text-only comment. To get inline media you
need either a machine-user PAT for the posting step, or the App plus hosted
image links (no inline video).

Required organization setup (once): a GitHub App with **Issues: Read and write**
and **Pull requests: Read** (Metadata: Read is implicit), installed on the
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
| `lib/vendor-guard.test.mjs` | Fails when a vendored body drifts from the fairtrade canonical copy |
| `mock.mjs` | Composed backend: browse in-process, transcript detail proxied, scenario control |
| `lib/scenario.mjs` | Sets the composed mock's scenario from a journey |
| `*.journey.mjs` | One file per fundamental feature |

## Adding a journey

Add `<feature>.journey.mjs` next to `explore.journey.mjs` using the shared
`test`/`expect` from `lib/fixtures.mjs`, assert roles and computed tokens rather
than class strings, and keep every case in fixtures/data, not inline tables.

## Status and next steps

Covers Explore and the transcript viewer. The app-agnostic modules in `lib/`
(`determinism-constants.mjs`, `determinism.mjs`, `assertions.mjs`) are vendored
byte-faithfully from `fairtrade-design-system/scripts/journey/lib/`;
`pnpm check:journey-vendoring` (with `FAIRTRADE_CHECKOUT` set) fails when a
vendored body drifts. `lib/fixtures.mjs` and `lib/scenario.mjs` stay
app-specific. The composed mock currently serves explore + transcript + auth;
the home and collective surfaces can be folded in the same way. Served-bytes
build provenance, as the production shoots assert, is a later slice, since a dev
server cannot provide it.