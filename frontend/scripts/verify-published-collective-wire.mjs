import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
import { createRequire } from 'node:module'
import { createHash } from 'node:crypto'
import { parse } from 'yaml'
import { zVillageGroupedContributableResponse, zVillageDeprecatedGroupedContributableResponse } from '@peasant-labs/schema'

const [evidenceDir, oldPackageRoot] = process.argv.slice(2)
if (!evidenceDir || !oldPackageRoot) throw new Error('Pass the owned real PostgreSQL producer evidence directory and an independently installed published schema0.25 package root')
const requireOld = createRequire(resolve(oldPackageRoot, 'package.json'))
const currentManifest = JSON.parse(readFileSync(new URL('../node_modules/@peasant-labs/schema/package.json', import.meta.url), 'utf8'))
if (currentManifest.version !== '0.31.0') throw new Error('Current producer proof requires the actual published0.31.0 frontend dependency')
const old = await import(pathToFileURL(requireOld.resolve('@peasant-labs/schema')).href)
const oldManifest = JSON.parse(readFileSync(resolve(oldPackageRoot, 'node_modules/@peasant-labs/schema/package.json'), 'utf8'))
if (oldManifest.version !== '0.25.0') throw new Error('The legacy proof must use the actual published0.25.0 package, not a current alias')
const responses = Object.fromEntries(['canonical', 'legacy'].map((name) => {
  const bytes = readFileSync(resolve(evidenceDir, `${name}.json`))
  return [name, { value: JSON.parse(bytes), sha256: createHash('sha256').update(bytes).digest('hex') }]
}))
const validators = { canonical: zVillageGroupedContributableResponse, deprecated: zVillageDeprecatedGroupedContributableResponse, old: old.zVillageGroupedContributableResponse }
const cases = parse(readFileSync(new URL('./testdata/published-collective-wire.yaml', import.meta.url), 'utf8'), { strict: true })
const required = new Set(['current-canonical-accepts-real-postgres-producer', 'current-deprecated-accepts-real-postgres-producer', 'current-canonical-refuses-legacy-only-key', 'current-deprecated-refuses-canonical-only-key', 'published-old-parser-accepts-real-legacy-producer', 'published-old-parser-refuses-canonical-only-producer'])
for (const row of cases) {
  if (!required.delete(row.name) || !validators[row.validator] || !responses[row.response]) throw new Error(`Unknown/repeated wire proof ${row.name}`)
  const accepted = validators[row.validator].safeParse(responses[row.response].value).success
  if (accepted !== row.accepts) throw new Error(`Published parser disagrees with actual producer in ${row.name}: accepts=${accepted}`)
  console.log(JSON.stringify({ name: row.name, accepted, producerSHA256: responses[row.response].sha256 }))
}
if (required.size) throw new Error(`Missing published wire proofs: ${[...required].join(', ')}`)
