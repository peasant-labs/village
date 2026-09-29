/* Where the evidence job finds each screenshot and clip that report.json records.
 *
 * Drives the same functions ci-post-evidence.mjs uses. The cases live in
 * testdata/evidence-paths.yaml (never inline here); each case names the files that
 * exist, so the fixture runs the same on any machine. One case also runs against
 * the real filesystem, the check the posting script uses by default.
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
const existsIn = (present) => {
  const files = new Set(present)
  return (path) => files.has(path)
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
  })
})

describe('resolveAttachmentPath', () => {
  for (const c of fixture.pathCases) {
    it(c.name, () => {
      const got = resolveAttachmentPath(c.recorded, c.artifactsDir ?? fixture.artifactsDir, existsIn(c.present))
      expect(got).toBe(c.want)
    })
  }

  it('checks the real filesystem when no check is passed', () => {
    const c = fixture.pathCases.find((x) => x.name === 'container-path-re-roots-to-local')
    const root = mkdtempSync(join(tmpdir(), 'evidence-paths-'))
    try {
      const local = join(root, c.want.slice(fixture.artifactsDir.length))
      mkdirSync(dirname(local), { recursive: true })
      writeFileSync(local, '')
      expect(resolveAttachmentPath(c.recorded, root)).toBe(local)
      rmSync(local)
      expect(resolveAttachmentPath(c.recorded, root)).toBeNull()
    } finally {
      rmSync(root, { recursive: true, force: true })
    }
  })
})

describe('collectMedia', () => {
  for (const c of fixture.reportCases) {
    it(c.name, () => {
      const got = collectMedia(c.tests, fixture.artifactsDir, existsIn(c.present))
      expect(got).toEqual(c.want)
    })
  }
})
