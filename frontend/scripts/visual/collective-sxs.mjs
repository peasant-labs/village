/* The collective grouped surfaces' side-by-side arms: the mapping each arm is
   judged by, where each side's capture lives, the ONE computed-style comparison
   both shoots feed, and the fail-closed checks that a composite is never built
   from a missing or blank side or from an unproven reference.

   ORACLE. The live in-use demo is the fidelity oracle. It mounts NO collective
   surface that reads the grouped helper pages: its collective detail browse is a
   flat table, its contribute tree is a flat project tree, and its review queue is
   a flat ModerationQueue. What the demo does mount is the canonical
   grouped-helper primitive demonstration, `?app=commons&helpers=<case>#inuse`
   (the named examples `src/ui/HELPER-GROUPS.md` lists). Each arm therefore pairs
   the demo's demonstration of that primitive against the village surface that
   composes it, and its `mapping` states what the pair can and cannot compare. A
   demo case that stops mounting fails `demo-helper-groups-shoot.mjs` loudly
   instead of silently comparing a different surface.

   This module is shared: the demo shoot, the app-side shoot and the stitcher all
   read the same table, the same property set and the same provenance predicates,
   and the self-check drives those same predicates. No test-only path.

   WHAT IS AND IS NOT PROVEN
     - The reference side proves an EXACT RELEASE of the demo checkout: its
       `package.json` version equals the `@peasant-labs/fairtrade` pin in this
       app, and `git describe --tags --exact-match` names exactly
       `fairtrade-v<version>`; plus the served asset's sha256, cross-checked
       against the same file inside that checkout's `dist/`. That proves "these
       served bytes are a locally built dist of the pinned release", not "these
       bytes were published": a rebuild of the same release, a rebuild at a
       dirty tree, or a hand-edited dist is outside what the check can see. When
       no checkout is available, an operator may pin the served digest directly
       (`DEMO_ASSET_SHA256`), which proves only "these served bytes are the ones
       the operator pinned" (recorded as `digest-only`).
     - The subject side proves the served app bundle carries both markers this
       change introduces, and records the served chunk's sha256.
     - Neither side proves the build was never rebuilt. The composite header and
       the harness README state which of these applies to the reference pane.

   THE STYLE COMPARISON, AND ITS EXCEPTIONS. `COMPARED_PROPERTIES` is the one
   property set, read from both sides by `measureCollectiveStyles` and compared by
   `compareStyleRecords`. A property mismatch fails the stitch. An element that
   one side does not mount is a mismatch UNLESS it is named in `STYLE_EXCEPTIONS`
   for that arm with its reason; there are exactly two, both the same
   design-system fact and both on display-only arms. Every other absence fails the
   run — including the canonical connector on the arms that select members, which
   `CONNECTOR_ARMS`/`connectorProblem` assert POSITIVELY rather than as the
   absence of an allowance. The only value normalization is the font-family alias
   named in `normalizeValue`. Element WIDTHS and wrap state are measured
   (`WRAP_ELEMENTS`) and reported, never gated: the two panes are different
   surfaces and their containers are legitimately different widths. */

import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { DEFAULT_MIN_BYTES, MIN_DISTINCT_COLORS, MIN_NONBG_RATIO } from './surface-gate.mjs'

export const THEMES = ['dark', 'light']

/* ── the arms table ───────────────────────────────────────────────────────────

   One entry per SxS arm.

   surface      the composite's name under <base>/sxs/collective/
   app          the app capture `grouped-helper-shoot.mjs` writes (full surface)
   appGrouped   the bounded grouped region that same run writes (the `-panel` file)
   demo         the demo's whole in-use surface for the arm's canonical case
   demoGrouped  the demo's `.helper-demo` demonstration element for that case
   demoCase     the `?helpers=` case the demo reference mounts
   demoState    `expanded` (disclosure open) or `selected` (open + one member ticked)
   styleScope   the container each side's style probe reads inside: the demo's
                demonstration element, and the app container that holds the owner
                row and its grouped children
   mapping      what this pair can and cannot compare, read by a human reviewer */
