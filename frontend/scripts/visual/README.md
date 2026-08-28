# Visual capture harness — village frontend transcript view

Capture the **real assembled village transcript view** across every transcript surface + both themes,
for visual review. The village frontend wires a session payload through the shared
`<SessionDetail>` composer (now sourced from fairtrade, formerly a sibling package):

```
wire SessionDetailPayload → <SessionDetail>   (+ @xyflow TrajectoryGraph in the graph slot)
```

These scripts produce **capture artifacts only** — they do not assert pixel/data parity. Parity is a
human judgement made from the side-by-side composites. (The per-surface non-empty gate **does** fail a
blank/duplicate capture, so a vacuous "both empty → looks identical" can't slip through.)

> Intended home: `frontend/scripts/visual/`. The scripts hardcode **no** absolute or worktree-specific
> paths; everything host-specific is an env var or CLI arg (see below).

## Oracle — what the transcript captures are (and are NOT) judged against

The village transcript view renders the **shared `<SessionDetail>` composer**
(its own `.tb-*` markup, now sourced from fairtrade), importing from fairtrade the
`adaptTranscript`/ViewModel data + token layer, **not** the fairtrade demo's `TranscriptViewer`
(`.txn-*`). So the app `.tb-*` vs the demo
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
   committed **`<SessionDetail>`** reference (`baseline/tb/`, an earlier capture from before the fairtrade
   graph fold, recorded before theme convergence) against the
   current village capture. Both are `.tb-*` and the same data. The SxS is **not** expected to be
   zero-diff; the theme-convergence delta is intentional and judged for **design-language
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
| `explore-agent-group-shoot.mjs` | Capture the collapsed and expanded group of agent-driven sessions on the real Explore route, in one theme, from one live page. Asserts build provenance (the group's control, its counted label, and the expanded rows' labels) before writing any PNG, so a stale or wrong-worktree server fails instead of producing a misleading capture. |
| `child-session-shoot.mjs` | Capture how one surface treats a session that another session started, in one theme, from one live page: `explore` (folded away, no control), `home` and `project` (collapsed and expanded chip). Asserts build provenance against the live DOM before writing any PNG — on discovery, that the started rows are off the grid, that a row whose parent the response does not carry still browses, that NO control rendered, and that the count above the grid matches the cards under it; on the other two, that the chip carries a counted label and sits in the same list unit as its own parent row. Also asserts **capture geometry** on both sides of every raster: the page parked at scroll 0, the whole document inside the viewport, and the clip box covering the document — so the fixed app header cannot composite over the middle of the image and the bottom of the list cannot be cut. |
| `surface-gate.mjs` | The non-empty-surface gate (vendored, self-contained copy of the fairtrade `scripts/surface-gate.mjs`). Fails a capture that is blank / near-empty / byte-identical to another surface — closing the silent-blank hole a valid-but-empty bounding box leaves open (e.g. an empty graph). |
| `stitch-sxs.mjs` | Compose labeled, **height-matched** side-by-side composites (`REFERENCE | SUBJECT`) per surface per theme. The shorter pane is padded (never scaled) with its own border-sampled background; a dashed hairline marks where the shorter capture ends. A surface missing a subject capture gets a labeled placeholder panel so the set stays complete. The reference side defaults to the **committed `baseline/tb/`** (the same-component `<SessionDetail>` "before"); `REF_DIR=demo` is the optional non-gating design-language sanity panel (see **Oracle**). |
| `baseline/tb/{dark,light}/` | The **committed** same-component reference: an earlier `<SessionDetail>` capture of the same `sess_demo_0001` session, recorded before theme convergence. It is a **frozen, non-regenerable** snapshot, so unlike the regenerable `demo/` it is tracked in the repository. The default `stitch` reads it directly, so the oracle works on a clean checkout with no staging. |

The **subject side** (`<base>/village/<theme>/`) is the current `village-shoot.mjs` run. The optional
`demo` reference (`<base>/demo/<theme>/`) comes from the matching Fairtrade source checkout's own
`scripts/shootdemo.mjs`; the default `tb` reference ships **committed** with
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

`MOCK_TITLE_HERO_DIAGNOSTIC=1` swaps the mock's fixture to a null stored title against a first user turn
of raw harness markup, for capturing the detail-hero title/breadcrumb evidence (village#32/#33). It is
off by default so the observed-model boot assertions above are unaffected:

```sh
MOCK_TITLE_HERO_DIAGNOSTIC=1 MOCK_REST_PORT=8788 node scripts/visual/mock-rest.mjs &
```

## Explore surface gate (`cex-explore`)

The Explore gate is a separate browse-focused harness for the shared `Explore` surface:

- **Reference (left):** Fairtrade in-use demo capture `app-2-village.png` from
  the matching source checkout's `scripts/shootdemo.mjs`.
- **Subject (right):** the village explore route capture `cex-explore.png` from
  `frontend/scripts/visual/explore-shoot.mjs` (point `VILLAGE_URL` at `/explore`;
  the root route serves discovery only to a signed-out visitor).
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
NEXT_PUBLIC_API_URL=http://localhost:8789/api/v1 pnpm dev &

# 3. Capture the subject side for both themes.
CHROME_PATH=$CHROME node scripts/visual/explore-shoot.mjs dark  $BASE/village/dark
CHROME_PATH=$CHROME node scripts/visual/explore-shoot.mjs light $BASE/village/light

# 4. Stage the demo reference from a Fairtrade source checkout matching the
#    version pinned in frontend/package.json. Run these from that checkout root.
CHROME_PATH=$CHROME node scripts/shootdemo.mjs dark  $BASE/demo/dark
CHROME_PATH=$CHROME node scripts/shootdemo.mjs light $BASE/demo/light

# 5. Stitch the cex surface set (reference = demo/app-2-village, subject = cex-explore).
CHROME_PATH=$CHROME SURFACE_SET=cex node scripts/visual/stitch-sxs.mjs $BASE

# 6. Boot-arm the real route.
CHROME_PATH=$CHROME node scripts/visual/boot-explore.mjs
```

### Collapsed group of agent-driven sessions

`explore-agent-group-shoot.mjs` captures the group of sessions no person prompted:
the browse list ending in `+ N agent sessions`, and the same list with the group
open and its rows badged. Both captures come from one live page, so the pair
cannot mix two builds.

It asserts build provenance BEFORE it captures anything: the served page must
carry the collapsed group's control and a counted label, and the expanded rows
must carry the label the group promises. A stale server, or one serving another
worktree, fails with a nonzero exit instead of producing a misleading PNG. The
explore mock (`mock-rest-explore.mjs`) serves both discovery scopes — the default
list plus `agent_total`, and `origin=agent` — the way the server does.

```sh
MOCK_REST_PORT=8789 node scripts/visual/mock-rest-explore.mjs &
# The mock refuses a busy port, but backgrounding it hides that from the shell:
# wait for THIS mock to answer before booting the app, so a refused port aborts
# the round instead of handing the capture to whatever else is listening.
until curl -sf http://localhost:8789/api/v1/tags/popular >/dev/null; do
  kill -0 %1 2>/dev/null || { echo 'mock-rest-explore did not start'; exit 2; }
  sleep 0.2
done
NEXT_PUBLIC_API_URL=http://localhost:8789/api/v1 pnpm build
NEXT_PUBLIC_API_URL=http://localhost:8789/api/v1 pnpm start &

CHROME_PATH=$CHROME node scripts/visual/explore-agent-group-shoot.mjs dark  $BASE/dark
CHROME_PATH=$CHROME node scripts/visual/explore-agent-group-shoot.mjs light $BASE/light
```

Capture into `review-capture/` (gitignored). Per-round proof PNGs are never
committed.

Manual click-through checklist for the non-automated part of the gate:

1. Search for `ai`; wait for the debounce, then confirm `AI Research Team` and `Verified Contributors` appear in the collective strip.
2. Toggle provider `claude-code` and topic `greenfield`; clear filters and confirm transcript `d41a8e` returns.
3. Switch between grid and list.
4. Open transcript `d41a8e`, confirm the route changes to `/transcripts/d41a8e`, then open profile `alice-dev` and confirm `/users/alice-dev`.
5. Search `ai` again, open `AI Research Team`, confirm `/groups/ai-research-team`, then back out and confirm the shell still renders the card grid.

### Sessions started by another session

`child-session-shoot.mjs` captures the one design on all three surfaces it
reaches, because the three answers only make sense read together:

- `explore` — the started sessions are folded away and the grid keeps the parent
  card alone. There is NO control to reveal them: a browse card names no parent,
  so a count hanging off one would ask a visitor to guess whose it was.
- `home` — the recent-sessions list hangs an expandable chip off the row that
  started them, collapsed and expanded.
- `project` — the same chip on `/users/{username}/projects/{projectHash}`.

Each surface asserts its own build provenance BEFORE it captures anything, read
from the live DOM. Discovery must have the started rows off the grid, must still
browse the mock's unmatched row (`c1a099`), whose parent that response does not
carry, must render NO control, and must show a count equal to the cards under
it. The other two must carry a chip with a counted label that sits in the SAME
list unit as its own parent's row — what a chip belongs to is a fact about the
DOM, not something a reader is asked to infer from the order. A stale server, or
one serving another worktree, fails with a nonzero exit instead of producing a
misleading PNG.

The mocks serve the rows the way the server does, each parent and the sessions
it started arriving in one response carrying `parent_session_id`:
`mock-rest-explore.mjs` for discovery, and `mock-rest-home.mjs` with
`MOCK_CHILD_SESSIONS=1` for home and the project page it links to. Both refuse a
busy `MOCK_REST_PORT` with exit 2 rather than dying on an unhandled `error`
event, because a stale copy of itself on that port serves an older fixture set
that still passes every provenance check.

Every surface also asserts **capture geometry**, which is what the provenance
checks cannot see. The app header is `position: fixed`, so
`captureBeyondViewport` rasters it at whatever scroll offset the page happens to
hold; a capture taken after scrolling a control into view paints the nav across
the middle of the image and cuts the bottom of the list. Every shot is therefore
taken with the viewport grown to the whole document and the page parked at
scroll 0, and each raster is bracketed by an assertion on the scroll offset, the
viewport fit, and the clip box's coverage of the document. Note that the
header's own rect top cannot fail on its own — a fixed element reads 0 at any
offset — so the scroll offset and the fit are the load-bearing checks.

```sh
MOCK_REST_PORT=8789 node scripts/visual/mock-rest-explore.mjs &
# The mock refuses a busy port, but backgrounding it hides that from the shell:
# wait for THIS mock to answer before booting the app, so a refused port aborts
# the round instead of handing the capture to whatever else is listening.
until curl -sf http://localhost:8789/api/v1/tags/popular >/dev/null; do
  kill -0 %1 2>/dev/null || { echo 'mock-rest-explore did not start'; exit 2; }
  sleep 0.2
done
NEXT_PUBLIC_API_URL=http://localhost:8789/api/v1 pnpm build
NEXT_PUBLIC_API_URL=http://localhost:8789/api/v1 pnpm start &

VILLAGE_URL=http://localhost:3000 CHROME_PATH=$CHROME \
  node scripts/visual/child-session-shoot.mjs explore dark  $BASE/explore/dark
VILLAGE_URL=http://localhost:3000 CHROME_PATH=$CHROME \
  node scripts/visual/child-session-shoot.mjs explore light $BASE/explore/light

# home and the project page read the owner-scoped mock instead. It listens on
# the SAME port, so the app keeps the API URL it was built with: stop the
# discovery mock first.
MOCK_REST_PORT=8789 MOCK_CHILD_SESSIONS=1 node scripts/visual/mock-rest-home.mjs &
until curl -sf http://localhost:8789/api/v1/auth/me >/dev/null; do sleep 0.2; done

for surface in home project; do
  for theme in dark light; do
    VILLAGE_URL=http://localhost:3000 CHROME_PATH=$CHROME \
      PROJECT_PATH=/users/alice-dev/projects/$(printf '1%.0s' {1..64}) \
      node scripts/visual/child-session-shoot.mjs $surface $theme $BASE/$surface/$theme
  done
done
```

## Own-profile contributed-collectives gate (`profile-collectives`)

`profile-collectives-shoot.mjs` captures the own-profile contributed-collectives
section on the REAL profile route, in three states taken from one live page so
they cannot mix builds: the section with every contributed collective and its
counters, one collective open with its submissions listed, and one submission
open with its full event history visible.

It asserts build provenance BEFORE writing any PNG. The served page must carry
the section, more than one contributed collective row, a collective with zero
approved contributions and some awaiting review, and the units sentence above
the counters stating that rejected and withdrawn count submission attempts
(not transcripts). Each of those exists only in the change this gate covers,
so a stale server or one serving another worktree fails with a nonzero exit
instead of producing a misleading capture. (Per-counter unit FOOTERS were
removed at the user's explicit request; the distinction now lives only in
that one sentence, which is what this gate checks.)

Set `NARROW=1` to capture at a ~390px mobile viewport instead of the default
desktop one, appending `-narrow` to every filename so a narrow run cannot
overwrite a desktop capture sharing the same outdir.

It also reads computed styles from the live DOM and prints them, because a
scaled PNG cannot tell two close token values apart. The reported values are the
counter's font family (prose vs mono), font size, `font-variant-numeric`
(tabular figures are load-bearing where three counters sit side by side), colour,
border radius and line height.

`mock-rest-profile.mjs` is the matching REST stand-in. It serves `/auth/me` as
the profile's own owner (so the page decides the viewer is the owner through the
production code path, not through a stub), the public profile, an empty library
list, the three-counter contributions list, the owner's submissions per
collective, and a share-event history containing all five states so the actor
labels are visible in the capture.

```sh
CHROME=/path/to/google-chrome
BASE=/abs/path/to/capture-base

MOCK_REST_PORT=8790 node scripts/visual/mock-rest-profile.mjs &
NEXT_PUBLIC_API_URL=http://localhost:8790/api/v1 pnpm build
NEXT_PUBLIC_API_URL=http://localhost:8790/api/v1 pnpm start &

CHROME_PATH=$CHROME node scripts/visual/profile-collectives-shoot.mjs dark  $BASE/dark
CHROME_PATH=$CHROME node scripts/visual/profile-collectives-shoot.mjs light $BASE/light

# narrow (~390px) captures, into the SAME outdirs — filenames get a -narrow suffix
CHROME_PATH=$CHROME NARROW=1 node scripts/visual/profile-collectives-shoot.mjs dark  $BASE/dark
CHROME_PATH=$CHROME NARROW=1 node scripts/visual/profile-collectives-shoot.mjs light $BASE/light
```

Capture into `review-capture/` (gitignored). Per-round proof PNGs are never
committed.

## Project-page gate (`vpp-*`)

The project page (`/users/{username}/projects/{projectHash}`) is captured by its own
pair of scripts, because it is the only surface whose content depends on WHO is
looking: the owner-only correction control, and a collectives roll-up that is
gated by collective visibility and the owner's contributor opt-in.

| Script | Role |
|---|---|
| `mock-rest-project.mjs` | REST stand-in for the project page. Serves the page payload and the two hash-keyed correction routes LIVE, so a reset is a real round-trip rather than a second frozen fixture. `MOCK_PROJECT_VIEWER=owner\|other\|anon` chooses who is looking; `MOCK_PROJECT_ROLLUP=empty` serves an empty roll-up; `MOCK_PROJECT_IDENTITY=path` serves a project with NO git remote and no chosen or disclosed name, so its name is the redacted local path its publisher recorded. |
| `project-page-shoot.mjs` | Captures the page per theme and asserts build provenance BEFORE writing any PNG: the served page must carry the project heading, the repository-label subtitle in `host:owner/repo` shape, and the correction control, and `VILLAGE_URL` must be the 64-hex hash-keyed route. `PROJECT_SHOOT_MODE=path` captures the path-named project instead: it requires the heading to carry the redacted-path shape, requires NO repository subtitle (there is no repository to label), and requires the correction control to explain the path tier - a sentence only a build that knows the tier serves, which is what makes a stale server fail instead of producing a misleading PNG. It also prints a `getComputedStyle` probe (tokens, fonts, radius, tabular numerals, text-transform), because `--surface` vs `--canvas` and `--ink-2` vs `--ink-3` cannot be told apart in a scaled PNG. |

`PROJECT_SHOOT_MODE` selects which production state is captured:

| Mode | Surfaces | Asserts |
|---|---|---|
| `owner` (default) | `vpp-project-page`, `vpp-project-rename`, `vpp-project-collectives`, `vpp-project-after-reset` | the control is present, and resetting the name really changes both the name and the tier it is resolved from |
| `viewer` | `vpp-project-page-viewer`, `vpp-project-collectives-empty` | the control is ABSENT for a viewer who is not the owner, and an empty roll-up renders as an ordinary empty state |
| `notfound` | `vpp-project-not-found` | one refusal panel, and NO project heading beside it |
| `profile` | `vpp-profile-projects` | the profile page whose project cards link into the project page. Point `VILLAGE_URL` at `/users/{username}`. The card heading renders a project's display name, which is USER CONTENT, so the mode refuses to capture unless the served name carries a capital, and it fails if the heading (or the profile display-name heading) computes a `text-transform` other than `none`. The design system lowercases `h1`/`h2`/`h3` as chrome, so an all-lowercase fixture could not tell a correct page from a broken one. |

Repeatable run (a real production build, not `next dev`, so the served bytes are
the bytes under review):

```sh
CHROME=/path/to/google-chrome
BASE=$PWD/review-capture                  # gitignored; per-round PNGs are never committed
HASH=a3f1c07d5b9e42618c0d7f4a2b6e8901d3c5a7f9b1e2d4c6a8f0b2d4e6f80123

# 1. serve the project fixtures
MOCK_REST_PORT=8790 node scripts/visual/mock-rest-project.mjs &

# 2. build + start the app against that API base (NEXT_PUBLIC_API_URL is baked at build time)
NEXT_PUBLIC_API_URL=http://localhost:8790/api/v1 pnpm build
NEXT_PUBLIC_API_URL=http://localhost:8790/api/v1 pnpm start &

# 3. confirm the served bundle is THIS build before trusting a capture
curl -s "http://localhost:3000/users/alice-dev/projects/$HASH" \
  | grep -oE 'src="[^"]*\.js"' | sed 's/src="//;s/"//' | sort -u \
  | xargs -I{} sh -c 'curl -s "http://localhost:3000{}" | grep -l "project-rename-control" >/dev/null && echo "provenance: {}"'

# 4. capture the owner's view in both themes
CHROME_PATH=$CHROME VILLAGE_URL="http://localhost:3000/users/alice-dev/projects/$HASH" \
  node scripts/visual/project-page-shoot.mjs dark  $BASE/dark
CHROME_PATH=$CHROME VILLAGE_URL="http://localhost:3000/users/alice-dev/projects/$HASH" \
  node scripts/visual/project-page-shoot.mjs light $BASE/light

# 4a. narrow (~390px) captures, into the SAME outdirs — filenames get a -narrow suffix
CHROME_PATH=$CHROME NARROW=1 VILLAGE_URL="http://localhost:3000/users/alice-dev/projects/$HASH" \
  node scripts/visual/project-page-shoot.mjs dark  $BASE/dark
CHROME_PATH=$CHROME NARROW=1 VILLAGE_URL="http://localhost:3000/users/alice-dev/projects/$HASH" \
  node scripts/visual/project-page-shoot.mjs light $BASE/light
```

```sh
# 5. capture the profile page, whose project cards link into the project page
CHROME_PATH=$CHROME PROJECT_SHOOT_MODE=profile \
  VILLAGE_URL="http://localhost:3000/users/alice-dev" \
  node scripts/visual/project-page-shoot.mjs dark  $BASE/dark
CHROME_PATH=$CHROME PROJECT_SHOOT_MODE=profile \
  VILLAGE_URL="http://localhost:3000/users/alice-dev" \
  node scripts/visual/project-page-shoot.mjs light $BASE/light
```

```sh
# 6. capture the project whose name comes from its redacted local path
MOCK_PROJECT_IDENTITY=path MOCK_REST_PORT=8790 node scripts/visual/mock-rest-project.mjs &
CHROME_PATH=$CHROME PROJECT_SHOOT_MODE=path \
  VILLAGE_URL="http://localhost:3000/users/alice-dev/projects/$HASH" \
  node scripts/visual/project-page-shoot.mjs dark  $BASE/dark
CHROME_PATH=$CHROME PROJECT_SHOOT_MODE=path \
  VILLAGE_URL="http://localhost:3000/users/alice-dev/projects/$HASH" \
  node scripts/visual/project-page-shoot.mjs light $BASE/light
```

The path mode needs its OWN mock process: the identity a mock serves is chosen
at startup, and the app must be BUILT against that mock's port because
`NEXT_PUBLIC_API_URL` is baked at build time.

The owner mode consumes the override it resets, so restart the mock between
themes. The profile mode reads the same identity state, so run it before the
owner mode resets the override, or restart the mock first. For the other two states, restart the mock with
`MOCK_PROJECT_VIEWER=other MOCK_PROJECT_ROLLUP=empty` and run with
`PROJECT_SHOOT_MODE=viewer`, then point `VILLAGE_URL` at a hash the mock does not
serve and run with `PROJECT_SHOOT_MODE=notfound`.

## Signed-in home gate

The root route serves the signed-in person's own home page, so its capture
needs a backend that answers `GET /auth/me`:

- **Subject:** `village-home.png` from `frontend/scripts/visual/home-shoot.mjs`,
  which asserts build provenance — the two sections in order, and project links
  keyed on the 64-character project hash — before it writes any PNG, so a stale
  or wrong-worktree server fails instead of producing a misleading capture. It
  also ASSERTS the session count's computed font family and numeric variant,
  which a scaled PNG cannot distinguish, through the same `assertComputed`
  helper all three arms use. It fails rather than reporting: a surface that
  ships unstyled must not produce a plausible-looking capture.
- **Backend:** `frontend/scripts/visual/mock-rest-home.mjs`. Set
  `MOCK_SIGNED_OUT=1` for the signed-out arm, where the same root route must
  serve discovery instead, and `MOCK_OWNER_LIST_FAILS=1` for the failure arm,
  where only the owner-scoped list request fails.
- **Blank-handle arm:** `HOME_SHOOT_MODE=no-handle` with `MOCK_BLANK_HANDLE=1`
  captures the terminal surface an account gets when it records a chosen handle
  while carrying none. Its provenance check requires the alert AND that no
  shimmer is left on the page, because the whole point of the surface is that it
  stops waiting.
- **Failure arm:** `HOME_SHOOT_MODE=failure` captures the answer the page owes
  when the owner-scoped request fails: the shared failure panel and its retry,
  and NOT the teaching empty state. Its provenance check refuses to write a PNG
  from a build that still renders "nothing published yet" on a failed request.
  The non-empty gate runs on the panel rather than the page, because this
  surface is deliberately sparse: a whole-page floor would be measuring the
  emptiness the design intends. The full page is captured beside it and its
  measurement reported.

Repeatable run:

The capture is taken against the SERVED PRODUCTION BUILD, never `pnpm dev`: the
development server paints its own overlay bubbles over the surface, which then
sit in the review artefact on top of the controls under review.

```sh
CHROME=/path/to/google-chrome

MOCK_REST_PORT=8791 node scripts/visual/mock-rest-home.mjs &
NEXT_PUBLIC_API_URL=http://localhost:8791/api/v1 pnpm build
NEXT_PUBLIC_API_URL=http://localhost:8791/api/v1 pnpm start -p 3000 &

CHROME_PATH=$CHROME VILLAGE_URL=http://localhost:3000/ \
  node scripts/visual/home-shoot.mjs dark /tmp/village-home-dark
CHROME_PATH=$CHROME VILLAGE_URL=http://localhost:3000/ \
  node scripts/visual/home-shoot.mjs light /tmp/village-home-light
```

For the failure arm, restart the mock with `MOCK_OWNER_LIST_FAILS=1` on the same
port the build was pointed at, then:

```sh
CHROME_PATH=$CHROME VILLAGE_URL=http://localhost:3000/ HOME_SHOOT_MODE=failure \
  node scripts/visual/home-shoot.mjs dark /tmp/village-home-failure-dark
CHROME_PATH=$CHROME VILLAGE_URL=http://localhost:3000/ HOME_SHOOT_MODE=failure \
  node scripts/visual/home-shoot.mjs light /tmp/village-home-failure-light
```
