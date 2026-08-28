/* Minimal REST mock for the signed-in home page's visual gate.

   The home route at `/` fetches:
     - GET /api/v1/auth/me                     (who is looking; decides `/` serves home, not explore)
     - GET /api/v1/transcripts?owner=...       (the caller's own sessions, grouped into projects)

   The dataset exercises what the page has to get right in one capture: several
   sessions across THREE projects with different session counts and different
   most-recent timestamps, so both orders on the page are visible — recent
   sessions most-recent-first, and projects most-recently-worked-first.

   Usage:
     MOCK_REST_PORT=8791 node scripts/visual/mock-rest-home.mjs
     MOCK_REST_PORT=8791 MOCK_SIGNED_OUT=1 node scripts/visual/mock-rest-home.mjs
     MOCK_REST_PORT=8791 MOCK_OWNER_LIST_FAILS=1 node scripts/visual/mock-rest-home.mjs
*/
import { createServer } from 'node:http'

const PORT = Number(process.env.MOCK_REST_PORT || 8791)
// When set, `/auth/me` refuses, so the root route serves discovery instead of
// home. That is the signed-out arm of the same capture, and it cannot be taken
// against a mock that answers `/auth/me` for everyone.
const SIGNED_OUT = process.env.MOCK_SIGNED_OUT === '1'
// When set, the OWNER-SCOPED list request fails while everything else still
// answers, so the capture shows the home page's failure surface rather than a
// whole broken app. The distinction is the point: a failed list is not an empty
// library, and only a capture where the rest of the page works can show that.
const OWNER_LIST_FAILS = process.env.MOCK_OWNER_LIST_FAILS === '1'

const user = {
  id: 'user-demo',
  github_id: 123456,
  github_username: 'alice-dev',
  display_name: 'Alice Developer',
  avatar_url: null,
  created_at: '2026-06-01T12:00:00Z',
  updated_at: '2026-06-01T12:00:00Z',
  is_discoverable: true,
  // Without these, the handle gate mounted for every page renders the "claim
  // your handle" onboarding screen instead of the route under capture.
  username_chosen: true,
  provider_username: 'alice-dev',
}

const HASH_VILLAGE = '1'.repeat(64)
const HASH_PEASANT = '2'.repeat(64)
const HASH_SCHEMA = '3'.repeat(64)

const sessions = [
  ['a1', 'Group a project contribution by session', HASH_VILLAGE, 'village', '2026-08-27T14:10:00Z', 'claude-code'],
  ['a2', 'Publish wizard: consent and receipt copy', HASH_PEASANT, 'peasant', '2026-08-26T09:30:00Z', 'claude-code'],
  ['a3', 'Governance audit trigger, fail closed', HASH_VILLAGE, 'village', '2026-08-25T17:45:00Z', 'opencode'],
  ['a4', 'Observed model on the session stats', HASH_SCHEMA, 'schema', '2026-08-24T11:00:00Z', 'codex'],
  ['a5', 'License menu widening, both checks', HASH_VILLAGE, 'village', '2026-08-22T08:15:00Z', 'gemini-cli'],
  ['a6', 'Kickstart selection preview', HASH_PEASANT, 'peasant', '2026-08-20T19:05:00Z', 'claude-code'],
  ['a7', 'Contract freshness gate', HASH_SCHEMA, 'schema', '2026-08-18T13:20:00Z', 'claude-code'],
]

const toListItem = ([id, title, hash, project, publishedAt, provider]) => ({
  transcript: {
    id,
    owner_id: user.id,
    local_id: id,
    title,
    description: null,
    visibility: 'public',
    model_provider: provider,
    model_name: 'claude-opus-4-8',
    harness_version: '2026.08',
    session_start: publishedAt,
    session_end: publishedAt,
    turn_count: 24,
    token_count: 120000,
    blob_size_bytes: 0,
    schema_version: '0.13.0',
    published_at: publishedAt,
    updated_at: publishedAt,
    parent_session_id: null,
    ingested_at: publishedAt,
    source_format: 'json',
    git_branch: 'develop',
    git_remote: null,
    project_hash: hash,
    project_name: project,
    project_display_name: project,
    project_name_source: 'consented',
    project_remote_label: `github.com:peasant-labs/${project}`,
    tool_call_count: 12,
    subagent_count: 0,
    duration_ms: 1_800_000,
    session_origin: 'user',
  },
  tags: [],
  owner: user,
})

const send = (res, status, body) => {
  res.writeHead(status, {
    'content-type': 'application/json',
    'access-control-allow-origin': '*',
    'access-control-allow-headers': '*',
    'access-control-allow-methods': 'GET,POST,DELETE,OPTIONS',
  })
  res.end(body == null ? '' : JSON.stringify(body))
}

const server = createServer((req, res) => {
  const url = new URL(req.url, `http://localhost:${PORT}`)
  const path = url.pathname.replace(/^\/api\/v1/, '')
  if (req.method === 'OPTIONS') return send(res, 204, null)
  console.log(`${req.method} ${url.pathname}${url.search}`)

  if (req.method !== 'GET') return send(res, 404, { error: `no mock route for ${req.method} ${url.pathname}` })

  if (path === '/auth/me') {
    if (SIGNED_OUT) return send(res, 401, { error: 'not signed in' })
    return send(res, 200, user)
  }
  if (path === '/auth/orgs') return send(res, 200, [])
  if (path === '/groups') return send(res, 200, [])
  if (path.startsWith('/tags/popular')) return send(res, 200, [])
  if (path === '/groups/search') return send(res, 200, { collectives: [] })

  if (path === '/transcripts') {
    // The home page asks for its own rows. An unscoped request is discovery's,
    // and is answered empty so a capture cannot silently pass on the wrong list.
    const owner = url.searchParams.get('owner')
    if (owner === user.github_username && OWNER_LIST_FAILS) {
      return send(res, 500, { error: 'the session list is unavailable' })
    }
    const rows = owner === user.github_username ? sessions.map(toListItem) : []
    return send(res, 200, {
      transcripts: rows,
      total: rows.length,
      agent_total: 0,
      page: Number(url.searchParams.get('page') || '1'),
      limit: Number(url.searchParams.get('limit') || '24'),
    })
  }
  if (/^\/users\/[^/]+$/.test(path)) return send(res, 200, user)

  return send(res, 404, { error: `no mock route for ${req.method} ${url.pathname}` })
})

// A busy port is the one failure that silently produces a WRONG capture: the
// app then talks to whatever else is listening, renders somebody else's
// fixture, and the shoot still passes its provenance checks because the page
// really does carry the home surface. Refuse loudly instead.
server.on('error', (err) => {
  if (err.code === 'EADDRINUSE') {
    console.error(
      `ERROR [mock-rest-home.mjs] port ${PORT} is already in use.
  What failed: another process is listening on ${PORT}.
  Why: a second capture run, or another surface's mock, claimed it first.
  Where: mock-rest-home.mjs startup.
  Means: the app would reach THAT server instead, and the capture would show its fixture.
  Fix: choose a free port with MOCK_REST_PORT and point NEXT_PUBLIC_API_URL at the same one.`,
    )
    process.exit(2)
  }
  throw err
})

server.listen(PORT, () => {
  console.log(`mock-rest-home: serving signed-in home fixtures on http://localhost:${PORT}/api/v1`)
})