export const COLLECTIVE_ARMS = [
  {
    surface: 'collective-grouped-browse',
    appMarkers: ['data-helper-group-owner', 'my-contributions-panel'],
    shootSurface: 'collective-browse',
    app: 'village-collective-grouped-browse.png',
    appGrouped: 'village-collective-grouped-browse-panel.png',
    demo: 'demo-collective-grouped-browse.png',
    demoGrouped: 'demo-collective-grouped-browse-tree.png',
    demoCase: 'three-independent-counts',
    demoState: 'expanded',
    // The capture is the owner-row wrapper: `owner-helper-groups` is only the
    // group, so the parent that also holds the owner row is the bounded region
    // the mapping is about.
    appGroupedSelector: 'div:has(> [data-testid="owner-helper-groups"])',
    styleScope: { reference: '.helper-demo', subject: 'div:has(> [data-testid="owner-helper-groups"])' },
    mapping:
      'the pair judges the owner anchor, the single indent step, the count chip and the individually linked members. Two limits, stated rather than implied: the browse list is read-only, so the demo control\'s per-member checkboxes and the connector they trace have no counterpart here, and the app\'s owner row is the transcript list row it always was rather than the reference\'s generic owner row.',
  },
  {
    surface: 'collective-grouped-contribute',
    appMarkers: ['data-helper-group-owner'],
    shootSurface: 'collective-contribute',
    app: 'village-collective-contribute-grouped-helper.png',
    appGrouped: 'village-collective-contribute-grouped-helper-panel.png',
    demo: 'demo-collective-grouped-contribute.png',
    demoGrouped: 'demo-collective-grouped-contribute-tree.png',
    demoCase: 'three-independent-counts',
    demoState: 'selected',
    demoSelectThread: 'G2',
    styleScope: { reference: '.helper-demo', subject: '[data-testid="contribute-member-panel"]' },
    mapping:
      'both sides tick exactly one member. The app states `contribute 1 transcript` on the route\'s own action bar where the demo states the selected identity in its summary line, and each side keeps its own closure, so off-primitive chrome is not comparable. Both sides also trace the canonical connector between their member checkboxes; the harness asserts it (CONNECTOR_ARMS), so its absence fails the gate.',
  },
  {
    surface: 'collective-grouped-review',
    appMarkers: ['data-helper-group-owner'],
    shootSurface: 'collective-review',
    app: 'village-collective-review-grouped-helper.png',
    appGrouped: 'village-collective-review-grouped-helper-panel.png',
    demo: 'demo-collective-grouped-review.png',
    demoGrouped: 'demo-collective-grouped-review-tree.png',
    demoCase: 'trunk-replacement',
    demoState: 'selected',
    demoSelectThread: 'G4',
    styleScope: { reference: '.helper-demo', subject: '[data-testid="review-panel"]' },
    mapping:
      'the review queue selects one identity, never a group. The reference is the named example whose group holds two members carrying the same title and a third that does not, so a tick visibly names ONE row; the app states `1 selected` on its decision bar where the demo states the identity in its summary line. As on contribute, both sides trace the canonical connector, which the harness asserts (CONNECTOR_ARMS).',
  },
  {
    surface: 'collective-grouped-my-shares',
    appMarkers: ['data-helper-group-owner', 'my-contributions-panel'],
    shootSurface: 'collective-my-shares',
    app: 'village-collective-my-shares.png',
    appGrouped: 'village-collective-my-shares-panel.png',
    demo: 'demo-collective-grouped-my-shares.png',
    demoGrouped: 'demo-collective-grouped-my-shares-tree.png',
    demoCase: 'ordinary-child-exit',
    demoState: 'expanded',
    styleScope: { reference: '.helper-demo', subject: '[data-testid="my-contributions-panel"]' },
    mapping:
      'the panel keeps the contribution row it always drew (its transcript link, its pending state, its unshare control, its count) and hangs the grouped disclosure beneath that row, which is the reference example whose owner row keeps its own ordinary exit beside its grouped children. The panel\'s grouped read is display-only: its members are individually linked and carry no per-member tick and no traced connector, which the reference example does draw.',
  },
]

