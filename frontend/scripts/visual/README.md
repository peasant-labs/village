# Visual capture harness - Village frontend

Village's transcript path uses the released canonical composition:

```text
REST SessionDetailPayload
  -> React Query
  -> SessionDetailV2
  -> Fairtrade adaptTranscript
  -> Fairtrade TranscriptViewer
  -> transcript-browser TrajectoryGraph in the graph slot
```

Village does not derive sticky model state. `observedModel` omissions stay absent
on the wire, and Fairtrade resolves effective model carry-forward before display
projection.

## Mounted production-route evidence

`mock-rest.mjs` reads the strict
`src/testdata/final-contract-compatibility.yaml` case named
`sticky-observed-model-transition`. It serves source observations A, omission,
B, omission. `probe-village.mjs` and `boot-village.mjs` independently require:

- effective models A, A, B, B;
- exactly one `model changed: A -> B` marker;
- the requested dark or light theme on both the document and viewer;
- Atkinson Hyperlegible prose and Atkinson Hyperlegible Mono model chrome;
- Village's persistent product header plus the mounted transcript body;
- no blank capture, through `SurfaceGate`.

The evidence must come from a production standalone build. A fixture-route-only
capture is insufficient.

```sh
ROOT=/absolute/path/to/village
FRONTEND=$ROOT/frontend
CHROME=/path/to/chrome
CAPTURES=/tmp/village-observed-model

# Build with the mock API origin compiled into browser JavaScript.
NEXT_PUBLIC_API_URL=http://127.0.0.1:8788/api/v1 pnpm --dir "$FRONTEND" build

# Prepare Next's standalone tree after the build.
mkdir -p "$FRONTEND/.next/standalone/frontend/.next/static" \
  "$FRONTEND/.next/standalone/frontend/public"
cp -R "$FRONTEND/.next/static/." "$FRONTEND/.next/standalone/frontend/.next/static/"
cp -R "$FRONTEND/public/." "$FRONTEND/.next/standalone/frontend/public/"

MOCK_REST_PORT=8788 node "$FRONTEND/scripts/visual/mock-rest.mjs" &
PORT=3000 HOSTNAME=127.0.0.1 \
  node "$FRONTEND/.next/standalone/frontend/server.js" &

CHROME_PATH="$CHROME" VILLAGE_THEME=dark \
  node "$FRONTEND/scripts/visual/probe-village.mjs"
CHROME_PATH="$CHROME" VILLAGE_THEME=light \
  node "$FRONTEND/scripts/visual/probe-village.mjs"

CHROME_PATH="$CHROME" VILLAGE_THEME=dark VILLAGE_CAPTURE_DIR="$CAPTURES" \
  node "$FRONTEND/scripts/visual/boot-village.mjs"
CHROME_PATH="$CHROME" VILLAGE_THEME=light VILLAGE_CAPTURE_DIR="$CAPTURES" \
  node "$FRONTEND/scripts/visual/boot-village.mjs"
```

Inspect the generated standalone tree before launching because Next may preserve
a broader workspace prefix in other build layouts. Verify build provenance before
trusting images:

1. inspect installed package manifests for Schema `0.1.1`, Fairtrade `0.0.12`,
   and transcript-browser `0.0.7`;
2. verify the lockfile carries the published integrity for all three artifacts;
3. search the built standalone JavaScript for the released marker text
   `model changed:`;
4. run both computed-style probes and inspect both PNGs.

Captures are transient and remain untracked. The supervisor uploads the inspected
exact-head images to GitHub after opening the PR.

## Broad transcript demo capture

The development-only `/dev/visual-harness` route mounts the same Fairtrade
viewer with a larger deterministic session and transcript-browser's graph
engine. It is useful for walking every transcript tab, but it does not replace
the mounted production-route evidence above.

| Script | Role |
|---|---|
| `village-shoot.mjs` | Capture highlights, scorecard, trace, scrubber, rails, labels, graph, diffs, files, and annotations in one theme. |
| `surface-gate.mjs` | Reject blank, near-empty, or duplicate captures. |
| `stitch-sxs.mjs` | Compare the current app against Fairtrade's in-use demo. `REF_DIR=demo` is the default; `REF_DIR=tb` is an explicit historical-baseline mode only. |
| `png-diff.mjs` | Pixel comparison used by the side-by-side gate. |

```sh
BASE=/tmp/village-transcript-review
CHROME_PATH="$CHROME" node scripts/visual/village-shoot.mjs dark "$BASE/village/dark"
CHROME_PATH="$CHROME" node scripts/visual/village-shoot.mjs light "$BASE/village/light"

# Stage current Fairtrade demo captures under $BASE/demo/{dark,light}, then:
CHROME_PATH="$CHROME" node scripts/visual/stitch-sxs.mjs "$BASE"
```

The Fairtrade in-use demo is the fidelity oracle. The app and demo both render
`.txn-*` markup. Transcript-browser owns graph topology and interaction, not a
second transcript composer or sticky-model state machine.

## Explore and manage surfaces

The other visual families remain separate:

- Explore: `probe-explore.mjs`, `explore-shoot.mjs`, `boot-explore.mjs`, and
  `mock-rest-explore.mjs`.
- Manage: `manage-shoot.mjs`, `manage-boot-village.mjs`, and
  `manage-stitch-sxs.mjs`.

All families require `CHROME_PATH`. `PUPPETEER_CORE` can point to an explicit
module only when the app's installed `puppeteer-core` cannot resolve normally.
