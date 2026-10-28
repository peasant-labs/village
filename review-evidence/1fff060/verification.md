# Village published Fairtrade adoption — implementation handoff

## Frozen source

- Worktree: `/home/minttea/dev/peasant-labs/village/peasant-770--release--fairtrade-transcript-adoption`
- Branch: `peasant-770--release--fairtrade-transcript-adoption`
- Base: `5a571c7219ba0ca1c6cae9c193c4bad22c2ac680`
- Local commit: `1fff06081bed9bf180af3679aa3aabd2d292df34` (`chore(deps): adopt published Fairtrade 0.0.48`), made with `git agent-commit`.
- Changed files ONLY: `frontend/package.json`, `pnpm-lock.yaml`; six insertions/six deletions. No version/release/changelog, schema, backend, application, fixture or harness changes.
- Tree is clean. Nothing pushed; no PR/tag/merge/task closure. Frozen pending supervisor review (available for fixes).

The live Village remote is **canonical**, `git@github.com:peasant-labs/village.git`; **origin is the prerelease archive**. The preflight handoff's remote/base/schema facts were stale. Default was clean; fetched canonical, checked fast-forward against canonical/develop, then ran normal `make build` before creating the feature worktree. PR325 had already landed in that base; existing Schema0.35.0 remains untouched. PR330 is still open and overlaps the transcript adoption but is not a prerequisite. Fresh open-PR list: `open-prs.json` alongside this handoff.

## Published package

- Exact `@peasant-labs/fairtrade` **0.0.48**, registry resolution only.
- SRI: `sha512-z3NjuhSETH89pTBwe7RrFAPTdFT0vz5bXefdSQt9f8yaAhJMj/RWD9s1oro93sm2Xy1b6bW4A7tsYw0GLvfMzg==`.
- Re-pin command: `pnpm --dir frontend add --save-exact @peasant-labs/fairtrade@0.0.48`; pnpm11.24.0 regenerated the root lock. No unrelated resolution drift.
- `pnpm install --frozen-lockfile` passed. All **217** packed files of the supervisor-verified registry tarball were compared byte-for-byte with the installed package: identical. Installed package resides in this worktree's pnpm store, not a sibling source checkout/link.
- Publication receipt: `/tmp/opencode/txn-ledger/fairtrade-publication-verified.json` (registry hashes, source, npm workflow and SLSA subject/source; not an independent Sigstore signature-verifier claim).

## Completed checks and timings

Logs reside in `/tmp/opencode/txn-ledger/village-adoption/`.

| Command | Result | Wall time |
|---|---|---:|
| default checkout `make build` | pass, backend binary + frontend build | 17.739s |
| exact `pnpm ... add` | pass | 3.671s |
| `pnpm install --frozen-lockfile` | pass | 0.930s |
| `pnpm --dir frontend test` | 104 files/1175 tests pass; three pre-existing vendoring skips; mutation probes all killed | 53.303s |
| `pnpm --dir frontend exec tsc --noEmit` | pass | 5.354s |
| `pnpm --dir frontend lint` | pass, zero errors/two existing unused-import warnings | 8.559s |
| `CHROME_PATH=/home/minttea/.nix-profile/bin/google-chrome pnpm --dir frontend journey:ci` | **124/124 pass**, dark/light catalog | 258.48s |
| same browser `pnpm --dir frontend journey:collectives-built` | **2/2 pass**, production build/routes/redirects/provenance | 35.759s |
| `NEXT_PUBLIC_API_URL=http://localhost:18882/api/v1 pnpm --dir frontend build` | pass on committed head | 15.875s |
| bounded mounted capture pass | pass | 32.059s |
| visible-output confirmation/supplemental interactions | pass | 21.410s |
| mounted system filtering/clear/computed-token check | pass both themes | 2.525s |
| `impeccable detect --json frontend/package.json` | no findings (`impeccable.json`) | not separately timed |
| backend `go build ./...` | pass (extra, unchanged backend) | 3.102s |
| backend `go test -race -timeout 10m ./...` | pass; previously incomplete handler package305.216s, other packages successful/cached | 306.62s |
| backend `go vet ./...`, `gofmt -l .` | pass, no formatting output | 0.475s vet |

**Limitations/failures, not hidden:**

- The first extra backend `go test -race ./...` command exceeded the 180-second tool limit, after multiple packages passed, before the whole suite reported. Retried only that incomplete suite with `-timeout 10m`: **full suite passed**, handler package305.216s, total306.62s. No gate job remains running. No PostgreSQL/RustFS integration services or backend changes were introduced for this frontend-only adoption; backend service/integration CI still remains the supervisor's gate.
- The three frontend skips are `scripts/journey/lib/vendor-guard.test.mjs`, deliberately conditional on `FAIRTRADE_CHECKOUT`; no source-checkout-vendoring claim is made. The published runtime file comparison above is independent of that helper guard. No test was disabled or weakened.
- Lint warnings are pre-existing unused `afterEach` in `SessionDetailV2.mountedGraph.test.tsx` and unused `rowFor` in `mountedChildSessionGroupingCollective.test.tsx`.
- Local inspection initially selected both a timeline and stream Bash button; scoped the inspector to the actual stream and reran. The width inspector initially expected one-edge geometry; corrected its observation to the canonical centered two-edge geometry (73px pointer travel changes width146px). These were inspector errors, not application fixes or gate changes.
- Initial thinking captures showed output below the viewport. Replaced that state only, confirmed actual output rectangles inside the viewport, and inspected every replacement. No source rebuild or hidden element workaround.
- `/usr/bin/time` is absent; normal shell `time` was used after that setup-only command failed before building.

