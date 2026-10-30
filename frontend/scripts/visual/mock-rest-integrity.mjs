/* REST mock for the integrity-badge visual gate.

   Serves one flagged transcript through the REAL route's reads (`GET /transcripts/{id}`, its
   `/content`, annotations, pulls, collectives) with:
     - `integrity { flagged_turns }` on the detail response, as the server sends it;
     - a `Village-Content-Hash` header on the content response equal to the row's `content_hash`, and
       `Access-Control-Expose-Headers: Village-Content-Hash`, so the browser may read it cross-origin.

   MOCK_INTEGRITY_VIEWER selects who is looking:
     public  (default) nobody signed in, reading a public transcript
     member  a signed-in collective member reading an approved share
     owner   the owner of a private transcript shared to a collective (also serves the groups the
             manage access dialog lists)

   Usage:
     MOCK_INTEGRITY_VIEWER=owner MOCK_REST_PORT=8791 node scripts/visual/mock-rest-integrity.mjs
   then build the app with NEXT_PUBLIC_API_URL=http://localhost:8791/api/v1. */
import { createServer } from 'node:http'

const PORT = Number(process.env.MOCK_REST_PORT || 8791)
const VIEWER = process.env.MOCK_INTEGRITY_VIEWER || 'public'
if (!['public', 'member', 'owner'].includes(VIEWER)) {
  console.error(`ERROR [mock-rest-integrity.mjs] MOCK_INTEGRITY_VIEWER=${JSON.stringify(VIEWER)} is not public | member | owner.`)
  process.exit(1)
}

export const ID = 'integrity-demo'
export const CONTENT_HASH = '5d41402abc4b2a76b9719d911017c5925d41402abc4b2a76b9719d911017c592'
export const FLAGGED_TURNS = [1, 3]
const GROUP = { id: '11111111-1111-4111-8111-111111111111', name: 'AI Research Team' }

const profile = (id, username) => ({ kind: 'profile', id, username, display_name: username, avatar_url: null })
const ownerUser = {
  id: 'user-owner', github_id: 1, github_username: 'alice-dev', display_name: 'alice-dev', avatar_url: null,
  created_at: '2026-06-01T12:00:00Z', updated_at: '2026-06-01T12:00:00Z', is_discoverable: true,
  username_chosen: true, provider_username: 'alice-dev',
}
const memberUser = { ...ownerUser, id: 'user-member', github_id: 2, github_username: 'bob-member', display_name: 'bob-member', provider_username: 'bob-member' }
const me = VIEWER === 'owner' ? ownerUser : VIEWER === 'member' ? memberUser : null

const turns = [
  ['user', 'why is ingest dropping commits?'],
  ['assistant', 'Looking at the commit detector now; the loop skips a merge commit whenever its parent is already known.'],
  ['user', 'can you fix it and add a test?'],
  ['assistant', 'Done: the detector now records the merge commit, and a table test covers the skipped-parent case.'],
  ['user', 'thanks, ship it'],
].map(([role, text], index) => ({
  index, role, content: text, timestamp: `2026-08-21T09:0${index}:00.000Z`, depth: 0,
}))
const content = {
  id: 'sess_integrity_0001', harness: 'claude-code',
  startTime: '2026-08-21T09:00:00.000Z', endTime: '2026-08-21T09:05:00.000Z', durationMins: 5,
  totalTokens: 900, tokensIn: 500, tokensOut: 400, turnCount: turns.length, toolCallCount: 0,
  project: 'village', model: 'anthropic/claude-fable-5', turns,
}

const detail = {
  transcript: {
    id: ID, local_id: 'sess_integrity_0001',
    visibility: VIEWER === 'public' ? 'public' : 'shared',
    title: 'Fix the dropped merge commits', description: null,
    project_name: 'village', project_display_name: 'village', project_hash: 'a'.repeat(64),
    project_name_source: 'consented', project_remote_label: '', content_hash: CONTENT_HASH,
  },
  tags: [], shares: [],
  owner: profile('user-owner', 'alice-dev'),
  enriched_shares: [{
    transcript_id: ID, group_id: GROUP.id, group_name: GROUP.name, acceptance_mode: 'open',
    status: 'approved', shared_at: '2026-08-22T00:00:00.000Z',
  }],
  integrity: { flagged_turns: FLAGGED_TURNS },
}

const send = (res, code, body, extra = {}) => {
  res.writeHead(code, {
    'content-type': 'application/json',
    'access-control-allow-origin': '*',
    'access-control-allow-headers': '*',
    'access-control-allow-methods': 'GET,POST,DELETE,OPTIONS',
    ...extra,
  })
  res.end(body == null ? '' : JSON.stringify(body))
}

const server = createServer((req, res) => {
  const url = new URL(req.url, `http://localhost:${PORT}`)
  const p = url.pathname.replace(/^\/api\/v1/, '')
  if (req.method === 'OPTIONS') return send(res, 204, null)
  console.log(`${req.method} ${url.pathname}`)
  if (req.method !== 'GET') return send(res, 405, { error: 'mock is read-only' })
  if (p === `/transcripts/${ID}`) return send(res, 200, detail)
  if (p === `/transcripts/${ID}/content`) {
    return send(res, 200, content, {
      'village-content-hash': CONTENT_HASH,
      'access-control-expose-headers': 'Village-Content-Hash',
    })
  }
  if (p === `/transcripts/${ID}/annotations`) return send(res, 200, { annotations: [] })
  if (p === `/transcripts/${ID}/pulls`) return send(res, 200, { pull_requests: [] })
  if (p === `/transcripts/${ID}/attestations`) return send(res, 200, [])
  if (p === `/transcripts/${ID}/collectives`) {
    const rows = VIEWER === 'public' ? [] : [{ ...GROUP, description: null, linked_github_org: null, shared_at: '2026-08-22T00:00:00.000Z' }]
    return send(res, 200, { collectives: rows })
  }
  if (p === '/auth/me') return me ? send(res, 200, me) : send(res, 401, { error: 'not signed in (mock)' })
  if (p === '/auth/orgs') return send(res, 200, [])
  if (p === '/collectives') {
    return me ? send(res, 200, [{ id: GROUP.id, name: GROUP.name, acceptance_mode: 'open', member_count: 3, role: 'member' }]) : send(res, 401, { error: 'not signed in (mock)' })
  }
  if (p === '/users/me/collectives/contributions') {
    return send(res, 200, { collectives: [{ id: GROUP.id, name: GROUP.name, approved_count: 1, pending_count: 0, rejected_attempt_count: 0, withdrawn_attempt_count: 0 }] })
  }
  if (p === `/collectives/${GROUP.id}/my-shares`) return send(res, 200, [{ id: ID, status: 'approved' }])
  return send(res, 401, { error: 'unauthenticated (mock)' })
})

server.on('error', (err) => {
  console.error(`ERROR [mock-rest-integrity.mjs] could not listen on ${PORT}: ${err.code === 'EADDRINUSE' ? 'port already in use; a capture would screenshot whatever owns it' : err.message}`)
  process.exit(1)
})
server.listen(PORT, () => console.log(`mock-rest-integrity: viewer=${VIEWER} transcript "${ID}" on http://localhost:${PORT}/api/v1`))
