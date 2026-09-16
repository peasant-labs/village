/* Self-check for the collective grouped side-by-side gate.
 *
 * Three refusals are pinned here, each driving the SAME predicate the harness
 * fails closed with, so no claim outruns its gate:
 *   - a composite is never built from a missing, blank or byte-identical side
 *     (`sideProblem`);
 *   - a demo reference is never captured without an EXACT-RELEASE proof and a
 *     served-bytes match (`releaseProofProblem`, `digestProblem`);
 *   - the app surface is never attested against the demo unless every property in
 *     the one compared set matches, or the missing element is a NAMED exception
 *     (`compareStyleRecords`).
 * The cases live in testdata/collective-sxs-selfcheck.yaml (never inline here).
 */
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import YAML from 'yaml'
import {
  APP_MARKERS,
  APP_PANEL_MARKER,
  COLLECTIVE_ARMS,
  COMPARED_PROPERTIES,
  REQUIRED_ARM_SURFACES,
  STYLE_DIVERGENCES,
  STYLE_EXCEPTIONS,
  appDir,
  compareStyleRecords,
  demoDir,
  digestProblem,
  groupedSidePaths,
  normalizeValue,
  provenanceForArm,
  releaseProofProblem,
  sidePaths,
  sideProblem,
} from './collective-sxs.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const fixture = YAML.parse(readFileSync(join(here, 'testdata/collective-sxs-selfcheck.yaml'), 'utf8'))

/* Build one side's style record from the fixture's shared faithful read-out,
   applying the case's patch and removals. */
const sideFromBase = (base, patch = {}, remove = []) => {
  const elements = structuredClone(base.elements)
  for (const [element, properties] of Object.entries(patch)) Object.assign(elements[element], properties)
  for (const element of remove) delete elements[element]
  return { elements }
}
const mismatchKeys = (result) => result.mismatches.map((entry) => `${entry.element}.${entry.property}`).sort()

