import { createServer } from 'node:http'
import { createServer as createNetServer } from 'node:net'
import { spawn, execFileSync } from 'node:child_process'
import { createHash, randomUUID } from 'node:crypto'
import { readFileSync, writeFileSync, mkdirSync, readdirSync, rmSync, existsSync } from 'node:fs'
import { resolve, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { setTimeout as delay } from 'node:timers/promises'
import { handleCollectiveRequest } from './lib/collective-fixtures.mjs'
import { handleHomeRequest } from './lib/home-fixtures.mjs'
import { handleExploreRequest } from '../visual/mock-rest-explore.mjs'

const frontend = fileURLToPath(new URL('../../', import.meta.url))
process.chdir(frontend)
const repo = resolve(frontend, '..')
const root = join(repo, 'review-capture', `collectives-built-${randomUUID()}`)
const lock = join(frontend, '.collectives-built-lock')
const owner = randomUUID()
const children = []
const hash = (bytes) => createHash('sha256').update(bytes).digest('hex')
const ticks = (pid) => readFileSync(`/proc/${pid}/stat`, 'utf8').split(') ')[1].split(' ')[19]
const alive = (pid, start) => { try { return ticks(pid) === start } catch { return false } }
const owned = (child, token) => readdirSync('/proc').some((pid) => {
  if (!/^\d+$/.test(pid)) return false
  try {
    const stat = readFileSync(`/proc/${pid}/stat`, 'utf8').split(') ')[1].split(' ')
    return stat[0] !== 'Z' && Number(stat[2]) === child.pid &&
      readFileSync(`/proc/${pid}/environ`, 'utf8').split('\0').includes(`COLLECTIVES_BUILD_OWNER=${token}`)
  } catch { return false }
})
async function stop(child, token) {
  if (!owned(child, token)) return
  const signal = (name) => { try { process.kill(-child.pid, name) } catch (error) { if (error.code !== 'ESRCH') throw error } }
  signal('SIGTERM')
  for (let i = 0; i < 20 && owned(child, token); i++) await delay(100)
  if (owned(child, token)) signal('SIGKILL')
}
async function stopOwnedGroups(token) {
  const groups = new Set()
  for (const pid of readdirSync('/proc').filter((value) => /^\d+$/.test(value))) {
    try {
      if (readFileSync(`/proc/${pid}/environ`, 'utf8').split('\0').includes(`COLLECTIVES_BUILD_OWNER=${token}`)) {
        groups.add(Number(readFileSync(`/proc/${pid}/stat`, 'utf8').split(') ')[1].split(' ')[2]))
      }
    } catch { /* process exited */ }
  }
  // Chromium can own a separate process group; the inherited random marker
  // proves it too, even if its Playwright parent died before cleanup.
  for (const pid of groups) await stop({ pid }, token)
}
// Linux /proc identity plus the random child environment marker prevents PID
// reuse from turning stale-run recovery into a broad process kill.
if (existsSync(lock)) {
  const old = JSON.parse(readFileSync(join(lock, 'owner.json'), 'utf8'))
  if (alive(old.pid, old.start)) throw new Error('A collective build already owns .next; wait for it to finish before running this command')
  await stopOwnedGroups(old.owner)
  console.log('Recovered stale collective build ownership; only marker-proven process groups were stopped')
  rmSync(lock, { recursive: true })
}
mkdirSync(lock)
mkdirSync(root, { recursive: true })
const saveOwner = () => writeFileSync(join(lock, 'owner.json'), JSON.stringify({ pid: process.pid, start: ticks(process.pid), owner, children }))
saveOwner()
let server
let cleaning
async function cleanup() {
  if (cleaning) return cleaning
  cleaning = (async () => {
    for (const child of [...children].reverse()) await stop(child, owner)
    await stopOwnedGroups(owner)
    if (server?.listening) { server.closeAllConnections(); await new Promise((done) => server.close(done)) }
    rmSync(lock, { recursive: true, force: true })
  })()
  return cleaning
}
for (const signal of ['SIGINT', 'SIGTERM', 'SIGHUP']) process.once(signal, () => { void cleanup().then(() => process.exit(1)) })
function launch(args, env) {
  if (cleaning) throw new Error('The collective build is stopping; no new process may start during cleanup')
  const child = spawn('pnpm', args, { cwd: frontend, detached: true, stdio: 'inherit', env: { ...process.env, ...env, COLLECTIVES_BUILD_OWNER: owner } })
  const record = { pid: child.pid, start: ticks(child.pid) }
  children.push(record); saveOwner()
  child.done = new Promise((done, fail) => { child.once('error', fail); child.once('exit', (code, signal) => code === 0 ? done() : fail(new Error(`${args.join(' ')} failed (${code ?? signal}); inspect ${root}`))) })
  // next start stays running while the suite executes; attach rejection now.
  child.done.catch(() => {})
  return child
}
async function bounded(promise, ms, operation) {
  let timer
  try { return await Promise.race([promise, new Promise((_, fail) => { timer = setTimeout(() => fail(new Error(`${operation} exceeded ${ms}ms; owned resources will be stopped`)), ms) })]) } finally { clearTimeout(timer) }
}
async function unusedPort() {
  const socket = createNetServer()
  await new Promise((done, fail) => { socket.once('error', fail); socket.listen(0, '127.0.0.1', done) })
  const port = socket.address().port
  await new Promise((done) => socket.close(done))
  return port
}
try {
  const apiRequests = []
  server = createServer(async (req, res) => {
    const url = new URL(req.url, 'http://fixture')
    apiRequests.push(url.pathname)
    if (req.method === 'OPTIONS') { res.writeHead(204, { 'access-control-allow-origin': '*', 'access-control-allow-headers': '*' }); res.end(); return }
    if (!url.pathname.startsWith('/api/v1/')) { res.writeHead(400); res.end('fixture requires the complete /api/v1 API base'); return }
    const queue = url.pathname.match(/^\/api\/v1\/collectives\/[^/]+\/(pending|contributable)$/)
    if (queue) {
      res.writeHead(200, { 'content-type': 'application/json', 'access-control-allow-origin': '*' })
      res.end(JSON.stringify(url.searchParams.get('view') === 'grouped'
        ? { items: [], page: 1, limit: Number(url.searchParams.get('limit') || 100), totalItems: 0, ordinarySessionTotal: 0, helperThreadTotal: 0 }
        : queue[1] === 'pending' ? [] : { transcripts: [] }))
      return
    }
    if (handleCollectiveRequest(req, res)) return
    if (await handleHomeRequest(req, res, 'default')) return
    if (handleExploreRequest(req, res)) return
    res.writeHead(404, { 'access-control-allow-origin': '*' }); res.end('no focused fixture route')
  })
  await new Promise((done, fail) => { server.once('error', fail); server.listen(0, '127.0.0.1', done) })
  const apiBase = `http://127.0.0.1:${server.address().port}/api/v1`
  const appURL = `http://127.0.0.1:${await unusedPort()}`
  const env = { NEXT_PUBLIC_API_URL: apiBase, COLLECTIVES_BUILT_ROOT: root, COLLECTIVES_BUILT_URL: appURL }
  await bounded(launch(['run', 'build'], env).done, 600_000, 'Next production build')
  const buildID = readFileSync(join(frontend, '.next/BUILD_ID'), 'utf8').trim()
  const assets = {}
  function inventory(dir) {
    for (const item of readdirSync(dir, { withFileTypes: true })) {
      const path = join(dir, item.name)
      if (item.isDirectory()) inventory(path)
      else assets[relative(join(frontend, '.next'), path)] = hash(readFileSync(path))
    }
  }
  inventory(join(frontend, '.next/static'))
  const manifest = {
    sourceCommit: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: repo, encoding: 'utf8' }).trim(),
    sourceDiffHash: hash(execFileSync('git', ['diff', 'HEAD'], { cwd: repo })),
    lockHash: hash(readFileSync(join(repo, 'pnpm-lock.yaml'))), frontend, apiBase, appURL, buildID, assets,
    servedVerified: false,
  }
  manifest.sourceFiles = Object.fromEntries(execFileSync('git', ['ls-files', '-z', '--cached', '--others', '--exclude-standard'], { cwd: repo, encoding: 'utf8' })
    .split('\0').filter(Boolean).filter((path) => existsSync(join(repo, path))).map((path) => [path, hash(readFileSync(join(repo, path)))]))
  writeFileSync(join(root, 'build.json'), JSON.stringify(manifest, null, 2))
  const app = launch(['exec', 'next', 'start', '--hostname', '127.0.0.1', '--port', new URL(appURL).port], env)
  const html = await bounded((async () => {
    for (;;) {
      try { const result = await fetch(`${appURL}/collectives`, { signal: AbortSignal.timeout(1500) }); if (result.ok) return result.text() } catch { /* not listening yet */ }
      await delay(150)
    }
  })(), 30_000, 'next start readiness')
  manifest.canonicalHTMLHash = hash(readFileSync(join(frontend, '.next/server/app/collectives.html')))
  if (!html.includes(buildID) || hash(html) !== manifest.canonicalHTMLHash || !html.includes('sign in to join or create collectives')) throw new Error('Served canonical HTML differs from this production build')
  const references = [...html.matchAll(/(?:src|href)="([^"?#]+\.(?:js|css))"/g)].map((match) => match[1]).filter((path) => path.startsWith('/_next/'))
  if (!references.length) throw new Error('No served Next assets found; provenance cannot be verified')
  for (const path of new Set(references)) {
    const response = await fetch(`${appURL}${path}`, { signal: AbortSignal.timeout(5000) })
    if (!response.ok || hash(Buffer.from(await response.arrayBuffer())) !== assets[path.replace('/_next/', '')]) throw new Error(`Served asset differs from owned build: ${path}`)
  }
  manifest.servedVerified = true
  manifest.verifiedAssets = [...new Set(references)]
  writeFileSync(join(root, 'build.json'), JSON.stringify(manifest, null, 2))
  await bounded(Promise.race([launch(['exec', 'playwright', 'test', '--config', 'playwright.collectives-built.config.mjs'], env).done, app.done.then(() => { throw new Error('next start exited during the built journey') })]), 300_000, 'built collective suite')
  if (!apiRequests.includes('/api/v1/collectives/visible') || apiRequests.some((path) => path.startsWith('/collectives') || path.includes('/api/v1/api/v1') || path.startsWith('/api/v1/groups'))) throw new Error('Built browser used the wrong API resource base')
  writeFileSync(join(root, 'fixture-requests.json'), JSON.stringify(apiRequests, null, 2))
  console.log(`Build evidence ready for inspection at ${root}`)
} finally { await cleanup() }