/* The four arms this gate must always carry. `git` cannot delete a case from a
   fixture file, so the required-NAME manifest in the self-check fixture pins
   them; this list is the same manifest in code. */
export const REQUIRED_ARM_SURFACES = [
  'collective-grouped-browse',
  'collective-grouped-contribute',
  'collective-grouped-review',
  'collective-grouped-my-shares',
]

export const demoDir = (base, theme) => `${base}/demo/collective/${theme}`
export const appDir = (base, theme) => `${base}/village/collective/${theme}`

/* Both sides of the full-surface composite for one arm + theme. */
export const sidePaths = (base, theme, arm) => ({
  reference: `${demoDir(base, theme)}/${arm.demo}`,
  subject: `${appDir(base, theme)}/${arm.app}`,
})

/* Both sides of the grouped-region composite. */
export const groupedSidePaths = (base, theme, arm) => ({
  reference: `${demoDir(base, theme)}/${arm.demoGrouped}`,
  subject: `${appDir(base, theme)}/${arm.appGrouped}`,
})

/* ── the style comparison ─────────────────────────────────────────────────── */

/* ONE property set, read from both sides by `measureCollectiveStyles`, so the two
   shoots cannot drift apart. Every property here is state-independent: painted
   colours are deliberately NOT compared because they move with the focus state an
   arm ends in, and the captures carry them for the human read instead. */
export const COMPARED_PROPERTIES = {
  trigger: {
    selector: '.helper-group-trigger',
    properties: ['fontFamily', 'fontSize', 'borderRadius', 'minHeight', 'textTransform', 'paddingLeft'],
  },
  count: {
    selector: '.helper-group-count',
    properties: ['fontFamily', 'fontSize', 'fontVariantNumeric', 'textTransform', 'borderRadius'],
  },
  facts: {
    selector: '.helper-thread-facts',
    properties: ['fontFamily', 'fontSize', 'fontVariantNumeric', 'whiteSpace'],
  },
  title: {
    selector: '.helper-thread-open, .helper-thread-title',
    properties: ['fontFamily', 'fontSize', 'textTransform', 'whiteSpace', 'textOverflow', 'lineHeight'],
  },
  rail: {
    selector: '.helper-tree-rail__path',
    properties: ['stroke', 'strokeWidth'],
  },
}

/* Elements whose ABSENCE on one side is expected, named per arm WITH the reason.
   Exactly two, and both are the same design-system fact: a display-only grouped
   read mounts no checkbox, so no connector is traced. Anything else missing is a
   mismatch, never a silent skip. */
export const STYLE_EXCEPTIONS = [
  {
    arm: 'collective-grouped-browse',
    element: 'rail',
    reason:
      'the browse list is a display-only grouped read (no per-member selection callback), so no checkbox is mounted and the design system traces no connector (HELPER-GROUPS.md)',
  },
  {
    arm: 'collective-grouped-my-shares',
    element: 'rail',
    reason:
      'the contributions panel is a display-only grouped read, so no checkbox is mounted and no connector is traced',
  },
]
/* The arms that DO select members: each mounts per-member checkboxes, so each
   must compose the design system's helper tree, which owns the traced connector
   (`.helper-tree-rail__path`). Named here so the connector is asserted POSITIVELY
   — a present element on both sides — rather than as the absence of an allowance;
   the self-check pins this list by NAME like the arm manifest, so an arm cannot
   drop its assertion silently. The display-only arms are the opposite case and
   are covered by `STYLE_EXCEPTIONS` instead. */
export const CONNECTOR_ARMS = ['collective-grouped-contribute', 'collective-grouped-review']

/* Null when every connector arm mounts the canonical connector on BOTH sides,
   else the actionable reason the stitch must stop. A display-only arm is not in
   `CONNECTOR_ARMS` and is never failed here. */
