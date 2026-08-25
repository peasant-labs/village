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
const STRICT = '88888888-8888-4888-8888-888888888888'

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
  // Refused three times and then withdrawn. The refusals stay counted from the
  // event ledger while both transcript counters fall to zero, and the current
  // state row is gone — so opening this collective lists no submissions and
  // its history has no entry point. That dead end is real and is captured here
  // rather than described only in prose.
  {
    id: STRICT,
    name: 'Strict Curators',
    description: 'refused three times, then withdrawn',
    linked_github_org: null,
    approved_count: 0,
    pending_count: 0,
    rejected_attempt_count: 3,
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
  [STRICT]: [],
}

// One attempt row. `recorded` is when the attempt was opened and `decided` when
// it was closed (null while it is still open). Both are given per row: a shared
// constant would make every still-open row show the same instant and the log
// would appear to run backwards, which is a defect in the FIXTURE and would
// make the capture misleading evidence.
const event = (num, status, actor, recorded, decided) => ({
  event_num: num,
  status,
  recorded_at: recorded,
  decided_at: decided,
  decided_by_actor: actor,
})

// The full five-state history: submitted, refused, submitted again, accepted,
// withdrawn by the owner, then removed by the collective. Every row names the
// actor class, and no row reads as a re-submission of a withdrawal.
const eventsByGroupAndTranscript = {
  // All five states in the order they happened, ending open. One row per
  // submission: a row is opened pending and closed in place, except a
  // withdrawal of an ACCEPTED contribution, which appends its own row so the
  // acceptance stays on record. Times increase down the log.
  [`${RESEARCH}/9a1c4e21`]: [
    event(1, 'rejected', 'moderator', '2026-08-01T09:00:00Z', '2026-08-02T10:00:00Z'),
    event(2, 'approved', 'moderator', '2026-08-03T08:15:00Z', '2026-08-04T14:30:00Z'),
    event(3, 'revoked', 'collective', '2026-08-05T11:00:00Z', '2026-08-05T11:00:00Z'),
    event(4, 'retracted', 'owner', '2026-08-06T07:05:00Z', '2026-08-06T08:20:00Z'),
    event(5, 'pending', '', '2026-08-07T09:40:00Z', null),
  ],
  [`${RESEARCH}/4f77b0c3`]: [
    event(1, 'rejected', 'moderator', '2026-07-20T13:00:00Z', '2026-07-22T09:10:00Z'),
    event(2, 'approved', 'moderator', '2026-07-25T10:30:00Z', '2026-07-26T15:45:00Z'),
  ],
  [`${RESEARCH}/c02d5188`]: [
    event(1, 'approved', 'moderator', '2026-07-11T08:00:00Z', '2026-07-11T17:20:00Z'),
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
