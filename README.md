# Village mounted visual review

Exact source: `6e53455ba8ac5570b37730a58aca75be57050a7c`, tree `658f74b3a8929b251a06e6a55ee59e5c8f792ce8`, based on develop `3982cbf7d0a573cc5951dfdd88db73d2811f477b`. Installed official registry package `@peasant-labs/fairtrade@0.0.56`; clean frozen install and normal production build. Build ID `6mgPih5pXFfq1R7GxZ5Bd`. The standalone server on3344 resolved this exact checkout; served asset hashes and banner marker are recorded in `proof/village-full-review-056-provenance.json`.

All data is synthetic. Existing populated preview services and in-memory state were retained.

- 78 distinct mounted states,310 theme/viewport observations: light/dark at1440×1000 and390×1000, except canonical desktop-only zen mode. These are state combinations, not78unique routes.
- Every primary capture was visually inspected using the78four-view sheets in `inspection/`. All118full-page images were reviewed:106unique hashes, with142below-fold segments across24inspection sheets in `tails/`; exact duplicate manifest included.
- Six additional scroll observations produce14images: mobile dialog access choices, plus native16px Mermaid vertical scrolling and horizontal panning at both sizes/themes. These supplement the310primary observations; they are not additional unique route claims.
- All310 primary observations passed readiness, no document horizontal overflow, and their applicable viewer gap/error assertions. Critical prose/wordless/PR-header/zen viewer cases retain zero hidden center scroll and ≤1px stream/footer separation.
-37 unique visible alias/canonical transcript exits mounted the real viewer with matching title/nonempty content and no API/browser errors. Nine retained redirect routes were checked separately.

The route/state inventory and individual actual responses, expected errors, readiness and geometry are in `proof/`. Ordinary pages include long lists, settings, disclosures, history, pagination, all collective settings tabs, contribution/review preview, permission/empty/error/loading states, welcome, privacy, unsubscribe, retained explore and not-found routes. The visual-harness route is correctly not-found in production.

## Findings fixed

Agent-only Home notice uses two semantic paragraphs with deliberate spacing. Project summary aligns to the profile's flat summary row; contribution empty state has a useful heading. Transcript metadata, profile, project and published-library failed reads now offer actual retry rather than claiming a missing/empty record;404 behavior is retained. Official Fairtrade56 fixes the canonical initial-turn hidden-ancestor scrolling and HandleClaim helper measure/copy without consumer styling forks.

## Checks and limits

Frozen install, production build, TypeScript and ESLint passed (lint retains two pre-existing unused-import warnings). Frontend tests:1279pass; three existing process-lock tests fail on macOS because util-linux `flock` is unavailable. All mutation commands passed separately, including all18 grouped-helper mutants. Linux CI must decide those process tests; they were not skipped or weakened.

No all-accessibility pass is claimed: canonical Welcome HandleClaim has no h1 override, recorded for canonical follow-up; no duplicate visible title was added. Firefox headless could not launch in this macOS environment (SWGL framebuffer mapping error/timeouts), so final browser evidence is Chromium. Global/error boundary code was reviewed without claiming a forced mounted crash. Transient auth routes were checked through actual redirects/source rather than screenshots after navigation had unloaded them.

Two initial phone project-owner capture setups waited for hidden collapsed-sheet controls. Their failed setup results are retained locally. Final captures open the real project-settings sheet before waiting; no production source or gate assertion was weakened.

## Delivered

[PR352](https://github.com/peasant-labs/village/pull/352) merged after every CI check passed, including the full Linux frontend test/mutation suite and backend race integration aggregate. Actual merge `6b34ad17b6369527f5ecce926a9aa6003d99b07b` has exactly the reviewed tree. Clean develop was synced, frozen-installed and normally built before replacing the owned3310frontend with a detached production standalone process. The original API/proxy processes and synthetic state were preserved.

Postmerge proof is in `postmerge/`:22critical banner/viewer observations passed and their images were inspected;37actual alias exits and all synthetic contribution/history checks passed again. The original310-state-combination review was not unnecessarily repeated against an identical source tree. Railway deployment7439536691 succeeded for the actual mergeSHA; public callback returned200,22of23observed assets match local output, and the freshly fetched bannerCSS matches exactly. One JavaScript asset differs under the public/local API configurations, so no all-assets equality is claimed.
