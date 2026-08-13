# Visual capture harness — village frontend transcript view

Capture the **real assembled village transcript view** across every transcript surface + both themes,
for visual review. The village frontend wires a session payload through the shared
`@peasant-labs/transcript-browser` composer:

```
wire SessionDetailPayload → <SessionDetail>   (+ @xyflow TrajectoryGraph in the graph slot)
```

These scripts produce **capture artifacts only** — they do not assert pixel/data parity. Parity is a
human judgement made from the side-by-side composites. (The per-surface non-empty gate **does** fail a
blank/duplicate capture, so a vacuous "both empty → looks identical" can't slip through.)

> Intended home: `frontend/scripts/visual/`. The scripts hardcode **no** absolute or worktree-specific
> paths; everything host-specific is an env var or CLI arg (see below).

## Oracle — what the transcript captures are (and are NOT) judged against

The village transcript view renders the **`@peasant-labs/transcript-browser` `<SessionDetail>` composer**
(its own `.tb-*` markup), importing from fairtrade only the `adaptTranscript`/ViewModel data + token
layer — **not** the fairtrade demo's `TranscriptViewer` (`.txn-*`). So the app `.tb-*` vs the demo
`.txn-*` is a **component difference, not a regression**: the design-system demo is the **wrong** surface
oracle for transcript (unlike the chrome harness, where demo-parity **is** the gate).

**The pass/fail for transcript is the harness's AUTOMATED gates, not a side-by-side:**

1. **Theme / design-language cohesion** — every surface renders with fairtrade tokens under
   `[data-theme]`, in **both** themes. The harness hard-gates that the theme actually flips (`die(3)`)
   and captures dark + light.
2. **No host-integration regressions** — the composer mounts + renders real content in the village host
   (the structural gates `die(4)`/`die(5)` + the non-empty `SurfaceGate`, incl. md5 duplicate-detection,
   enforce "it actually rendered" — no blank/duplicate surface slips through).
3. **No-regression vs the same-component `<SessionDetail>` reference** — `stitch-sxs.mjs` pairs the
   committed **transcript-browser `<SessionDetail>`** reference (`baseline/tb/`, the prior-epoch capture
   of the same `sess_demo_0001` session, taken *before* this epoch's theme-convergence) against the
   current village capture. Both are `.tb-*` and the same data. The SxS is **not** expected to be
   zero-diff — this epoch's theme-convergence delta is intentional; it is judged for **design-language
   cohesion + no host-integration regression**, not pixel-identity. (That frozen "before" is
   non-regenerable, so it ships **committed** — see `baseline/tb/` below.)

The "10/10 surfaces" the shoot reports is a **capture-completeness** count (every surface rendered
non-empty content past the `SurfaceGate`) — **not** a demo-parity pass/fail.

> The `REF_DIR=demo` pairing (the fairtrade demo's `TranscriptViewer` vs the app) is kept only as a
> **NON-GATING design-language sanity panel** — a quick eyeball that the tokens/type/spacing read the
> same *language*. It is **not** a parity gate (different components under the KEEP-`<SessionDetail>`
> consumption boundary).

## The harness route (how the composer mounts without a backend)

The real transcript page (`/transcripts/[id]`) fetches its payload over REST and gates actions on auth,
so it can't render in a bare `next dev`. Instead the harness drives a **dev-only fixture route**,
`/dev/visual-harness`, which mounts the SAME `<SessionDetail>` composer with a bundled
`SessionDetailPayload` fixture (`src/app/dev/visual-harness/sample-session.ts` — the canonical
`sess_demo_0001` recorded session). The route:

- renders the composer in normal document flow with a sticky harness strip on top. The shared
  `<SessionDetail>` composer (`.tb-detail` root) scrolls the **page** and reveals a sticky condensed
  header (`.tb-stickyhead`, carrying the scrubber) on scroll — it is **not** a height-bounded
  inner-scroller. Some of its surfaces are viewport-relative (the rail/graph fill the viewport height),
  so the capture does **not** grow the viewport; it relies on the whole transcript being in the DOM in
  normal flow and uses `captureBeyondViewport:true` to raster each element's full bounds in one frame.
- carries its own `.theme-btn` that flips `[data-theme]` on the document element (the way the app's real
  theme control does), so the capture toggles + asserts theme the same way for both apps.
- mounts the composer with the same props the real page uses (detected phases, derived annotations, the
  @xyflow graph slot, **and village's real per-turn label control wired into the `renderTurnActions`
  slot** — the same slot the production page uses) but all capabilities on and host callbacks stubbed.
  It 404s in a production build, so it never ships as a public route.

### Selectors & surface names

The shots are written under the canonical **`txn-*` surface names** (so they pair 1:1 with the demo
captures), but the village `<SessionDetail>` composer renders those surfaces with **`.tb-*` classes**
(`.tb-detail`, `.tb-canvas`, `.tb-scorecard`, `.tb-detail-graphwrap`, `.tb-stickyhead` / `.tb-scrubber`,
the `.tb-segmented-btn` list/graph toggle, `.tb-detail-railwrap`) — the harness compares the same
SURFACES, not the same class strings. The per-turn label popover (`txn-label-popover`) is captured
through village's own label control (`button[aria-label="Add label"]` → `.pop-card[role="dialog"]`),
mounted into the composer's `renderTurnActions` slot exactly as the production page mounts it.

## Scripts

| Script | Role |
|---|---|
| `probe-village.mjs` | Print the harness route's DOM shape — tab/view-toggle labels, `.tb-*` box sizes, and the page scroll metrics. Run first whenever the harness route or the shared composite changes, to confirm the capture selectors still resolve and the page overflows the viewport (`document.scrollHeight > innerHeight`). |
| `village-shoot.mjs` | Drive the harness route with puppeteer and screenshot every transcript surface for one theme. Each capture is run through the non-empty-surface gate before it's accepted; each surface is wrapped in try/catch so one failure records a gap and the run continues. |
| `surface-gate.mjs` | The non-empty-surface gate (vendored, self-contained copy of the fairtrade `scripts/surface-gate.mjs`). Fails a capture that is blank / near-empty / byte-identical to another surface — closing the silent-blank hole a valid-but-empty bounding box leaves open (e.g. an empty graph). |
| `stitch-sxs.mjs` | Compose labeled, **height-matched** side-by-side composites (`REFERENCE | SUBJECT`) per surface per theme. The shorter pane is padded (never scaled) with its own border-sampled background; a dashed hairline marks where the shorter capture ends. A surface missing a subject capture gets a labeled placeholder panel so the set stays complete. The reference side defaults to the **committed `baseline/tb/`** (the same-component `<SessionDetail>` "before"); `REF_DIR=demo` is the optional non-gating design-language sanity panel (see **Oracle**). |
| `baseline/tb/{dark,light}/` | The **committed** same-component reference — the prior-epoch transcript-browser `<SessionDetail>` capture (taken *before* this epoch's theme-convergence) of the same `sess_demo_0001` session. It is a **frozen, non-regenerable** snapshot (that app state is gone post-convergence), so unlike the regenerable `demo/` it is **tracked in the repo** — the only place the no-regression "before" survives. The default `stitch` reads it directly, so the oracle works on a clean checkout with no staging. |

The **subject side** (`<base>/village/<theme>/`) is the current `village-shoot.mjs` run. The optional
`demo` reference (`<base>/demo/<theme>/`) comes from the fairtrade design-system's own harness
(`fairtrade-design-system/scripts/shootdemo.mjs`); the default `tb` reference ships **committed** with
the harness (above), so nothing needs staging for the no-regression oracle.

## Adding a surface to the two-arm gate

The harness is two arms over a shared surface set: the **capture+diff arm** (`village-shoot.mjs` →
`stitch-sxs.mjs`, which now runs an **imgdiff** pixel gate, not only a human-glance composite) and the
**host-integration boot arm** (`boot-village.mjs`). To register a new surface end-to-end:

1. **Capture it** — add a `await surface('txn-<name>', async () => { … await shotFull('txn-<name>', '<sel>') })`
   block in `village-shoot.mjs` (navigate/reveal the surface, then shoot its selector). Captures run under
   `applyDeterminism` (frozen clock + seeded `Math.random` + reduced motion, from `determinism.mjs`) so the
   PNG is byte-stable for the diff.
2. **Register it in the diff arm** — append `['txn-<name>', null]` to the `SURFACES` array in
   `stitch-sxs.mjs`. The stitch pixel-diffs the raw reference vs the raw app capture per `[surface, theme]`.
3. **Register a boot arm** — append a `{ id, url, mount, capture, interact }` entry to the `SURFACES`
   registry in `boot-village.mjs` (the block comment there documents each field).
4. **Add a baseline PNG** — commit `scripts/visual/baseline/tb/{dark,light}/txn-<name>.png` so the
   no-regression reference exists for both themes (a missing baseline fails the diff closed as `NO-REF`).
5. **imgdiff thresholds** — the gate uses `IMGDIFF_TOL = 16` (per-channel, /255; absorbs AA shimmer) and
   FAILs any surface whose differing-pixel share exceeds `IMGDIFF_FAIL_PCT = 0.5` (%), or that is
   non-comparable (missing ref/app, or a size mismatch). Both consts live at the top of `stitch-sxs.mjs`.

## Environment

| Var | Used by | Default | Notes |
|---|---|---|---|
| `CHROME_PATH` | all | — (**required**) | Path to a Chrome/Chromium binary puppeteer drives. |
| `VILLAGE_URL` | probe, shoot | `http://localhost:3000/dev/visual-harness` | The harness route's dev-server URL (`next dev` default port). |
| `REF_DIR` | stitch | `tb` | Reference (left) set name. Default `tb` = the committed same-component `<SessionDetail>` "before" (read from `scripts/visual/baseline/tb/<theme>/` unless a same-named set is staged under `<base>/`); set `demo` for the design-language sanity panel (staged under `<base>/demo/`). |
| `REF_LABEL` | stitch | the `<SessionDetail>` reference caption | The reference-pane caption drawn on each composite. |
| `APP_DIR` | stitch | `village` | Subject (right) capture subdir under `<base>` (`<base>/<APP_DIR>/<theme>/`). |
| `APP_LABEL` | stitch | the village caption | The subject-pane caption drawn on each composite. |
| `PUPPETEER_CORE` | all | `puppeteer-core` | Explicit module path to `puppeteer-core`, **only** if a bare import won't resolve (see below). |

**`puppeteer-core` resolution.** These scripts `import('puppeteer-core')`, which is a **devDependency of
this app** — so `pnpm install` makes the bare import resolve and `pnpm run probe:village` /
`pnpm run shoot:village` / `pnpm run sxs` run out of the box (you still supply `CHROME_PATH`).
`PUPPETEER_CORE` is only needed to point at an explicit copy (e.g.
`PUPPETEER_CORE=/path/to/fairtrade/node_modules/puppeteer-core`) when running a script from a context
where that bare import can't resolve.

## Run

```sh
CHROME=/path/to/google-chrome
BASE=/abs/path/to/capture-base        # holds village/ (current), sxs/ (+ optional demo/)

# 0. (optional) probe the DOM after any harness-route or composite change
CHROME_PATH=$CHROME node scripts/visual/probe-village.mjs

# 1. app side — start the village frontend dev server (`pnpm dev`), then shoot both themes
CHROME_PATH=$CHROME node scripts/visual/village-shoot.mjs dark  $BASE/village/dark
CHROME_PATH=$CHROME node scripts/visual/village-shoot.mjs light $BASE/village/light

# 2. transcript SxS — the same-component <SessionDetail> before/after. The reference is the COMMITTED
#    baseline/ (no staging needed); a regression shows as a visible divergence in the side-by-side.
CHROME_PATH=$CHROME node scripts/visual/stitch-sxs.mjs $BASE

# 3. (optional) design-language SANITY panel — the fairtrade demo (a DIFFERENT component) vs the app.
#    NON-GATING: an eyeball that the design language reads the same. Requires the demo side staged into
#    $BASE/demo/ (the fairtrade design-system's own scripts/shootdemo.mjs).
CHROME_PATH=$CHROME REF_DIR=demo REF_LABEL="DEMO  (fairtrade TranscriptViewer — design-language sanity)" \
  node scripts/visual/stitch-sxs.mjs $BASE
```

## Failure contract (two tiers)

`village-shoot.mjs` distinguishes a whole-run invalidity from a single-surface miss:

- **Structural gates → HARD non-zero exit, abort immediately** (the run cannot produce valid output),
  with a DISTINCT code so "harness broke" stays separable from "a surface regressed":
  - **exit 3 — theme-didn't-flip** — after clicking `.theme-btn`, `[data-theme]` is not the requested
    theme (every capture would be the wrong theme).
  - **exit 4 — composite-not-rendered** — `.tb-detail` never mounted (the composer didn't render).
  - **exit 5 — page-not-scrolling** — the document does not overflow the viewport
    (`document.scrollHeight <= innerHeight`). This is the page-scroll analog of the reference harness's
    "the internal stream never overflows" gate: there is nothing to capture.
- **Per-surface outcomes → recorded + the run CONTINUES, then exits 1 (clean run exits 0)**:
  - a **gap** — a single surface failing (selector never mounted, a popover that didn't open, one
    blank/duplicate `SurfaceGate` rejection) is recorded with an actionable reason; `stitch-sxs.mjs`
    draws a labeled placeholder for it so it is visible in the side-by-side rather than silently dropped.
  - a **partial** — a surface taller than the 8000px ceiling is captured **in full**
    (`captureBeyondViewport` never clips/drops it) and flagged for review.
  A run with ≥1 gap or partial exits **1** so CI flags it (distinct from the structural 3/4/5); a clean
  run exits **0**.

The final line prints `captured: N (partial: P) gaps: M` and a `RESULTS_JSON=…` summary.

## Host-integration check (R9) — `boot-village.mjs`

The capture harness drives a backend-free dev **fixture** route for determinism, so it deliberately
**bypasses village's real data path**: `/transcripts/[id]` → React Query (`useTranscript` +
`useTranscriptContent`) → REST `GET /transcripts/{id}` + `/transcripts/{id}/content` → the
`SessionDetailV2` adapter → `<SessionDetail>`. `boot-village.mjs` is the **host-integration arm** that
covers exactly that gap: it boots the **real** viewer route against a REST backend and asserts
`.tb-detail` actually renders non-empty through the real adapter + REST/React-Query path — so a broken
REST wiring / adapter / host shell fails LOUD even when the fixture-route captures are green.

`mock-rest.mjs` is a tiny REST stand-in (village's analog of peasant's `--mock-data-store`) so the check
is self-contained — no Postgres/MinIO/auth stack:

```sh
CHROME=/path/to/google-chrome
# 1. serve the REST endpoints (a representative session)
MOCK_REST_PORT=8788 node scripts/visual/mock-rest.mjs &
# 2. start the app pointed at the mock (NEXT_PUBLIC_API_URL is the only API-base env the app reads)
NEXT_PUBLIC_API_URL=http://localhost:8788/api/v1 pnpm dev &      # next dev on :3000
# 3. boot the REAL route + assert the composite renders from REST
CHROME_PATH=$CHROME VILLAGE_TRANSCRIPT=demo node scripts/visual/boot-village.mjs
```

Or point it at a real village backend that serves a viewable transcript (`VILLAGE_REAL_ORIGIN` +
`VILLAGE_TRANSCRIPT`, or `VILLAGE_REAL_URL`). Exit codes: **0** = the real route rendered a non-empty
`.tb-detail`; **2** = `.tb-detail` never mounted (the real REST/React-Query path is broken/unreachable);
**1** = the rendered surface failed the non-empty `SurfaceGate`. (The mock serves a representative
session sufficient to prove the render path; point at a real backend for the canonical data.)

## Explore surface gate (`cex-explore`)

The Explore gate is a separate browse-focused harness for the shared `Explore` surface:

- **Reference (left):** fairtrade in-use demo capture `app-2-village.png` from
  `fairtrade-design-system/scripts/shootdemo.mjs`.
- **Subject (right):** the village home route capture `cex-explore.png` from
  `frontend/scripts/visual/explore-shoot.mjs`.
- **Boot arm:** `frontend/scripts/visual/boot-explore.mjs` against
  `frontend/scripts/visual/mock-rest-explore.mjs`.

Repeatable run:

```sh
CHROME=/path/to/google-chrome
BASE=/abs/path/to/capture-base

# 0. Probe the route before capture.
CHROME_PATH=$CHROME node scripts/visual/probe-explore.mjs

# 1. Start the mock REST backend for the real app route.
MOCK_REST_PORT=8789 node scripts/visual/mock-rest-explore.mjs &

# 2. Start the app against that API base.
NEXT_PUBLIC_API_URL=http://localhost:8789/api/v1 npm run dev &

# 3. Capture the subject side for both themes.
CHROME_PATH=$CHROME node scripts/visual/explore-shoot.mjs dark  $BASE/village/dark
CHROME_PATH=$CHROME node scripts/visual/explore-shoot.mjs light $BASE/village/light

# 4. Stage the fairtrade demo reference from the fairtrade-design-system worktree root.
#    Run these from ../../../../fairtrade-design-system/fairtrade-village-explore.
CHROME_PATH=$CHROME node scripts/shootdemo.mjs dark  $BASE/demo/dark
CHROME_PATH=$CHROME node scripts/shootdemo.mjs light $BASE/demo/light

# 5. Stitch the cex surface set (reference = demo/app-2-village, subject = cex-explore).
CHROME_PATH=$CHROME SURFACE_SET=cex node scripts/visual/stitch-sxs.mjs $BASE

# 6. Boot-arm the real route.
CHROME_PATH=$CHROME node scripts/visual/boot-explore.mjs
```

Manual click-through checklist for the non-automated part of the gate:

1. Search for `ai`; wait for the debounce, then confirm `AI Research Team` and `Verified Contributors` appear in the collective strip.
2. Toggle provider `claude-code` and topic `greenfield`; clear filters and confirm transcript `d41a8e` returns.
3. Switch between grid and list.
4. Open transcript `d41a8e`, confirm the route changes to `/transcripts/d41a8e`, then open profile `alice-dev` and confirm `/users/alice-dev`.
5. Search `ai` again, open `AI Research Team`, confirm `/groups/ai-research-team`, then back out and confirm the shell still renders the card grid.
