/* REST mock for the positioned-transcript visual gate.

   Advertises positioned reads on `/schema/version` and serves the positioned transcripts
   (scripts/journey/lib/positioned-transcript-fixtures.mjs, whose bodies all come from the one shared
   builder, scripts/journey/lib/positioned-bodies.mjs). Everything else, the signed-in viewer and the
   demo collective's contribute and review pages, is the bundled transcript mock
   (scripts/visual/mock-rest.mjs), spawned behind an internal port. The collective's contributable
   list and review queue lead with the positioned preview transcript, so both preview columns read
   through the positioned reader.

   MOCK_POSITIONED_WORLD selects the world (served | revoked | withheld; default served).
   POST /__mock/positioned {"world":"..."} switches it while running.

   usage:
     MOCK_REST_PORT=8795 node scripts/visual/mock-rest-positioned.mjs
   then build and start the app with NEXT_PUBLIC_API_URL=http://localhost:8795/api/v1. */
import { createServer, request as httpRequest } from 'node:http'
import { spawn } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import { handlePositionedRequest, HttpMethod, POSITIONED, PositionedWorld } from '../journey/lib/positioned-transcript-fixtures.mjs'

const WHERE = 'mock-rest-positioned.mjs'
const PORT = Number(process.env.MOCK_REST_PORT || 8795)
const UPSTREAM_PORT = PORT + 1
const GROUP = process.env.MOCK_GROUP_ID || 'demo-group'
const WORLDS = Object.values(PositionedWorld)
let world = process.env.MOCK_POSITIONED_WORLD || PositionedWorld.Served
if (!WORLDS.includes(world)) {
  console.error(`ERROR [${WHERE}] MOCK_POSITIONED_WORLD=${JSON.stringify(world)} is not one of ${WORLDS.join(' | ')}.`)
  process.exit(1)
}

const upstream = spawn(process.execPath, [fileURLToPath(new URL('./mock-rest.mjs', import.meta.url))], {
  env: { ...process.env, MOCK_REST_PORT: String(UPSTREAM_PORT), MOCK_GROUP_ID: GROUP },
  stdio: ['ignore', 'ignore', 'inherit'],
})
const stop = () => { try { upstream.kill('SIGTERM') } catch { /* already gone */ } }
process.on('exit', stop)
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => process.exit(0))

const send = (res, status, body) => {
  res.writeHead(status, { 'content-type': 'application/json', 'access-control-allow-origin': '*', 'access-control-allow-headers': '*' })
  res.end(JSON.stringify(body))
}

/** Read an upstream answer whole. */
function fetchUpstream(req, path = req.url) {
  return new Promise((resolve, reject) => {
    const forward = httpRequest({ hostname: '127.0.0.1', port: UPSTREAM_PORT, path, method: req.method, headers: req.headers }, (up) => {
      const chunks = []
      up.on('data', (chunk) => chunks.push(chunk))
      up.on('end', () => resolve({ status: up.statusCode || 502, headers: up.headers, body: Buffer.concat(chunks) }))
    })
    forward.on('error', reject)
    req.pipe(forward)
  })
}

/** The preview transcript stands in for the first row of the contributable list and the review queue. */
const preview = POSITIONED.preview
const leadWithPreview = {
  [`/collectives/${GROUP}/contributable`]: (body) => {
    const [first, ...rest] = body.transcripts
    return { ...body, transcripts: [{ ...first, id: preview.transcriptId, title: preview.title }, ...rest] }
  },
  [`/collectives/${GROUP}/pending`]: (body) => {
    const [first, ...rest] = body
    return [{ ...first, transcript_id: preview.transcriptId, title: preview.title }, ...rest]
  },
}

async function waitForUpstream(timeoutMs = 15000) {
  const started = Date.now()
  while (Date.now() - started < timeoutMs) {
    const up = await new Promise((resolve) => {
      const probe = httpRequest({ hostname: '127.0.0.1', port: UPSTREAM_PORT, path: '/', method: HttpMethod.Get }, (r) => { r.resume(); resolve(true) })
      probe.on('error', () => resolve(false))
      probe.end()
    })
    if (up) return true
    await new Promise((resolve) => setTimeout(resolve, 150))
  }
  return false
}
if (!(await waitForUpstream())) {
  console.error(`ERROR [${WHERE}] the bundled transcript mock never answered on 127.0.0.1:${UPSTREAM_PORT}; check that the port is free and scripts/visual/mock-rest.mjs starts on its own.`)
  stop()
  process.exit(2)
}

const server = createServer(async (req, res) => {
  const path = new URL(req.url, 'http://fixture').pathname.replace(/^\/api\/v1/, '')
  if (req.method === 'OPTIONS') return send(res, 204, {})
  if (path === '/__mock/positioned' && req.method === HttpMethod.Post) {
    let text = ''
    for await (const chunk of req) text += chunk
    const next = JSON.parse(text || '{}').world
    if (!WORLDS.includes(next)) return send(res, 400, { error: `world must be one of ${WORLDS.join(' | ')}` })
    world = next
    return send(res, 200, { world })
  }
  if (await handlePositionedRequest(req, res, world)) return
  try {
    const answer = await fetchUpstream(req)
    const rewrite = req.method === HttpMethod.Get && answer.status === 200 ? leadWithPreview[path] : undefined
    if (rewrite) return send(res, 200, rewrite(JSON.parse(answer.body.toString('utf8'))))
    res.writeHead(answer.status, answer.headers)
    res.end(answer.body)
  } catch {
    send(res, 502, { error: 'the bundled transcript mock is unavailable' })
  }
})

server.on('error', (err) => {
  console.error(`ERROR [${WHERE}] could not listen on ${PORT}: ${err.message}. Stop the process holding the port, or set MOCK_REST_PORT to a free one.`)
  stop()
  process.exit(1)
})
server.listen(PORT, () => console.log(`mock-rest-positioned: ${world} world on http://localhost:${PORT}/api/v1 (bundled mock on :${UPSTREAM_PORT})`))
