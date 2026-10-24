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
import { signedInPageLoadHeaders } from './lib/collectives-built-auth.mjs'
import { parse } from 'yaml'

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
/**
 * Whether a readiness read failed because the server is still starting rather
 * than because the answer was wrong. Only a transport failure may be retried:
 * a status, a redirect or an address that does not match is a logical answer
 * and fails closed below, so a long loop can never turn a wrong page into a
 * slow pass.
 */
function startupTransportFailure(error) {
  if (error?.name === 'TimeoutError' || error?.name === 'AbortError') return true
  // undici wraps every network-level failure (a refused connection, a socket
  // closed before it answers) in `TypeError: fetch failed` and puts the reason
  // on `cause`, whose shape differs by Node version; any of these means the
  // server gave no answer yet.
  if (error?.name === 'TypeError' && /fetch failed/i.test(error.message ?? '')) return true
  const codes = new Set()
  if (typeof error?.code === 'string') codes.add(error.code)
  if (typeof error?.cause?.code === 'string') codes.add(error.cause.code)
  for (const nested of error?.cause?.errors ?? []) {
    if (typeof nested?.code === 'string') codes.add(nested.code)
  }
  return [...codes].some((code) => ['ECONNREFUSED', 'ECONNRESET', 'EPIPE', 'UND_ERR_SOCKET'].includes(code))
}
try {
  mkdirSync(root, { recursive: true })
  const apiRequests = []
  let contributionContext = false
  server = createServer(async (req, res) => {
    const url = new URL(req.url, 'http://fixture')
    apiRequests.push(url.pathname)
    if (req.method === 'OPTIONS') {
      res.writeHead(204, { 'access-control-allow-origin': '*', 'access-control-allow-headers': 'content-type, authorization', 'access-control-allow-methods': 'GET, POST, PATCH, DELETE, OPTIONS' })
      res.end(); return
    }
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
  const schemaPackage = JSON.parse(readFileSync(join(frontend, 'node_modules/@peasant-labs/schema/package.json'), 'utf8'))
  const designPackage = JSON.parse(readFileSync(join(frontend, 'node_modules/@peasant-labs/fairtrade/package.json'), 'utf8'))
  const declared = JSON.parse(readFileSync(join(frontend, 'package.json'), 'utf8')).dependencies
  const lockfile = parse(readFileSync(join(repo, 'pnpm-lock.yaml'), 'utf8'))
  if (schemaPackage.version !== declared['@peasant-labs/schema'] || designPackage.version !== declared['@peasant-labs/fairtrade']) throw new Error('Built browser dependencies differ from exact declared published pins')
  manifest.dependencies = {
    schema: { version: schemaPackage.version, integrity: lockfile.packages[`@peasant-labs/schema@${schemaPackage.version}`]?.resolution.integrity },
    fairtrade: { version: designPackage.version, integrity: lockfile.packages[`@peasant-labs/fairtrade@${designPackage.version}`]?.resolution.integrity },
  }
  if (!manifest.dependencies.schema.integrity || !manifest.dependencies.fairtrade.integrity) throw new Error('Published package integrity is missing from the frozen lockfile')
  manifest.apiKind = 'app-owned-rest-fixture'
  manifest.sourceFiles = Object.fromEntries(execFileSync('git', ['ls-files', '-z', '--cached', '--others', '--exclude-standard'], { cwd: repo, encoding: 'utf8' })
    .split('\0').filter(Boolean).filter((path) => existsSync(join(repo, path))).map((path) => [path, hash(readFileSync(join(repo, path)))]))
  writeFileSync(join(root, 'build.json'), JSON.stringify(manifest, null, 2))
  const app = launch(['exec', 'next', 'start', '--hostname', '127.0.0.1', '--port', new URL(appURL).port], env)
  // One page load of the route, made by the signed-in session the browser
  // carries and asking for a document exactly as a navigation does. Redirects
  // are NOT followed: `src/proxy.ts` sends a page load without a session to the
  // marketing site, and following that redirect would verify another site's
  // HTML while looking like a readiness check. The answer is inspected for its
  // status, its redirect target and its final address before anything is
  // claimed about it, and only a transport failure (the server not listening
  // yet) is retried. The loop stops as soon as the bounded wait gives up, so a
  // failed startup exits instead of polling a dead port forever.
  let reading = true
  const served = await bounded((async () => {
    for (;;) {
      if (!reading) return null
      try {
        const response = await fetch(`${appURL}/collectives`, {
          headers: signedInPageLoadHeaders(),
          redirect: 'manual',
          signal: AbortSignal.timeout(1500),
        })
        return {
          status: response.status,
          location: response.headers.get('location'),
          url: response.url,
          bytes: Buffer.from(await response.arrayBuffer()),
        }
      } catch (error) {
        if (!reading) return null
        if (app.child.exitCode !== null || app.child.signalCode !== null) {
          throw new Error(`next start exited (${app.child.exitCode ?? app.child.signalCode}) before the collectives page load could be read; the server output above this line names its own reason`)
        }
        if (!startupTransportFailure(error)) throw error
        await delay(150)
      }
    }
  })(), 30_000, 'next start readiness').finally(() => { reading = false })
  if (served === null) throw new Error('next start readiness stopped before the collectives page load was read; inspect the retained build ownership and the server output before retrying')
  const canonicalHTML = readFileSync(join(frontend, '.next/server/app/collectives.html'))
  manifest.canonicalHTMLHash = hash(canonicalHTML)
  manifest.readiness = { status: served.status, finalURL: served.url, location: served.location ?? null }
  // Retain what was read before deciding on it, so a failing run names the
  // status, redirect target and address it actually saw.
  writeFileSync(join(root, 'build.json'), JSON.stringify(manifest, null, 2))
  // The served page must be this build's own prerendered collectives body. The
  // page's copy changes with the product, so the route is named by the page's
  // own frame rather than by a sentence beside its heading. The build id and
  // the byte hash still prove the page came from this build; the mounted body
  // and chrome are asserted by the Playwright suite this guard gates.
  const pageMarker = 'data-testid="collectives-page"'
  const expectedURL = `${appURL}/collectives`
  const html = new TextDecoder().decode(served.bytes)
  const problems = []
  if (served.status === 0) problems.push('the page load answered with an opaque redirect instead of the collectives page; the app did not accept the fixture session on this route, and this proof never follows a redirect off the app')
  else if (served.status !== 200) problems.push(`the page load of /collectives answered HTTP ${served.status}${served.location ? ` to ${served.location}` : ''} instead of being served the collectives page; a redirect means the app did not accept the fixture session on this route (src/proxy.ts sends a signed-out page load to the marketing site), and this proof reads only the app's own page`)
  else if (served.location) problems.push(`the served collectives page still named ${served.location} as a redirect target`)
  if (served.url && served.url !== expectedURL) problems.push(`the page load ended at ${served.url} rather than ${expectedURL}, so the answer cannot be this route's own HTML`)
  if (!html.includes(buildID)) problems.push(`the served HTML does not name build ${buildID}; the server is serving another build`)
  if (hash(served.bytes) !== manifest.canonicalHTMLHash) problems.push(`the served HTML differs from the prerendered .next/server/app/collectives.html (served ${hash(served.bytes)}, built ${manifest.canonicalHTMLHash})`)
  if (!html.includes(pageMarker)) problems.push(`the served HTML is not the collectives page body: it is missing ${pageMarker}`)
  if (problems.length) throw new Error(`Served canonical HTML is not this production build's collectives page: ${problems.join('; ')}. The readiness read is one authenticated page load: its cookie name is the production AUTH_COOKIE from frontend/src/lib/authCookieName.ts and its session is the same fixture the browser journey carries, so a redirect means that session or the route's policy changed. Build evidence: ${root}/build.json`)
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
