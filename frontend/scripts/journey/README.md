# Village journey harness (Playwright)

Scripted, agent-runnable **feature flows** that produce motion evidence and
machine-readable validation. This is the interaction/motion arm the screenshot
harness in `scripts/visual/` does not cover; it does not replace that harness or
its pixel/provenance gates.

A journey boots the app and its mock REST backend, drives a real feature, and
asserts the semantic surface (ARIA roles), the design-system contract (computed
tokens), routes, and WCAG. A failing run keeps its trace and video; every run
records the ARIA tree and axe report.

## Run

```sh
# both themes, artifacts under scripts/journey/.artifacts
pnpm journey

# also record a video for every test (not only failures)
JOURNEY_VIDEO=1 pnpm journey

# open the HTML report
pnpm journey:report
```

The harness starts the app and mock itself (`webServer` in
`playwright.journey.config.mjs`) on ports `3010` (app) and `8799` (mock), so no
manual setup is needed. Override with `JOURNEY_APP_PORT` / `JOURNEY_MOCK_PORT`.

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
| `lib/determinism.mjs` | Frozen clock + seeded PRNG (ported from `scripts/visual/determinism.mjs`) |
| `lib/fixtures.mjs` | Authenticated, theme-pinned, deterministic context |
| `lib/assertions.mjs` | axe, computed-token, and theme assertions |
| `*.journey.mjs` | One file per fundamental feature |

## Adding a journey

Add `<feature>.journey.mjs` next to `explore.journey.mjs` using the shared
`test`/`expect` from `lib/fixtures.mjs`, assert roles and computed tokens rather
than class strings, and keep every case in fixtures/data, not inline tables.

## Status and next steps

This is the first keystone slice (village Explore). The app-agnostic modules in
`lib/` are intended to move to the fairtrade canonical harness and be vendored
back into consumers, the same way `scripts/visual/surface-gate.mjs` is vendored
today; served-bytes build provenance (as the production shoots assert) is the
next slice, since a dev server cannot provide it.