describe('collective grouped side-by-side arms', () => {
  it('carries every required arm by name', () => {
    const surfaces = COLLECTIVE_ARMS.map((arm) => arm.surface)
    for (const required of fixture.requiredArms) expect(surfaces).toContain(required)
    expect(new Set(surfaces).size).toBe(surfaces.length)
    expect([...REQUIRED_ARM_SURFACES].sort()).toEqual([...fixture.requiredArms].sort())
  })

  it('names the demo case, the state, the scopes and the mapping every arm is judged by', () => {
    for (const arm of COLLECTIVE_ARMS) {
      expect(arm.demoCase, `${arm.surface} must name a demo case`).toBeTruthy()
      expect(['expanded', 'selected']).toContain(arm.demoState)
      if (arm.demoState === 'selected') expect(arm.demoSelectThread, `${arm.surface} must name the member it ticks`).toBeTruthy()
      // The mapping is the honest note about what the pair can and cannot compare.
      expect(arm.mapping.length, `${arm.surface} must document its mapping`).toBeGreaterThan(60)
      // Every arm must name the container each side's style probe reads in.
      expect(arm.styleScope?.reference, `${arm.surface} must name its reference style scope`).toBeTruthy()
      expect(arm.styleScope?.subject, `${arm.surface} must name its subject style scope`).toBeTruthy()
      expect(arm.shootSurface, `${arm.surface} must name the app shoot surface that records it`).toBeTruthy()
      for (const file of [arm.app, arm.demo, arm.appGrouped, arm.demoGrouped]) expect(String(file).endsWith('.png')).toBe(true)
    }
  })

  it('requires exactly the markers each arm\'s own route bundle carries', () => {
    for (const entry of fixture.armMarkers) {
      const arm = COLLECTIVE_ARMS.find((candidate) => candidate.surface === entry.arm)
      expect(arm, `${entry.arm} is not a carried arm`).toBeTruthy()
      expect([...arm.appMarkers].sort()).toEqual([...entry.markers].sort())
    }
    // Every arm keeps the mount marker the build under review introduces, and only
    // an arm whose route loads the contributions panel requires that marker too.
    for (const arm of COLLECTIVE_ARMS) expect(arm.appMarkers).toContain(APP_MARKERS[0])
    const panelArms = COLLECTIVE_ARMS.filter((arm) => arm.appMarkers.includes(APP_PANEL_MARKER)).map((arm) => arm.surface)
    expect(panelArms.sort()).toEqual(['collective-grouped-browse', 'collective-grouped-my-shares'])
  })

  it('separates contract-expected absences from recorded divergences, each named with its reason', () => {
    for (const list of [STYLE_EXCEPTIONS, STYLE_DIVERGENCES]) {
      for (const entry of list) {
        expect(COLLECTIVE_ARMS.some((arm) => arm.surface === entry.arm), `${entry.arm} is not a carried arm`).toBe(true)
        expect(Object.keys(COMPARED_PROPERTIES)).toContain(entry.element)
        expect(entry.reason.length, `${entry.arm}.${entry.element} must state why`).toBeGreaterThan(60)
      }
    }
    // No arm may appear on both lists for the same element: a gap is either the
    // contract's own behaviour or a recorded divergence, never both.
    for (const exception of STYLE_EXCEPTIONS) {
      expect(STYLE_DIVERGENCES.some((entry) => entry.arm === exception.arm && entry.element === exception.element)).toBe(false)
    }
  })

  it('compares one explicitly listed property set, with only the named exceptions', () => {
    expect(Object.keys(COMPARED_PROPERTIES).sort()).toEqual(['count', 'facts', 'rail', 'title', 'trigger'])
    // Every exception names an arm this table carries, an element the property set
    // reads, and a reason — an exception without a reason is a silent skip.
    for (const exception of STYLE_EXCEPTIONS) {
      expect(COLLECTIVE_ARMS.some((arm) => arm.surface === exception.arm), `${exception.arm} is not a carried arm`).toBe(true)
      expect(Object.keys(COMPARED_PROPERTIES)).toContain(exception.element)
      expect(exception.reason.length, `${exception.arm}.${exception.element} must state why`).toBeGreaterThan(40)
    }
  })

  it('resolves both levels of both sides under the base directory', () => {
    const base = '/captures'
    for (const theme of ['dark', 'light']) {
      for (const arm of COLLECTIVE_ARMS) {
        expect(sidePaths(base, theme, arm)).toEqual({
          reference: `${demoDir(base, theme)}/${arm.demo}`,
          subject: `${appDir(base, theme)}/${arm.app}`,
        })
        expect(groupedSidePaths(base, theme, arm)).toEqual({
          reference: `${demoDir(base, theme)}/${arm.demoGrouped}`,
          subject: `${appDir(base, theme)}/${arm.appGrouped}`,
        })
      }
    }
  })

  it.each(fixture.cases.map((entry) => [entry.name, entry]))('side check: %s', (_name, entry) => {
    const problem = sideProblem({
      side: entry.side,
      surface: 'collective-grouped-browse',
      theme: 'dark',
      path: `/captures/${entry.side}.png`,
      exists: entry.exists,
      measurement: entry.measurement,
      otherMd5: entry.otherMd5 ?? null,
    })
    if (entry.expected === null) {
      expect(problem).toBeNull()
      return
    }
    expect(problem).not.toBeNull()
    expect(problem).toContain(entry.expected)
    // Every refusal must be actionable: what failed, where, what it means, how to fix.
    for (const section of ['What failed:', 'Where:', 'Means:', 'Fix:']) expect(problem).toContain(section)
  })

  it.each(fixture.releaseProofCases.map((entry) => [entry.name, entry]))('release proof: %s', (_name, entry) => {
    const problem = releaseProofProblem({
      checkout: entry.checkout,
      pinnedVersion: entry.pinnedVersion,
      checkoutVersion: entry.checkoutVersion,
      describeTag: entry.describeTag,
    })
    if (entry.expected === null) {
      expect(problem).toBeNull()
      return
    }
    expect(problem).not.toBeNull()
    expect(problem).toContain(entry.expected)
    for (const section of ['What failed:', 'Where:', 'Means:', 'Fix:']) expect(problem).toContain(section)
  })

  it.each(fixture.digestCases.map((entry) => [entry.name, entry]))('served-bytes proof: %s', (_name, entry) => {
    const problem = digestProblem({ served: entry.served, local: entry.local, source: 'self-check' })
    if (entry.expected === null) {
      expect(problem).toBeNull()
      return
    }
    expect(problem).not.toBeNull()
    expect(problem).toContain(entry.expected)
    for (const section of ['What failed:', 'Where:', 'Means:', 'Fix:']) expect(problem).toContain(section)
  })

  it.each(fixture.styleCases.map((entry) => [entry.name, entry]))('style comparison: %s', (_name, entry) => {
    const arm = entry.arm ?? 'collective-grouped-contribute'
    const reference = sideFromBase(fixture.styleBase, entry.referencePatch, entry.referenceRemove ?? [])
    const subject = sideFromBase(fixture.styleBase, entry.subjectPatch, entry.subjectRemove ?? [])
    const result = compareStyleRecords({ arm, reference, subject })
    expect(mismatchKeys(result)).toEqual([...entry.expectedMismatches].sort())
    expect(result.exceptions.map((exception) => exception.element).sort()).toEqual([...entry.expectedExceptions].sort())
    expect(result.divergences.map((divergence) => divergence.element).sort()).toEqual([...(entry.expectedDivergences ?? [])].sort())
    expect(result.compared).toBeGreaterThan(0)
    // A recorded divergence is a report, never a silent skip: it names why.
    for (const divergence of result.divergences) expect(divergence.reason.length).toBeGreaterThan(60)
  })

  it('attributes an arm only by the bundle its own route served', () => {
    const record = {
      proof: 'served-bundle',
      asset: { file: 'last-arm.js', sha256: 'ffff' },
      markers: ['panel-marker'],
      arms: [
        { surface: 'collective-grouped-browse', asset: { file: 'page.js', sha256: 'aaaa' }, markers: ['mount'] },
        { surface: 'collective-grouped-contribute', asset: { file: 'contribute.js', sha256: 'bbbb' }, markers: ['mount'] },
      ],
    }
    expect(provenanceForArm(record, 'collective-grouped-browse').asset.file).toBe('page.js')
    expect(provenanceForArm(record, 'collective-grouped-contribute').asset.file).toBe('contribute.js')
    expect(provenanceForArm(record, 'collective-grouped-contribute').markers).toEqual(['mount'])
    // an arm the record does not name has NO attribution — never another arm's
    expect(provenanceForArm(record, 'collective-grouped-review')).toBeNull()
  })

  it('normalizes only the font-family alias it names', () => {
    expect(normalizeValue('fontFamily', '"Atkinson Hyperlegible Mono", ui-monospace, Menlo'))
      .toBe(normalizeValue('fontFamily', 'atkinsonHyperlegibleMono, "atkinsonHyperlegibleMono Fallback", ui-monospace'))
    expect(normalizeValue('fontFamily', '"Inter", sans-serif')).not.toBe(normalizeValue('fontFamily', '"Atkinson Hyperlegible Mono", ui-monospace'))
    // every other property is compared verbatim
    expect(normalizeValue('fontSize', '14px')).toBe('14px')
  })
})