export const connectorProblem = ({ arm, reference, subject }) => {
  if (!CONNECTOR_ARMS.includes(arm)) return null
  const ref = reference?.elements?.rail ?? null
  const sub = subject?.elements?.rail ?? null
  if (ref != null && sub != null) return null
  const missingOn =
    ref == null && sub == null ? 'both sides' : ref == null ? 'the reference side' : 'the subject side'
  return (
    `ERROR [collective-stitch-sxs] arm "${arm}" does not mount the canonical helper connector.\n` +
    `  What failed: the traced connector (${COMPARED_PROPERTIES.rail.selector}) is absent on ${missingOn}.\n` +
    `  Why: this arm mounts per-member checkboxes, and the design system traces the connector only when those checkboxes sit inside its helper-tree composition.\n` +
    `  Where: collective-sxs.mjs CONNECTOR_ARMS + COMPARED_PROPERTIES.rail, read on both sides by measureCollectiveStyles.\n` +
    `  Means: the pair would be composed without the canonical connector this arm claims to compare.\n` +
    `  Fix: compose the owner row through the design system's helper tree; if the arm really is display-only, move it to STYLE_EXCEPTIONS with its reason and out of CONNECTOR_ARMS.\n`
  )
}

/* The named exception an arm carries for an element, or null. An absence with no
   exception is a mismatch (fail closed); there is no third, recorded-but-allowed
   category. */
export const exceptionFor = (arm, element) =>
  STYLE_EXCEPTIONS.find((entry) => entry.arm === arm && entry.element === element) ?? null

/* Element WIDTHS and wrap state are measured and reported, never gated: the panes
   are different surfaces whose containers are legitimately different widths, and
   the finding this answers is that a wrap claim must be measured, not eyeballed. */
export const WRAP_ELEMENTS = { count: '.helper-group-count', facts: '.helper-thread-facts' }

/* The ONLY value normalization applied anywhere: the app resolves the design
   system's families through Next's font alias, so the first family name is spelled
   `atkinsonHyperlegibleMono` there and `"Atkinson Hyperlegible Mono"` in the demo.
   Lowercasing and dropping the whitespace/quotes makes those two equal while still
   failing on a genuinely different family (e.g. a fallback or a brand font). */
