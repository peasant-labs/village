import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import YAML from 'yaml'
import { checkVendorPins } from './vendor-guard.mjs'

const dir = dirname(fileURLToPath(import.meta.url))
const corpus = YAML.parse(readFileSync(join(dir, 'vendor-digests.testdata.yaml'), 'utf8'))
const manifest = YAML.parse(readFileSync(join(dir, 'vendor-digests.testdata.manifest.yaml'), 'utf8'))
const vendors = corpus.files.filter(row => row.kind === 'vendored')
const bodies = new Map(vendors.map(row => [row.name, readFileSync(join(dir, row.name), 'utf8')]))
const canonical = text => text.replace(/^\/\* VENDORED from [\s\S]*?\*\/\n/, '')

describe('journey lib vendoring', () => {
  it('always checks current inventory and pins, with optional canonical checkout', () => {
    expect(checkVendorPins({ dir, files: corpus.files, ...manifest, checkout: process.env.FAIRTRADE_CHECKOUT })).toEqual([])
    expect(new Set(corpus.cases.map(row => row.name))).toEqual(new Set(manifest.requiredCaseNames))
    expect(corpus.cases.length).toBe(new Set(corpus.cases.map(row => row.name)).size)
  })
  for (const row of corpus.cases) it(row.name, () => {
    const scratch = mkdtempSync(join(tmpdir(), 'journey-pins-'))
    try {
      const local = join(scratch, 'local')
      const upstream = join(scratch, 'upstream')
      mkdirSync(local)
      mkdirSync(join(upstream, 'scripts/journey/lib'), { recursive: true })
      for (const [name, text] of bodies) {
        writeFileSync(join(local, name), text)
        writeFileSync(join(upstream, 'scripts/journey/lib', name), canonical(text))
      }
      const files = vendors.map(value => ({ ...value }))
      const first = files[0]
      const target = join(local, first.name)
      if (row.mutation === 'body') writeFileSync(target, bodies.get(first.name) + '\n// changed body\n')
      else if (row.mutation === 'unknown') writeFileSync(join(local, 'unclassified.mjs'), '')
      else if (row.mutation === 'pin') first.sha256 = '0'.repeat(64)
      else if (row.mutation === 'malformed-pin') first.sha256 = 'not-a-digest'
      else if (row.mutation === 'kind') first.kind = 'unrecognized'
      else if (row.mutation === 'relabel') { first.kind = 'app-local'; delete first.sha256 }
      else if (row.mutation === 'path') first.name = '../outside.mjs'
      else if (row.mutation === 'upstream') writeFileSync(join(upstream, 'scripts/journey/lib', first.name), canonical(bodies.get(first.name)) + '\n// changed upstream\n')
      else if (row.mutation === 'banner') writeFileSync(target, '/* VENDORED from canonical upstream. Reworded banner. */\n' + canonical(bodies.get(first.name)))
      else throw new Error(`unknown fixture mutation: ${row.mutation}`)
      const names = vendors.map(value => value.name)
      const problems = checkVendorPins({ dir: local, files, requiredFileNames: names, requiredVendoredNames: names, checkout: upstream })
      if (row.expected === null) expect(problems).toEqual([])
      else expect(problems.join('\n')).toContain(row.expected)
    } finally {
      rmSync(scratch, { recursive: true, force: true })
    }
  })
})
