/* Public transcript mock. Content comes from the mounted reader fixture. */
import { readFileSync } from 'node:fs'
import { parse } from 'yaml'
import { createServer } from 'node:http'

const PORT = Number(process.env.MOCK_REST_PORT || 8791)
const VIEWER = process.env.MOCK_INTEGRITY_VIEWER || 'public'
if (!['public', 'member', 'owner'].includes(VIEWER)) {
  console.error(`ERROR [mock-rest-published-images.mjs] MOCK_INTEGRITY_VIEWER=${JSON.stringify(VIEWER)} is not public | member | owner.`)
  process.exit(1)
}

export const ID = 'omitted-images'
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

const fixture = parse(readFileSync(new URL('../../src/testdata/published-image-reader.yaml', import.meta.url), 'utf8'))
const cases = fixture.cases.filter((entry) => entry.name === 'two-published-images')
if (cases.length !== 1) throw new Error('published reader fixture must contain exactly one two-published-images case')
const content = JSON.parse(cases[0].content)

const detail = {
  transcript: {
    id: ID, local_id: content.sessionDetail.id, model_provider: 'codex',
    visibility: VIEWER === 'public' ? 'public' : 'shared',
    title: 'Published images', description: null,
    project_name: 'village', project_display_name: 'village', project_hash: 'a'.repeat(64),
    project_name_source: 'consented', project_remote_label: '', content_hash: CONTENT_HASH,
  },
  tags: [], shares: [],
  owner: profile('user-owner', 'alice-dev'),
  enriched_shares: [{
    transcript_id: ID, group_id: GROUP.id, group_name: GROUP.name, acceptance_mode: 'open',
    status: 'approved', shared_at: '2026-08-22T00:00:00.000Z',
  }],

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
  console.error(`ERROR [mock-rest-published-images.mjs] could not listen on ${PORT}: ${err.code === 'EADDRINUSE' ? 'port already in use; a capture would screenshot whatever owns it' : err.message}`)
  process.exit(1)
})
server.listen(PORT, () => console.log(`mock-rest-published-images: viewer=${VIEWER} transcript "${ID}" on http://localhost:${PORT}/api/v1`))
