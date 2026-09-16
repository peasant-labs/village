/* Self-check for the collective grouped side-by-side gate.
 *
 * A composite must never be built from a missing, blank, or byte-identical side:
 * a blank pane would be read as a surface that "matches" by having nothing to
 * show, which is the silent-blank hole the content floor exists to close. The
 * cases live in testdata/collective-sxs-selfcheck.yaml (never inline here) and
 * drive the SAME `sideProblem` predicate collective-stitch-sxs.mjs fails closed
 * with, so this suite checks the production path, not a copy of it.
 */
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import YAML from 'yaml'
import {
  COLLECTIVE_ARMS,
  REQUIRED_ARM_SURFACES,
  appDir,
  demoDir,
  groupedSidePaths,
  sidePaths,
  sideProblem,
} from './collective-sxs.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const fixture = YAML.parse(readFileSync(join(here, 'testdata/collective-sxs-selfcheck.yaml'), 'utf8'))

describe('collective grouped side-by-side arms', () => {
  it('carries every required arm by name', () => {
    const surfaces = COLLECTIVE_ARMS.map((arm) => arm.surface)
    for (const required of fixture.requiredArms) expect(surfaces).toContain(required)
    expect(new Set(surfaces).size).toBe(surfaces.length)
    expect([...REQUIRED_ARM_SURFACES].sort()).toEqual([...fixture.requiredArms].sort())
  })

  it('names the demo case, the state and the mapping every arm is judged by', () => {
    for (const arm of COLLECTIVE_ARMS) {
      expect(arm.demoCase, `${arm.surface} must name a demo case`).toBeTruthy()
      expect(['expanded', 'selected']).toContain(arm.demoState)
      if (arm.demoState === 'selected') expect(arm.demoSelectThread, `${arm.surface} must name the member it ticks`).toBeTruthy()
      // The mapping is the honest note about what the pair can and cannot compare.
      expect(arm.mapping.length, `${arm.surface} must document its mapping`).toBeGreaterThan(60)
      expect(arm.app.endsWith('.png')).toBe(true)
      expect(arm.demo.endsWith('.png')).toBe(true)
      expect(arm.appGrouped.endsWith('.png')).toBe(true)
      expect(arm.demoGrouped.endsWith('.png')).toBe(true)
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

  it.each(fixture.cases.map((entry) => [entry.name, entry]))('%s', (_name, entry) => {
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
    expect(problem).toContain('What failed:')
    expect(problem).toContain('Where:')
    expect(problem).toContain('Means:')
    expect(problem).toContain('Fix:')
  })
})
