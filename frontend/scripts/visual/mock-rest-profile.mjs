/* Minimal REST mock for the profile contributed-collectives visual gate.

   The own-profile route fetches:
     - GET /api/v1/auth/me                                  (who is looking)
     - GET /api/v1/users/{username}                         (the public profile)
     - GET /api/v1/transcripts?owner=...                    (the library)
     - GET /api/v1/users/me/collectives/contributions       (the section)
   and, once a collective and then a submission are opened:
     - GET /api/v1/groups/{id}/my-shares
     - GET /api/v1/users/me/collectives/{groupId}/transcripts/{transcriptId}/events

   The dataset is chosen to exercise the cases the section has to get right in
   one capture: a collective with approved, awaiting-review AND repeatedly
   refused submissions, a collective holding only submissions awaiting review,
   and an event history containing all five states so the actor labels are
   visible.

   Usage:
     MOCK_REST_PORT=8790 node scripts/visual/mock-rest-profile.mjs
*/
import { createServer } from 'node:http'

const PORT = Number(process.env.MOCK_REST_PORT || 8790)

const user = {
  id: 'user-demo',
  github_id: 123456,
  github_username: 'alice-dev',
  display_name: 'Alice Developer',
  avatar_url: null,
  created_at: '2026-06-01T12:00:00Z',
  updated_at: '2026-06-01T12:00:00Z',
  is_discoverable: true,
  username_chosen: true,
  provider_username: 'alice-dev',
}

const RESEARCH = '22222222-2222-4222-8222-222222222222'
const QUIET = '11111111-1111-4111-8111-111111111111'
const FORMER = '33333333-3333-4333-8333-333333333333'

const contributions = [
  {
    id: RESEARCH,
    name: 'AI Research Team',
    description: 'transcripts of applied model work, reviewed before publication',
    linked_github_org: 'ai-research',
    approved_count: 2,
    pending_count: 1,
    rejected_attempt_count: 2,
  },
  {
    id: QUIET,
    name: 'Quiet Reviewers',
    description: null,
    linked_github_org: null,
    approved_count: 0,
    pending_count: 3,
    rejected_attempt_count: 0,
  },
  {
    id: FORMER,
    name: 'Former Home',
    description: 'everything offered here was withdrawn again',
    linked_github_org: null,
    approved_count: 0,
    pending_count: 0,
    rejected_attempt_count: 0,
  },
]

const share = (id, title, status) => ({
  id,
  title,
  model_provider: 'claude-code',
  model_name: 'Claude Opus 4.5',
  visibility: 'public',
  published_at: '2026-08-01T09:00:00Z',
  turn_count: 42,
  tokens_in: 91000,
  tokens_out: 12000,
  status,
  shared_at: '2026-08-01T09:00:00Z',
})

const sharesByGroup = {
  [RESEARCH]: [
    share('9a1c4e21', 'Rewriting the ingest commit detector', 'pending'),
    share('4f77b0c3', 'Tracing a redaction rule regression', 'approved'),
    share('c02d5188', 'Measuring push latency across regions', 'approved'),
  ],
  [QUIET]: [
    share('7b3e9d04', 'Reading the store migration ledger', 'pending'),
    share('1d8a6c55', 'A first pass at the pull contract', 'pending'),
    share('e5b21f70', 'Notes on the redaction category split', 'pending'),
  ],
  [FORMER]: [],
}

const event = (num, status, actor, decided) => ({
  event_num: num,
  status,
  recorded_at: '2026-08-01T09:00:00Z',
  decided_at: decided,
  decided_by_actor: actor,
})

// The full five-state history: submitted, refused, submitted again, accepted,
// withdrawn by the owner, then removed by the collective. Every row names the
// actor class, and no row reads as a re-submission of a withdrawal.
const eventsByGroupAndTranscript = {
  // All five states, in the order they happened, ending open — so the capture
  // shows a refusal, a withdrawal by the owner and a removal by the collective
  // side by side, each naming its own actor.
  [`${RESEARCH}/9a1c4e21`]: [
    event(1, 'pending', '', null),
    event(2, 'rejected', 'moderator', '2026-08-02T10:00:00Z'),
    event(3, 'pending', '', null),
    event(4, 'approved', 'moderator', '2026-08-04T14:30:00Z'),
    event(5, 'revoked', 'collective', '2026-08-05T11:00:00Z'),
    event(6, 'pending', '', null),
    event(7, 'retracted', 'owner', '2026-08-06T08:20:00Z'),
    event(8, 'pending', '', null),
  ],
  [`${RESEARCH}/4f77b0c3`]: [
    event(1, 'pending', '', null),
    event(2, 'approved', 'moderator', '2026-08-03T11:15:00Z'),
    event(3, 'retracted', 'owner', '2026-08-05T08:00:00Z'),
    event(4, 'pending', '', null),
    event(5, 'approved', 'moderator', '2026-08-06T09:45:00Z'),
  ],
  [`${RESEARCH}/c02d5188`]: [
    event(1, 'pending', '', null),
    event(2, 'approved', 'moderator', '2026-08-07T16:20:00Z'),
    event(3, 'revoked', 'collective', '2026-08-09T12:00:00Z'),
  ],
}

const send = (res, status, body) => {
  res.writeHead(status, {
    'content-type': 'application/json',
    'access-control-allow-origin': '*',
    'access-control-allow-headers': '*',
  })
  res.end(body == null ? '' : JSON.stringify(body))
}

const server = createServer((req, res) => {
  const url = new URL(req.url, `http://localhost:${PORT}`)
  const path = url.pathname.replace(/^\/api\/v1/, '')
  if (req.method === 'OPTIONS') return send(res, 204, null)
  console.log(`${req.method} ${url.pathname}`)

  if (req.method !== 'GET') return send(res, 404, { error: `no mock route for ${req.method} ${url.pathname}` })

  if (path === '/auth/me') return send(res, 200, user)
  if (path === '/auth/orgs') return send(res, 200, [])
  if (path === '/groups') return send(res, 200, [])
  if (path === '/users/me/collectives/contributions') return send(res, 200, { collectives: contributions })

  const events = path.match(/^\/users\/me\/collectives\/([^/]+)\/transcripts\/([^/]+)\/events$/)
  if (events) return send(res, 200, eventsByGroupAndTranscript[`${events[1]}/${events[2]}`] ?? [])

  const shares = path.match(/^\/groups\/([^/]+)\/my-shares$/)
  if (shares) return send(res, 200, sharesByGroup[shares[1]] ?? [])

  if (path === '/transcripts') {
    return send(res, 200, {
      transcripts: [],
      total: 0,
      agent_total: 0,
      page: Number(url.searchParams.get('page') || '1'),
      limit: Number(url.searchParams.get('limit') || '24'),
    })
  }
  if (/^\/users\/[^/]+$/.test(path)) return send(res, 200, user)

  return send(res, 404, { error: `no mock route for ${req.method} ${url.pathname}` })
})

server.listen(PORT, () => {
  console.log(`mock-rest-profile: serving profile fixtures on http://localhost:${PORT}/api/v1`)
})