export const normalizeValue = (property, value) => {
  const text = String(value ?? '')
  if (property !== 'fontFamily') return text
  return text.split(',')[0].trim().replace(/^["']|["']$/g, '').toLowerCase().replace(/\s+/g, '')
}

/* Compare one arm's readings. `reference` and `subject` are the records
   `measureCollectiveStyles` produced. Returns the number of properties actually
   compared, the mismatches (each named), and the named exceptions that applied. */
export const compareStyleRecords = ({ arm, reference, subject }) => {
  const mismatches = []
  const exceptions = []
  let compared = 0
  for (const [element, spec] of Object.entries(COMPARED_PROPERTIES)) {
    const ref = reference?.elements?.[element] ?? null
    const sub = subject?.elements?.[element] ?? null
    if (ref == null || sub == null) {
      const expected = exceptionFor(arm, element)
      if (expected) {
        exceptions.push({
          element,
          missingOn: ref == null && sub == null ? 'both sides' : ref == null ? 'the reference side' : 'the subject side',
          reason: expected.reason,
        })
      } else {
        mismatches.push({
          element,
          property: '(element not mounted)',
          reference: ref == null ? null : 'mounted',
          subject: sub == null ? null : 'mounted',
          reason: 'one side does not mount this element and no named exception covers it',
        })
      }
      continue
    }
    for (const property of spec.properties) {
      compared++
      const a = normalizeValue(property, ref[property])
      const b = normalizeValue(property, sub[property])
      if (a !== b) mismatches.push({ element, property, reference: ref[property], subject: sub[property] })
    }
  }
  return { compared, mismatches, exceptions }
}

/* One prose line for a composite header and the run log: what was compared and
   which named exceptions applied. */
export const describeStyleCheck = ({ compared, mismatches, exceptions }) =>
  `styles · ${compared} properties compared on both sides · ${mismatches.length} mismatch(es) · ` +
  (exceptions.length
    ? `${exceptions.length} named exception(s): ${exceptions.map((e) => `${e.element} absent on ${e.missingOn}`).join(', ')}`
    : 'no named exception applied')

/* Read the shared property set + the wrap measurements from the live page inside
   `scope` (the arm's container on the side being measured). Both shoots call this
   one function, so the readings are shaped identically by construction. */
export const measureCollectiveStyles = (page, scope) =>
  page.evaluate((compared, wrapElements, scopeSelector) => {
    const root = document.querySelector(scopeSelector)
    if (!root) return { scope: scopeSelector, missingScope: true, elements: {}, wrap: {} }
    const read = (selector, properties) => {
      const el = root.querySelector(selector)
      if (!el) return null
      const style = getComputedStyle(el)
      return Object.fromEntries(properties.map((property) => [property, style[property]]))
    }
    const wrapOf = (el) => {
      const style = getComputedStyle(el)
      const lineHeight = parseFloat(style.lineHeight) || 0
      const rects = el.getClientRects().length || 1
      const byHeight = lineHeight > 0 ? Math.max(1, Math.round(el.getBoundingClientRect().height / lineHeight)) : 1
      const lines = Math.max(rects, byHeight)
      return {
        clientWidth: el.clientWidth,
        scrollWidth: el.scrollWidth,
        height: Math.round(el.getBoundingClientRect().height),
        lineHeight,
        lines,
        wraps: lines > 1,
      }
    }
    return {
      scope: scopeSelector,
      elements: Object.fromEntries(
        Object.entries(compared).map(([element, spec]) => [element, read(spec.selector, spec.properties)]),
      ),
      wrap: Object.fromEntries(
        Object.entries(wrapElements).map(([element, selector]) => {
          const el = root.querySelector(selector)
          return [element, el ? wrapOf(el) : null]
        }),
      ),
    }
  }, COMPARED_PROPERTIES, WRAP_ELEMENTS, scope)

/* One prose line for the measured wrap state, per side, for the composite header
   and the log. A null reading means the element is not mounted on that side. */
export const describeWrap = (wrap) =>
  wrap == null
    ? 'not mounted'
    : `${wrap.lines} line${wrap.lines === 1 ? '' : 's'} in ${wrap.clientWidth}px${wrap.wraps ? ' (wrapped)' : ''}`

/* ── exact-release provenance ─────────────────────────────────────────────── */

/* The markers each side's served bundle must carry. They exist only in the release
   that rewrote the helper list as a tree, so a stale dist fails on content; the
   release proof below is what makes the claim exact rather than marker-level. */
export const DEMO_MARKERS = ['data-helper-demo', 'helper-tree-rail__path']
/* The marker the build under review introduces on EVERY collective route, and the
   one only the collective page's bundle carries. An arm requires the markers its
   OWN route bundle has: the contribute and review routes do not load the
   contributions panel's chunk, so requiring that marker there would refuse a
   perfectly attributable build. */
export const APP_MARKERS = ['data-helper-group-owner']
export const APP_PANEL_MARKER = 'my-contributions-panel'

/* The tag the fairtrade repo cuts for a release (`fairtrade-vX.Y.Z[-rcN]`). */
export const DEMO_TAG_PREFIX = 'fairtrade-v'

/* The version this app pins. Read from the app manifest so the proof cannot drift
   from the dependency the app actually resolves. */
export const pinnedFairtradeVersion = () => {
  const manifest = JSON.parse(readFileSync(new URL('../../package.json', import.meta.url), 'utf8'))
  const pin = manifest.dependencies?.['@peasant-labs/fairtrade']
  if (!pin) throw new Error('frontend/package.json does not depend on @peasant-labs/fairtrade')
  return String(pin).replace(/^[^\d]*/, '')
}

export const sha256Hex = (buffer) => createHash('sha256').update(buffer).digest('hex')

/* Null when the named demo checkout is exactly the pinned release, else the
   actionable reason. `describeTag` is `git describe --tags --exact-match`, so a
   checkout sitting between releases cannot pass. */
export const releaseProofProblem = ({ checkout, pinnedVersion, checkoutVersion, describeTag }) => {
  const head = 'ERROR [collective-sxs] the demo reference is not an exact release of the pinned design system.'
  const detail = (what, fix) =>
    `${head}\n` +
    `  What failed: ${what}\n` +
    `  Where: demo-helper-groups-shoot.mjs exact-release proof (FAIRTRADE_CHECKOUT=${checkout || '(unset)'}).\n` +
    `  Means: the reference pane could only be attributed to a marker, not to the release this app pins.\n` +
    `  Fix: ${fix}`
  if (!checkout) {
    return detail(
      'no demo checkout was named, so no release could be proven.',
      'point FAIRTRADE_CHECKOUT at the fairtrade checkout you built the served dist from, or pin the served bytes with DEMO_ASSET_SHA256 (recorded as digest-only, see the harness README).',
    )
  }
  if (checkoutVersion !== pinnedVersion) {
    return detail(
      `that checkout's package.json version is "${checkoutVersion}" but this app pins "${pinnedVersion}".`,
      'build and serve the demo from the checkout at the pinned version, then retry.',
    )
  }
  if (describeTag !== `${DEMO_TAG_PREFIX}${pinnedVersion}`) {
    return detail(
      `git describe --tags --exact-match answered "${describeTag || '(no exact tag)'}" instead of "${DEMO_TAG_PREFIX}${pinnedVersion}".`,
      'check out the exact release tag that produced this version (not a branch head between releases) and rebuild the demo.',
    )
  }
  return null
}

/* Null when the served asset's digest is the one proven (cross-checked against the
   checkout's own built file, or pinned by the operator), else the reason. */
export const digestProblem = ({ served, local, source }) => {
  const head = 'ERROR [collective-sxs] the served demo asset does not match the proven build.'
  const detail = (what, fix) =>
    `${head}\n` +
    `  What failed: ${what}\n` +
    `  Where: demo-helper-groups-shoot.mjs served-asset digest check (${source}).\n` +
    `  Means: the captured reference came from bytes nobody can attribute.\n` +
    `  Fix: ${fix}`
  if (!served) {
    return detail('the served asset could not be fetched to digest it.', 'confirm DEMO_ORIGIN serves the built demo, then retry.')
  }
  if (!local) {
    return detail(
      `the checkout's own dist has no file matching the served asset (${served.url}).`,
      'serve the dist built by that checkout (`vite preview` from its root), or stop it and rebuild.',
    )
  }
  if (local.sha256 !== served.sha256) {
    return detail(
      `the served asset (${served.url}, sha256 ${served.sha256.slice(0, 16)}…) differs from the same file inside the pinned checkout's dist (sha256 ${local.sha256.slice(0, 16)}…).`,
      'the server is serving a different dist than the checkout under proof; stop it and re-serve the pinned dist.',
    )
  }
  return null
}

/* The provenance record a given arm is judged by: an arm's capture is only
   attributable by the bundle ITS OWN route served, so the per-arm entry is the
   one that counts. A record whose arms list does not name this surface returns
   null, and the stitcher refuses the composite rather than attributing it with
   another arm's bundle. */
export const provenanceForArm = (record, surface) => {
  const entry = (record?.arms ?? []).find((candidate) => candidate.surface === surface)
  if (!entry) return null
  return {
    ...record,
    asset: entry.asset ?? record.asset,
    local: entry.local ?? record.local,
    markers: entry.markers ?? record.markers,
  }
}

/* The one provenance line drawn on every composite and printed in the run log.
   Says which proof applied, and what it does NOT cover. */
export const provenanceLine = (record) => {
  if (!record) return 'provenance · not recorded (this reference was staged by hand)'
  const asset = `${record.asset?.file ?? record.asset?.url ?? '(unknown asset)'} sha256 ${String(record.asset?.sha256 ?? '').slice(0, 16)}…`
  if (record.proof === 'exact-release') {
    return `provenance · fairtrade ${record.pinnedVersion} · tag ${record.tag} (exact release, described --exact-match) · ${asset} · served bytes == the checkout dist's bytes · NOT proven: that these bytes were published, or that the tree the dist was built from was clean`
  }
  if (record.proof === 'digest-only') {
    return `provenance · ${asset} · digest pinned by DEMO_ASSET_SHA256 · NOT proven: which release built it (no checkout proof)`
  }
  if (record.proof === 'served-bundle') {
    return `provenance · served bundle ${record.asset?.file ?? '(unknown)'} sha256 ${String(record.asset?.sha256 ?? '').slice(0, 16)}… · carries ${(record.markers ?? []).join(', ')} · NOT proven: that the tree this build came from was clean, or that the server serves only this build`
  }
  return `provenance · ${asset} · markers only · NOT proven: release, version or build`
}

/* ── the fail-closed side check ───────────────────────────────────────────── */

export const SIDE_NAMES = { reference: 'reference (fairtrade demo)', subject: 'subject (village app)' }

/* Why a measured side is blank, or null when it carries real content. The
   thresholds are the vendored non-empty-surface gate's own constants, so the
   demo side, the app side and the shoot scripts enforce one content floor. */
export const blankSideReason = ({ bytes, nonbgRatio, distinctColors } = {}) => {
  if (bytes == null || nonbgRatio == null || distinctColors == null) return 'unmeasurable (no decoded PNG)'
  if (bytes < DEFAULT_MIN_BYTES) return `${bytes} bytes, below the ${DEFAULT_MIN_BYTES}-byte content floor`
  if (nonbgRatio < MIN_NONBG_RATIO) {
    return `a near-uniform fill (${(nonbgRatio * 100).toFixed(2)}% of pixels differ from its background, floor ${(MIN_NONBG_RATIO * 100).toFixed(1)}%)`
  }
  if (distinctColors < MIN_DISTINCT_COLORS) return `a flat fill (${distinctColors} distinct colours, floor ${MIN_DISTINCT_COLORS})`
  return null
}

/* Null when this side may be composed, else the actionable reason it may not. */
export const sideProblem = ({ side, surface, theme, path, exists, measurement, otherMd5 = null }) => {
  const who = SIDE_NAMES[side] ?? side
  const head = `ERROR [collective-stitch-sxs] the ${who} side of "${surface}" (${theme}) cannot be composed.`
  const tail = (what, means, fix) =>
    `${head}\n` +
    `  What failed: ${what}\n` +
    `  Where: collective-stitch-sxs.mjs side check on ${path || '(unknown path)'}.\n` +
    `  Means: ${means}\n` +
    `  Fix: ${fix}`
  if (!exists) {
    return tail(
      `no capture at ${path}.`,
      'the composite would pair one real surface with a missing pane, so the run would report a comparison it never made.',
      side === 'reference'
        ? `capture the demo side first: CHROME_PATH=... FAIRTRADE_CHECKOUT=<pinned checkout> DEMO_ORIGIN=<built demo origin> node scripts/visual/demo-helper-groups-shoot.mjs ${theme} ${demoDir('<base>', theme)}`
        : `capture the app side first: GROUPED_SHOOT_SURFACE=<arm> VILLAGE_ORIGIN=<app origin> node scripts/visual/grouped-helper-shoot.mjs ${theme} ${appDir('<base>', theme)}`,
    )
  }
  const blank = blankSideReason(measurement)
  if (blank) {
    return tail(
      `${path} is blank: ${blank}.`,
      'a blank pane would be read as a surface that matches by having nothing to show, the silent-blank hole the content floor exists to close.',
      're-run the shoot that writes this side against a mounted surface (see the harness README), then re-stitch.',
    )
  }
  if (otherMd5 && measurement.md5 === otherMd5) {
    return tail(
      `${path} is byte-identical (md5 ${measurement.md5.slice(0, 12)}) to the other side of the same composite.`,
      'two sides of a demo-versus-app pair cannot be the same pixels; one side was written twice or from the wrong origin.',
      'confirm each side was captured from its own origin and worktree, then re-stitch.',
    )
  }
  return null
}
