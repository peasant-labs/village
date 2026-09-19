/* Composed journey mock: one command, one port, no env.
 *
 * Composes the explore browse fixtures (in-process, imported from
 * scripts/visual/mock-rest-explore.mjs) with the transcript-detail fixtures
 * (spawned from scripts/visual/mock-rest.mjs behind an internal port). Journeys
 * therefore need no per-area mock selection and no port matrix; `pnpm journey`
 * is the whole interface.
 *
 * Scenario control: POST /__mock/scenario {"name":"..."} lets a journey declare
 * the world it needs without an env var or a restart. Today:
 *   default  - the composed fixtures as-is
 *   empty    - the browse list answers with no rows (empty-state journeys)
 *
 * The transcript half is proxied rather than imported because its contract
 * fixtures are large and stateful; proxying keeps it byte-for-byte the mock the
 * Puppeteer shoots already trust.
 */
import { createServer, request as httpRequest } from 'node:http'
import { spawn } from 'node:child_process'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { handleExploreRequest } from '../visual/mock-rest-explore.mjs'

const PORT = Number(process.env.MOCK_REST_PORT || 8799)
const TRANSCRIPT_PORT = Number(process.env.JOURNEY_TRANSCRIPT_PORT || PORT + 1)
const TRANSCRIPT_SCRIPT = join(dirname(fileURLToPath(import.meta.url)), '..', 'visual', 'mock-rest.mjs')

let scenario = 'default'

const send = (res, code, body) => {
  res.writeHead(code, {
    'content-type': 'application/json',
    'access-control-allow-origin': '*',
    'access-control-allow-headers': '*',
    'access-control-allow-methods': 'GET,POST,DELETE,OPTIONS',
  })
  res.end(body == null ? '' : JSON.stringify(body))
}

const readBody = (req) =>
  new Promise((resolve) => {
    let data = ''
    req.on('data', (chunk) => { data += chunk })
    req.on('end', () => resolve(data))
  })

const proxyToTranscript = (req, res) => {
  const upstream = httpRequest(
    {
      hostname: '127.0.0.1',
      port: TRANSCRIPT_PORT,
      path: req.url,
      method: req.method,
      headers: req.headers,
    },
    (up) => {
      res.writeHead(up.statusCode || 502, up.headers)
      up.pipe(res)
    },
  )
  upstream.on('error', () => send(res, 502, { error: 'transcript mock unavailable' }))
  req.pipe(upstream)
}

const transcriptIsUp = () =>
  new Promise((resolve) => {
    const probe = httpRequest(
      { hostname: '127.0.0.1', port: TRANSCRIPT_PORT, path: '/api/v1/transcripts/demo', method: 'GET' },
      (resp) => { resp.resume(); resolve(true) },
    )
    probe.on('error', () => resolve(false))
    probe.end()
  })

const waitForTranscript = async (timeoutMs = 15000) => {
  const started = Date.now()
  while (Date.now() - started < timeoutMs) {
    if (await transcriptIsUp()) return true
    await new Promise((resolve) => setTimeout(resolve, 150))
  }
  return false
}

const transcript = spawn(process.execPath, [TRANSCRIPT_SCRIPT], {
  env: { ...process.env, MOCK_REST_PORT: String(TRANSCRIPT_PORT) },
  stdio: ['ignore', 'ignore', 'inherit'],
})
const stop = () => { try { transcript.kill('SIGTERM') } catch { /* already gone */ } }
process.on('exit', stop)

if (!(await waitForTranscript())) {
  console.error(`ERROR [journey mock] the transcript-detail mock never answered on 127.0.0.1:${TRANSCRIPT_PORT}.`)
  stop()
  process.exit(2)
}

const server = createServer(async (req, res) => {
  const url = new URL(req.url, `http://localhost:${PORT}`)
  const path = url.pathname.replace(/^\/api\/v1/, '')
  if (req.method === 'OPTIONS') return send(res, 204, null)

  if (path === '/__mock/scenario') {
    if (req.method === 'GET') return send(res, 200, { scenario })
    if (req.method === 'POST') {
      const body = await readBody(req)
      try {
        scenario = JSON.parse(body || '{}').name || scenario
      } catch {
        return send(res, 400, { error: 'invalid JSON body' })
      }
      return send(res, 200, { scenario })
    }
  }

  if (scenario === 'empty' && req.method === 'GET' && path === '/transcripts') {
    return send(res, 200, { transcripts: [], total: 0, agent_total: 0, page: 1, limit: 24 })
  }

  // Transcript detail and its subroutes go to the transcript mock; the list
  // (`/transcripts`, exact) stays with the explore half.
  if (path.startsWith('/transcripts/')) return proxyToTranscript(req, res)

  if (handleExploreRequest(req, res)) return
  return send(res, 404, { error: `no mock route for ${req.method} ${url.pathname}` })
})

server.on('error', (err) => {
  if (err.code === 'EADDRINUSE') {
    console.error(
      `ERROR [journey mock] port ${PORT} is already in use; a stale server is serving there. ` +
      `Kill it (pkill -f 'scripts/journey/mock.mjs') or set MOCK_REST_PORT to a free port.`,
    )
    stop()
    process.exit(2)
  }
  throw err
})

server.listen(PORT, () => {
  console.log(`journey mock: browse + transcript fixtures on http://localhost:${PORT}/api/v1 (transcript on :${TRANSCRIPT_PORT})`)
})