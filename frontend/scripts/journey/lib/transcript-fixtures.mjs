/* Transcript-page fixtures for the journey harness: a transcript the journey's
 * signed-in viewer owns.
 *
 * The composed mock's `/auth/me` answers as `user-demo` (alice-dev), so this
 * transcript names that user as its owner: the header offers `manage access`
 * and `edit title`, and the owner's popup reads and changes who can read it.
 * It is bound to five pull requests, more than the page shows before
 * `show all`. Its turns are the transcript-detail mock's own content, served
 * through `contentPath`, so the viewer below the header is the real one.
 *
 * `handleOwnedTranscriptRequest` composes into `scripts/journey/mock.mjs` the
 * same way `handleProjectRequest` does: it returns true when it served the
 * request. The share routes change the served audience in place, the way the
 * server's reads answer after a share or a withdrawal; `resetOwnedTranscript`
 * restores it whenever a journey sets a scenario.
 */

const PORT = Number(process.env.JOURNEY_MOCK_PORT || 8799)

export const OWNED_TRANSCRIPT = {
  id: '3f9c0a17-5b2e-4c1d-9a8f-2d6e7b40e21a',
  title: 'Fix flaky ingest test',
  ownerId: 'user-demo',
  ownerUsername: 'alice-dev',
  /** The transcript-detail mock's content, served for this id. */
  contentPath: '/api/v1/transcripts/demo/content',
}

/** The collectives the owner belongs to: the picker's pool. */
const OWNER_GROUPS = [
  { id: '11111111-1111-4111-8111-111111111111', name: 'Acme Platform', acceptance_mode: 'open', member_count: 12 },
  { id: '22222222-2222-4222-8222-222222222222', name: 'Acme Company', acceptance_mode: 'open', member_count: 87 },
  { id: '44444444-4444-4444-8444-444444444444', name: 'ML Reading Group', acceptance_mode: 'curated', member_count: 21 },
]

const INITIAL_READERS = ['11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222']

export const OWNED_PULL_REQUESTS = [
  { owner: 'acme', name: 'ingest-api', number: 42, title: 'Fix flaky ingest test', head_ref: 'fix/flaky-ingest', state: 'attached' },
  { owner: 'acme', name: 'ingest-api', number: 45, title: 'Retry with the injected clock', head_ref: 'fix/retry-clock', state: 'attached' },
  { owner: 'acme', name: 'web', number: 31, title: null, head_ref: null, state: 'detached' },
  { owner: 'acme', name: 'ingest-api', number: 51, title: 'Drop the sleep', head_ref: 'chore/no-sleep', state: 'attached' },
  { owner: 'acme', name: 'ingest-api', number: 60, title: 'Bump the ingest client', head_ref: 'deps/ingest', state: 'attached' },
]

let readers = [...INITIAL_READERS]
let pending = []

export function resetOwnedTranscript() {
  readers = [...INITIAL_READERS]
  pending = []
}

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

const groupById = (id) => OWNER_GROUPS.find((g) => g.id === id)

function metadata() {
  return {
    transcript: {
      id: OWNED_TRANSCRIPT.id,
      owner_id: OWNED_TRANSCRIPT.ownerId,
      local_id: 'sess_owned_0001',
      visibility: readers.length || pending.length ? 'shared' : 'private',
      title: OWNED_TRANSCRIPT.title,
      description: null,
      project_name: 'ingest-api',
      project_hash: '5'.repeat(64),
      project_display_name: 'ingest-api',
      project_name_source: 'consented',
      project_remote_label: 'github.com/acme/ingest-api',
      model_provider: 'claude-code',
    },
    tags: [],
    shares: [],
    enriched_shares: readers.map((id) => ({
      transcript_id: OWNED_TRANSCRIPT.id,
      group_id: id,
      group_name: groupById(id).name,
      acceptance_mode: groupById(id).acceptance_mode,
      status: 'approved',
      shared_at: '2026-09-01T00:00:00Z',
    })),
    owner: { id: OWNED_TRANSCRIPT.ownerId, github_username: OWNED_TRANSCRIPT.ownerUsername },
    attestations: [],
  }
}

/** Serves the owned transcript's reads and its share routes. `ownerGroups`
 *  says whether `GET /groups` answers with the owner's collectives: only the
 *  transcript-owner scenario asks for it, so other journeys keep the explore
 *  half's empty list. */
export function handleOwnedTranscriptRequest(req, res, { ownerGroups }) {
  const url = new URL(req.url, `http://localhost:${PORT}`)
  const path = url.pathname.replace(/^\/api\/v1/, '')
  const base = `/transcripts/${OWNED_TRANSCRIPT.id}`

  if (ownerGroups && req.method === 'GET' && path === '/groups') {
    send(res, 200, OWNER_GROUPS.map((g) => ({ ...g, role: 'member', member_since: '2026-01-01T00:00:00Z' })))
    return true
  }
  if (ownerGroups && req.method === 'GET' && path === '/users/me/collectives/contributions') {
    send(res, 200, { collectives: OWNER_GROUPS.filter((g) => readers.includes(g.id) || pending.includes(g.id))
      .map((g) => ({ id: g.id, name: g.name, approved_count: readers.includes(g.id) ? 1 : 0,
        pending_count: pending.includes(g.id) ? 1 : 0, rejected_attempt_count: 0, withdrawn_attempt_count: 0 })) })
    return true
  }
  const ownShares = path.match(/^\/groups\/([^/]+)\/my-shares$/)
  if (ownerGroups && req.method === 'GET' && ownShares) {
    const id = ownShares[1]
    send(res, 200, readers.includes(id) || pending.includes(id)
      ? [{ id: OWNED_TRANSCRIPT.id, status: readers.includes(id) ? 'approved' : 'pending' }] : [])
    return true
  }
  if (!path.startsWith(base)) return false

  if (req.method === 'GET' && path === base) {
    send(res, 200, metadata())
    return true
  }
  if (req.method === 'GET' && path === `${base}/annotations`) {
    send(res, 200, { annotations: [] })
    return true
  }
  if (req.method === 'GET' && path === `${base}/collectives`) {
    send(res, 200, {
      collectives: readers.map((id) => ({
        id,
        name: groupById(id).name,
        description: null,
        linked_github_org: null,
        shared_at: '2026-09-01T00:00:00Z',
      })),
    })
    return true
  }
  if (req.method === 'GET' && path === `${base}/pulls`) {
    send(res, 200, { pull_requests: OWNED_PULL_REQUESTS })
    return true
  }
  if (req.method === 'POST' && path === `${base}/share`) {
    readBody(req).then((body) => {
      const { group_ids = [] } = JSON.parse(body || '{}')
      for (const id of group_ids) {
        const group = groupById(id)
        if (!group || readers.includes(id) || pending.includes(id)) continue
        if (group.acceptance_mode === 'curated') pending.push(id)
        else readers.push(id)
      }
      send(res, 200, [...readers, ...pending].map((id) => ({ group_id: id, group_name: groupById(id).name, shared_at: '2026-09-01T00:00:00Z' })))
    })
    return true
  }
  const unshare = path.match(new RegExp(`^${base}/share/([^/]+)$`))
  if (req.method === 'DELETE' && unshare) {
    readers = readers.filter((id) => id !== unshare[1])
    pending = pending.filter((id) => id !== unshare[1])
    send(res, 200, { status: 'unshared' })
    return true
  }
  return false
}
