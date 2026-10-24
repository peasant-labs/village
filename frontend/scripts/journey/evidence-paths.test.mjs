/* Where the evidence job finds each screenshot and clip that report.json records.
 *
 * Drives the same functions ci-post-evidence.mjs uses. The cases live in
 * testdata/evidence-paths.yaml (never inline here); each case names the files that
 * exist, so the fixture runs the same on any machine. One case also runs through
 * collectMedia against the real filesystem, the check the posting script gets by
 * default.
 */
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import YAML from 'yaml'
import { collectMedia, resolveAttachmentPath } from './evidence-paths.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const fixture = YAML.parse(readFileSync(join(here, 'testdata/evidence-paths.yaml'), 'utf8'))

// An existence check over the case's files that records every path it is asked about.
const existsIn = (present, probed = []) => {
  const files = new Set(present)
  return (path) => {
    probed.push(path)
    return files.has(path)
  }
}

describe('evidence path fixtures', () => {
  it('carry every required case by name, each with a reason', () => {
    const groups = [
      [fixture.requiredPathCases, fixture.pathCases],
      [fixture.requiredReportCases, fixture.reportCases],
    ]
    for (const [required, cases] of groups) {
      const names = cases.map((c) => c.name)
      for (const name of required) expect(names).toContain(name)
      expect(new Set(names).size).toBe(names.length)
      for (const c of cases) expect(c.why?.trim(), `${c.name} has no why`).toBeTruthy()
    }
    for (const c of fixture.pathCases) expect(Array.isArray(c.probed), `${c.name} has no probed list`).toBe(true)
  })
})

describe('resolveAttachmentPath', () => {
  for (const c of fixture.pathCases) {
    it(c.name, () => {
      const probed = []
      const got = resolveAttachmentPath(c.recorded, c.artifactsDir ?? fixture.artifactsDir, existsIn(c.present, probed))
      expect(got).toBe(c.want)
      expect(probed).toEqual(c.probed)
    })
  }
})

describe('the journey workflow failure gate', () => {
  it('collects both evidence artifacts, then fails the job when the catalog did not succeed', () => {
    const workflow = YAML.parse(readFileSync(join(here, '../../../.github/workflows/journey.yml'), 'utf8'))
    const steps = workflow.jobs.journey.steps
    const catalog = steps.find((step) => step.id === 'journey')
    expect(catalog, 'the catalog step is the one whose outcome is re-raised').toBeTruthy()
    expect(catalog['continue-on-error'], 'the catalog step continues only so its evidence is collected').toBe(true)

    const uploads = steps
      .filter((step) => step.uses?.startsWith('actions/upload-artifact'))
      .map((step) => ({ name: step.with?.name, index: steps.indexOf(step) }))
    expect(uploads.map((upload) => upload.name)).toEqual(
      expect.arrayContaining(['journey-artifacts', 'collectives-built']),
    )

    const built = steps.findIndex((step) => step.name === 'Verify built collective routes and legacy redirects')
    expect(built, 'the built-route check runs in this job').toBeGreaterThanOrEqual(0)
    expect(steps[built]['continue-on-error'], 'a built-route failure fails the job directly').toBeUndefined()

    const gate = steps[steps.length - 1]
    expect(gate.if, 'the gate runs even after an earlier failure').toContain('always()')
    expect(gate.if, 'the gate re-raises the catalog outcome').toContain("steps.journey.outcome != 'success'")
    expect(gate.run, 'the gate exits non-zero').toMatch(/\bexit 1\b/)
    for (const upload of uploads) expect(steps.length - 1, `${upload.name} is uploaded before the gate`).toBeGreaterThan(upload.index)
    expect(built, 'the built-route check runs before the gate').toBeLessThan(steps.length - 1)

    const fail = workflow.jobs.evidence.steps.find((step) => step.name === 'Fail if journeys failed')
    expect(fail?.if, 'the evidence job fails the run on the journey job result').toContain("needs.journey.result == 'failure'")
  })
})

describe('collectMedia', () => {
  for (const c of fixture.reportCases) {
    it(c.name, () => {
      const got = collectMedia(c.tests, fixture.artifactsDir, existsIn(c.present))
      got.media.forEach((m, i) => expect(m.test).toBe(c.tests[i]))
      const media = got.media.map(({ image, video }) => ({ image, video }))
      expect({ ...got, media }).toEqual(c.want)
    })
  }

  it('checks the real filesystem when no check is passed', () => {
    const c = fixture.pathCases.find((x) => x.name === 'container-path-re-roots-to-local')
    expect(c.artifactsDir, 'the case must use the shared artifacts directory').toBeUndefined()
    const root = mkdtempSync(join(tmpdir(), 'evidence-paths-'))
    try {
      const artifactsDir = join(root, '.artifacts')
      const local = join(artifactsDir, c.want.slice(fixture.artifactsDir.length))
      const tests = [{ attachments: [{ contentType: 'image/png', path: c.recorded }] }]
      mkdirSync(dirname(local), { recursive: true })
      writeFileSync(local, '')
      expect(collectMedia(tests, artifactsDir)).toMatchObject({ resolved: 1, media: [{ image: local }] })
      rmSync(local)
      expect(collectMedia(tests, artifactsDir)).toMatchObject({ resolved: 0, media: [{ image: null }] })
    } finally {
      rmSync(root, { recursive: true, force: true })
    }
  })
})
