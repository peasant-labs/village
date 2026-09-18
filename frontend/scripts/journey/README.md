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

# a still frame per test is captured by default; disable with:
JOURNEY_SCREENSHOT=off pnpm journey

# a trace per test (openable inline from the report); failures always keep one
JOURNEY_TRACE=1 pnpm journey
# equivalent CLI override: pnpm exec playwright test --config playwright.journey.config.mjs --trace on

# a video per test (the heavy one; opt-in)
JOURNEY_VIDEO=1 pnpm journey

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