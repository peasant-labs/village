// @vitest-environment node
import { afterEach, describe, expect, it } from 'vitest'
import { spawn } from 'node:child_process'
import { mkdtempSync, readFileSync, writeFileSync, existsSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { zVillageGroupedContributableResponse, zVillageSessionListPayload } from '@peasant-labs/schema'
import { acquireBuildLifecycle, bounded, ownedGroups } from './built-lifecycle.mjs'
import { collectiveQueueFixture } from './collectives-built-fixtures.mjs'
import { loadBuiltSupportFixtures } from './built-support-fixtures.ts'

const resources = []
afterEach(async () => { for (const cleanup of resources.splice(0).reverse()) await cleanup() })

function peer(directory, mode = 'hold') {
  const child = spawn(process.execPath, [new URL('./lifecycle-disposable.mjs', import.meta.url).pathname, directory, mode], { stdio: ['pipe', 'pipe', 'pipe'] })
  const settled = new Promise((resolve) => child.once('exit', (code, signal) => resolve({ code, signal })))
  const ready = new Promise((resolve) => child.stdout.once('data', (data) => resolve(JSON.parse(data.toString()))))
  resources.push(async () => { child.kill('SIGKILL'); await bounded(settled, 3000, 'disposable peer exit') })
  return { child, settled, ready }
}
async function killedOwner(directory) {
  const owner = peer(directory, 'orphan')
  owner.child.stdin.write('acquire')
  const identity = await bounded(owner.ready, 3000, 'disposable owner startup')
  owner.child.kill('SIGKILL')
  await bounded(owner.settled, 3000, 'disposable owner kill')
  // Production recovery handles any child left behind by this killed owner.
  resources.push(async () => { const lifecycle = await acquireBuildLifecycle(directory, { termMs: 100, killMs: 2000 }); await lifecycle.cleanup() })
  return identity
}

describe('built collective fixture and lifetime contracts', () => {
  for (const row of loadBuiltSupportFixtures()) {
    it(row.name, async () => {
      if ('route' in row) {
        const response = collectiveQueueFixture(new URL(row.route, 'http://fixture'))
        const schema = row.schema === 'contributable' ? zVillageGroupedContributableResponse : zVillageSessionListPayload
        expect(schema.safeParse(response).success).toBe(true)
        expect((row.schema === 'contributable' ? zVillageSessionListPayload : zVillageGroupedContributableResponse).safeParse(response).success).toBe(false)
        return
      }
      const root = mkdtempSync(join(tmpdir(), 'collectives-lifecycle-'))
      resources.push(() => rmSync(root, { recursive: true, force: true }))
      const directory = join(root, 'ownership')
      if (row.name === 'term-resistant-child') {
        const lifecycle = await acquireBuildLifecycle(directory, row)
        resources.push(() => lifecycle.cleanup())
        const before = readFileSync(join(directory, 'owner.json'), 'utf8')
        const child = lifecycle.launch(process.execPath, ['-e', "process.on('SIGTERM',()=>{}); process.stdout.write('ready\\n'); setTimeout(()=>process.exit(3),15000)"], { stdio: ['ignore', 'pipe', 'inherit'] })
        await bounded(new Promise((resolve) => child.child.stdout.once('data', resolve)), 3000, 'TERM-resistant child readiness')
        expect(readFileSync(join(directory, 'owner.json'), 'utf8')).toBe(before)
        let settlements = 0
        void child.settled.then(() => { settlements += 1 })
        const cleanup = lifecycle.cleanup()
        expect(lifecycle.cleanup()).toBe(cleanup)
        await cleanup
        expect(settlements).toBe(1)
        expect(ownedGroups(lifecycle.identity.owner).size).toBe(0)
        expect(await child.settled).toEqual({ code: null, signal: 'SIGKILL' })
        expect(existsSync(directory)).toBe(false)
      } else if (row.name === 'concurrent-stale-acquisition') {
        const old = await killedOwner(directory)
        const first = peer(directory)
        const second = peer(directory)
        first.child.stdin.write('acquire'); second.child.stdin.write('acquire')
        const result = await bounded(Promise.race([
          first.ready.then((identity) => ({ winner: first, loser: second, identity })),
          second.ready.then((identity) => ({ winner: second, loser: first, identity })),
        ]), 5000, 'concurrent ownership acquisition')
        expect(await bounded(result.loser.settled, 3000, 'losing contender exit')).toEqual({ code: 2, signal: null })
        expect(JSON.parse(readFileSync(join(directory, 'owner.json'), 'utf8'))).toEqual(result.identity)
        expect(ownedGroups(old.owner).size).toBe(0)
        await expect(acquireBuildLifecycle(directory, row)).rejects.toThrow(/ownership is busy/)
        expect(JSON.parse(readFileSync(join(directory, 'owner.json'), 'utf8'))).toEqual(result.identity)
        result.winner.child.stdin.end()
        expect(await bounded(result.winner.settled, 3000, 'winner cleanup')).toEqual({ code: 0, signal: null })
        expect(existsSync(directory)).toBe(false)
      } else {
        const other = await acquireBuildLifecycle(join(root, 'unrelated'), row)
        resources.push(() => other.cleanup())
        const untouched = other.launch(process.execPath, ['-e', "process.stdout.write('ready\\n'); setTimeout(()=>process.exit(3),15000)"], { stdio: ['ignore', 'pipe', 'inherit'] })
        await bounded(new Promise((resolve) => untouched.child.stdout.once('data', resolve)), 3000, 'unrelated process readiness')
        const old = await killedOwner(directory)
        const immutable = readFileSync(join(directory, 'owner.json'), 'utf8')
        writeFileSync(join(directory, 'interrupted-bookkeeping.tmp'), '{')
        expect(readFileSync(join(directory, 'owner.json'), 'utf8')).toBe(immutable)
        const next = await acquireBuildLifecycle(directory, row)
        resources.push(() => next.cleanup())
        expect(next.identity.owner).not.toBe(old.owner)
        expect(ownedGroups(old.owner).size).toBe(0)
        expect(ownedGroups(other.identity.owner).size).toBeGreaterThan(0)
        expect(untouched.child.exitCode).toBe(null)
        await next.cleanup()
      }
    }, 20_000)
  }
})
