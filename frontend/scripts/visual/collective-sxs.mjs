/* The collective grouped surfaces' side-by-side arms: the mapping each arm is
   judged by, where each side's capture lives, and the fail-closed check that a
   composite is never built from a missing or blank side.

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

   This module is shared: the demo shoot, the app-side shoot registration and the
   stitcher all read the same table, and the self-check drives the same
   `sideProblem` predicate the stitcher fails closed with. No test-only path.

   The demo side is captured for every arm, so a renamed or removed demonstration
   case surfaces as a missing reference rather than an unverified comparison. */

import { DEFAULT_MIN_BYTES, MIN_DISTINCT_COLORS, MIN_NONBG_RATIO } from './surface-gate.mjs'

export const THEMES = ['dark', 'light']

/* One entry per SxS arm.

   surface      the composite's name under <base>/sxs/collective/
   app          the app capture `grouped-helper-shoot.mjs` writes (full surface)
   appGrouped   the bounded grouped region that same run writes (the `-panel` file)
   demo         the demo's whole in-use surface for the arm's canonical case
   demoGrouped  the demo's `.helper-demo` demonstration element for that case
   demoCase     the `?helpers=` case the demo reference mounts
   demoState    `expanded` (disclosure open) or `selected` (open + one member ticked)
   mapping      what this pair can and cannot compare, read by a human reviewer
*/
export const COLLECTIVE_ARMS = [
  {
    surface: 'collective-grouped-browse',
    app: 'village-collective-grouped-browse.png',
    appGrouped: 'village-collective-grouped-browse-panel.png',
    demo: 'demo-collective-grouped-browse.png',
    demoGrouped: 'demo-collective-grouped-browse-tree.png',
    demoCase: 'three-independent-counts',
    demoState: 'expanded',
    mapping:
      'the collective browse list is read-only, so this pair judges the owner anchor, the single indent step, the count chip, the traced connector and the individually linked members. The demo control additionally carries per-member checkboxes the browse list deliberately does not offer.',
  },
  {
    surface: 'collective-grouped-contribute',
    app: 'village-collective-contribute-grouped-helper.png',
    appGrouped: 'village-collective-contribute-grouped-helper-panel.png',
    demo: 'demo-collective-grouped-contribute.png',
    demoGrouped: 'demo-collective-grouped-contribute-tree.png',
    demoCase: 'three-independent-counts',
    demoState: 'selected',
    demoSelectThread: 'G2',
    mapping:
      'both sides tick exactly one member. The app states `contribute 1 transcript` on the route\'s own action bar where the demo states the selected identity in its summary line, and each side keeps its own closure, so off-primitive chrome is not comparable.',
  },
  {
    surface: 'collective-grouped-review',
    app: 'village-collective-review-grouped-helper.png',
    appGrouped: 'village-collective-review-grouped-helper-panel.png',
    demo: 'demo-collective-grouped-review.png',
    demoGrouped: 'demo-collective-grouped-review-tree.png',
    demoCase: 'trunk-replacement',
    demoState: 'selected',
    demoSelectThread: 'G4',
    mapping:
      'the review queue selects one identity, never a group. The reference is the named example whose group holds two members carrying the same title and a third that does not, so a tick visibly names ONE row; the app states `1 selected` on its decision bar where the demo states the identity in its summary line.',
  },
  {
    surface: 'collective-grouped-my-shares',
    app: 'village-collective-my-shares.png',
    appGrouped: 'village-collective-my-shares-panel.png',
    demo: 'demo-collective-grouped-my-shares.png',
    demoGrouped: 'demo-collective-grouped-my-shares-tree.png',
    demoCase: 'ordinary-child-exit',
    demoState: 'expanded',
    mapping:
      'the panel keeps the contribution row it always drew (its transcript link, its pending state, its unshare control, its count) and hangs the grouped disclosure beneath that row, which is the reference example whose owner row keeps its own ordinary exit beside its grouped children. The panel\'s grouped read is display-only: its members are individually linked and carry no per-member tick and no traced connector, which the reference example does draw.',
  },
]

/* The four arms this gate must always carry. `git` cannot delete a case from a
   fixture file, so the required-NAME manifest in the self-check fixture pins
   them; this list is the same manifest in code, read by the arms table's own
   self-check. */
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

/* Both sides of the grouped-region composite: the demo's demonstration element
   against the bounded app region the arm's own run captures. */
export const groupedSidePaths = (base, theme, arm) => ({
  reference: `${demoDir(base, theme)}/${arm.demoGrouped}`,
  subject: `${appDir(base, theme)}/${arm.appGrouped}`,
})

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

/* Null when this side may be composed, else the actionable reason it may not.

   `exists` is the operator's filesystem answer and `measurement` the gate's
   reading of that file. Both sides are checked the same way: a composite whose
   reference or subject is absent, blank or byte-identical to its counterpart
   would show a pane that is not the surface under review. */
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
        ? `capture the demo side first: CHROME_PATH=... DEMO_ORIGIN=<built demo origin> node scripts/visual/demo-helper-groups-shoot.mjs ${theme} ${demoDir('<base>', theme)}`
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
