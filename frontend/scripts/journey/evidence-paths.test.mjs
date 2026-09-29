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
