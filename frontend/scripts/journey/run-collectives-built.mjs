import { createServer } from 'node:http'
import { createServer as createNetServer } from 'node:net'
import { execFileSync } from 'node:child_process'
import { createHash, randomUUID } from 'node:crypto'
import { readFileSync, writeFileSync, mkdirSync, readdirSync, existsSync } from 'node:fs'
import { resolve, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { setTimeout as delay } from 'node:timers/promises'
import { handleCollectiveRequest } from './lib/collective-fixtures.mjs'
import { handleHomeRequest } from './lib/home-fixtures.mjs'
import { handleExploreRequest } from '../visual/mock-rest-explore.mjs'
import { acquireBuildLifecycle, bounded } from './lib/built-lifecycle.mjs'
import { collectiveQueueFixture } from './lib/collectives-built-fixtures.mjs'

const frontend = fileURLToPath(new URL('../../', import.meta.url))
process.chdir(frontend)
const repo = resolve(frontend, '..')
const root = join(repo, 'review-capture', `collectives-built-${randomUUID()}`)
const lock = join(frontend, '.collectives-built-lock')
const hash = (bytes) => createHash('sha256').update(bytes).digest('hex')
const lifecycle = await acquireBuildLifecycle(lock)
let server
let cleaning
async function cleanup() {
  if (cleaning) return cleaning
  cleaning = (async () => {
    try {
      if (server?.listening) { server.closeAllConnections(); await new Promise((done) => server.close(done)) }
    } finally { await lifecycle.cleanup() }
  })()
  return cleaning
}
for (const signal of ['SIGINT', 'SIGTERM', 'SIGHUP']) process.once(signal, () => {
  void cleanup().then(() => process.exit(1), (error) => { console.error(error); process.exit(1) })
})
function launch(args, env) {
  if (cleaning) throw new Error('The collective build is stopping; no new process may start during cleanup')
  return lifecycle.launch('pnpm', args, { cwd: frontend, stdio: 'inherit', env: { ...process.env, ...env } })
}
async function unusedPort() {
  const socket = createNetServer()
  await new Promise((done, fail) => { socket.once('error', fail); socket.listen(0, '127.0.0.1', done) })
  const port = socket.address().port
  await new Promise((done) => socket.close(done))
  return port
}
try {
  mkdirSync(root, { recursive: true })
  const apiRequests = []
  let contributionContext = false
  server = createServer(async (req, res) => {
    const url = new URL(req.url, 'http://fixture')
    apiRequests.push(url.pathname)
    if (req.method === 'OPTIONS') { res.writeHead(204, { 'access-control-allow-origin': '*', 'access-control-allow-headers': '*' }); res.end(); return }
    if (!url.pathname.startsWith('/api/v1/')) { res.writeHead(400); res.end('fixture requires the complete /api/v1 API base'); return }
    if (req.method === 'POST' && url.pathname === '/api/v1/__built/contribution-context') {
      contributionContext = url.searchParams.get('enabled') === '1'
      res.writeHead(204); res.end(); return
    }
    const queue = collectiveQueueFixture(url, contributionContext)
    if (queue !== undefined) {
      res.writeHead(200, { 'content-type': 'application/json', 'access-control-allow-origin': '*' })
      res.end(JSON.stringify(queue))
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