## Production route, artifact and visual evidence

Fresh standalone Next production server from the frozen worktree, at `http://127.0.0.1:13332`, built with the app-owned REST mock at18882. Production `/transcripts/demo` runs detail/content fetches → `useTranscript`/`useTranscriptContent` → real content parsing → `SessionDetailV2` → canonical `adaptTranscript`/`TranscriptViewer`, with the published package. The existing `sample-session.ts` data is supplied as a controlled REST content response; **the dev fixture route is never the proof target**. Auth/metadata and other reads use the app-owned mock. The signed-in non-owner host's real markdown menu action was clicked and emitted the download in all four theme/viewport combinations.

Reference: existing server15359, untouched, reviewed source29670f1. Its tree `1d6bab0b0d53e2f7db7fdd4525995d92b920c508` equals release merge5a2f9f3's tree (independently read through GitHub's commit API). All loaded JS/CSS responses were hash-compared with the respective owned `.next/static` or demo `dist` files:148 recorded responses. The served Village chunk `/_next/static/chunks/7f2a7ad02d54de96.js`, SHA256 `ccd14f2866a47becafadcf2a2d31ff20ff0a7d26b3c4be789aec9d43f6608078`, contains `txn-rail-title`, `txn-sticky-disclosure`, `txn-modelactions`, `txn-thinking-box`; each marker is absent from default's installed0.0.47 and present in0.0.48.

Build ID, source/base, lock hash, assets, release receipt, interactions, output geometry and computed styles: `captures/provenance.json`. System-filter readings: `system.json`. Reference contains newer prose/table/phase metadata; app uses its existing fixture, stored title, observed-model/label slots and own shell. **No identical-data/zero-pixel-parity claim.** Shared measured style properties match (55 in reading,66 with Think open), with Next's local Atkinson font aliases reported separately. Body16px/24px, mono chrome14px/21px, square borders, action-row padding, Think box border/inset and200ms canonical grid easing match; actual outline padding0 and tabular counts verified. Both actual root and viewer themes asserted. Phone is Chromium-emulated390×844, desktop1440×1000; normal motion. No physical-device or multi-touch gesture claim.

**All16 final captures inspected** (full persistent shell plus mounted body; Think output visible in its own box):

`/tmp/opencode/txn-ledger/village-adoption/captures/{fairtrade,village}-{dark,light}-{desktop,phone}-{reading,thinking}.png`

No durable screenshot branch was created/pushed: supervisor owns that upload and PR embedding. Do not present local paths alone as completed PR screenshot publishing.

## Standing regression walk

- Two user-turn groups, seven source tools and two Think rows once each; groups expanded/contents closed initially; no redundant call-count label. First prose precedes its action list. Actual mounted action rows inspected.
- Boxed Think33words (duration absent in source remains absent), consistent Read/Think/Grep geometry, no expanded-header divider; Bash input/$ and stdout opened on real stream button.
- Compact closes/reopens both lists. An already-open Think child survives parent collapse/reopen; normal-motion disclosure exercised on desktop and phone.
- Click selects actual card; rapid bottom overshoot reverses8→7, top overshoot reverses0→1. Sidebar collapse/reopen and zen round trip work. Width retained arbitrary146px two-edge reduction, then40px keyboard change when sufficient space exists; with both Village rails open the center is too narrow to adjust, so earlier559→559 was clamping, not proof of adjustment.
- System row from the released demo's synthetic ledger-system example transported through controlled REST: visible initially, actually absent after filtering, returned by clear; correct count1. Canonical source uses `system !== false`/`system === false`, so older absent property stays visible (source inspection, not a new persisted-state browser test).
- Search, every alternate tab, and app's real graph slot exercised; commit marker present in mounted sample. All124 existing journeys cover retained owner more/edit/manage-access/pull-list/clipboard/navigation flows; real non-owner download also checked on committed production build. No host code removed or replaced.
- **Not newly proven:** fully overflowing-rail follow coverage and interrupted-animation accessibility timeline remain the user's explicit Fairtrade deferrals. Normal motion was exercised, not a new frame-by-frame no-jiggle/flash proof. No claim that a mock REST run validates PostgreSQL/storage, OAuth or actual publication/redaction services. No expanded harness/matrix or production test seam was added.

Impeccable context/harden/audit applied within incumbent Fairtrade authority. Context launcher found no product record in the frontend root; read workspace `polyrepo/PRODUCT.md` directly. Narrow dependency adoption, no new visual world. Scoped audit found no adoption regression; existing catalog axe checks pass. Do not interpret the manifest detector's empty result as a whole-app WCAG audit.

## Supervisor next steps

Read `pr-body.md`, upload inspected images durably, push the local commit to **canonical**, create the dependency-only PR againstdevelop, obtain current-head independent review and CI. All assigned local gates are complete; the initial broad backend timeout was resolved by the passing bounded retry. No implementation blocker. Do not adopt PR330 or change Schema/version metadata to finish this adoption. Worker available for up to three review/fix rounds; source is frozen now. Intentional review servers remain on13332 (standalone Next, PID789523) and18882 (app-owned REST mock, PID785948), with no gate jobs outstanding; canonical demo15359 is untouched.
