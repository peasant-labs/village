/* Direct shared-helper pin check. No checkout is required; an explicit checkout
   additionally proves the three pinned bodies still match canonical upstream. */
import { createHash } from 'node:crypto'
import { lstatSync, readFileSync, readdirSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import YAML from 'yaml'

const banner = /^\/\* VENDORED from [\s\S]*?\*\/\n/
const digest = text => createHash('sha256').update(text.replace(banner, '')).digest('hex')
const sameNames = (a, b) => new Set(a).size === a.length && new Set(b).size === b.length && a.length === b.length && a.every(name => b.includes(name))

export function checkVendorPins({ dir, files, requiredFileNames, requiredVendoredNames, checkout }) {
  const problems = []
  for (const row of files) {
    if (!row || typeof row.name !== 'string' || !/^[a-zA-Z0-9][a-zA-Z0-9._-]*$/.test(row.name)) {
      problems.push('unsafe filename in vendor inventory')
      continue
    }
    if (!['vendored', 'app-local', 'guard'].includes(row.kind)) problems.push(`invalid kind: ${row.name}`)
    if (Object.keys(row).some(key => !['name', 'kind', 'sha256'].includes(key))) problems.push(`unknown inventory field: ${row.name}`)
    if (row.kind === 'vendored' ? !/^[0-9a-f]{64}$/.test(row.sha256 ?? '') : row.sha256 !== undefined) problems.push(`invalid pin: ${row.name}`)
  }
  if (problems.length) return problems
  const names = files.map(row => row.name)
  if (!sameNames(names, requiredFileNames)) problems.push('file membership differs from required inventory')
  if (!sameNames(files.filter(row => row.kind === 'vendored').map(row => row.name), requiredVendoredNames)) problems.push('vendored membership differs from required inventory')
  const actual = readdirSync(dir)
  if (!sameNames(actual, names)) problems.push('unclassified or missing file in vendor directory')
  for (const name of actual) {
    if (!lstatSync(join(dir, name)).isFile()) problems.push(`expected a regular file: ${name}`)
  }
  if (problems.length) return problems
  for (const row of files.filter(row => row.kind === 'vendored')) {
    if (digest(readFileSync(join(dir, row.name), 'utf8')) !== row.sha256) problems.push(`digest mismatch: ${row.name}`)
    if (checkout) {
      const upstream = join(resolve(checkout), 'scripts/journey/lib', row.name)
      try {
        if (!lstatSync(upstream).isFile()) throw new Error('not a regular file')
        const text = readFileSync(upstream, 'utf8')
        if (banner.test(text)) problems.push(`upstream must be canonical, not another vendored copy: ${row.name}`)
        else if (digest(text) !== row.sha256) problems.push(`upstream mismatch: ${row.name}`)
      } catch {
        problems.push(`missing or non-regular upstream file: ${row.name}`)
      }
    }
  }
  return problems
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const dir = dirname(fileURLToPath(import.meta.url))
  const corpus = YAML.parse(readFileSync(join(dir, 'vendor-digests.testdata.yaml'), 'utf8'))
  const manifest = YAML.parse(readFileSync(join(dir, 'vendor-digests.testdata.manifest.yaml'), 'utf8'))
  const problems = checkVendorPins({ dir, files: corpus.files, ...manifest, checkout: process.env.FAIRTRADE_CHECKOUT })
  if (problems.length) {
    console.error(problems.join('\n'))
    process.exitCode = 1
  } else console.log('journey vendoring: inventory and canonical body pins match')
}
