/* Minimal REST mock for the profile contributed-collectives visual gate.

   The own-profile route fetches:
     - GET /api/v1/auth/me                                  (who is looking)
     - GET /api/v1/users/{username}                         (the public profile)
     - GET /api/v1/users/{username}/orgs                    (the linked organizations)
     - GET /api/v1/transcripts?owner=...                    (the library)
     - GET /api/v1/users/me/collectives/contributions       (the section, now four counters)
   and, once a collective and then a submission are opened:
     - GET /api/v1/users/me/collectives/{groupId}/submissions   (the owner-only PAIRS list)
     - GET /api/v1/users/me/collectives/{groupId}/transcripts/{transcriptId}/events

   It also answers the settings page's reads (`/auth/orgs`, `/users/me/settings`,
   `/auth/api-keys`) so that surface can be captured from the same build.

   The dataset is chosen to exercise the cases the section has to get right in
   one capture: a collective with approved, awaiting-review AND repeatedly
   refused submissions, a collective holding only submissions awaiting review,
   a collective refused three times and then withdrawn (so its pairs list has
   a row with a "withdrawn" chip rather than being empty — the exact
   contradiction a real user acceptance test caught: a nonzero withdrawn
   counter beside "no submissions of yours are on record"), and an event
   history containing all five states so the actor labels are visible, plus a
   legacy terminal row whose decision time was never captured (migration 036
   copied its status without one) so the unknown-time label is visible.

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
    withdrawn_attempt_count: 1,
  },
  {
    id: QUIET,
    name: 'Quiet Reviewers',
    description: null,
    linked_github_org: null,
    approved_count: 0,
    pending_count: 3,
    rejected_attempt_count: 0,
    withdrawn_attempt_count: 0,
  },
  // Submitted, then withdrawn with no refusal ever recorded. The current
  // state row is gone (approved and pending both 0) and nothing was ever
  // refused (rejected 0), but the withdrawn counter is nonzero — and its
  // pairs list has a row, not nothing.
  {
    id: FORMER,
    name: 'Former Home',
    description: 'everything offered here was withdrawn again',
    linked_github_org: null,
    approved_count: 0,
    pending_count: 0,
    rejected_attempt_count: 0,
    withdrawn_attempt_count: 1,
  },
  // Refused three times and then withdrawn. The refusals stay counted from the
  // event ledger, the withdrawal itself is now also counted, and both
  // transcript counters fall to zero — but the pairs endpoint still lists the
  // one pair, with a chip reading "withdrawn" and its history control intact.
  // This is the exact collective a real user acceptance test caught
  // contradicting itself: "3 submission attempts" above, "no submissions of
  // yours are on record" below.
  {
    id: STRICT,
    name: 'Strict Curators',
    description: 'refused three times, then withdrawn',
    linked_github_org: null,
    approved_count: 0,
    pending_count: 0,
    rejected_attempt_count: 3,
    withdrawn_attempt_count: 1,
  },
]

// One (transcript, collective) ledger pair, as the owner-only pairs endpoint
// serves it: identity, its collective, a title (nullable), and latest state.
const pair = (groupId, transcriptId, title, status, eventNum, recordedAt) => ({
  transcript_id: transcriptId,
  group_id: groupId,
  title,
  status,
  event_num: eventNum,
  recorded_at: recordedAt,
})

const submissionsByGroup = {
  [RESEARCH]: [
    pair(RESEARCH, '9a1c4e21', 'Rewriting the ingest commit detector', 'pending', 5, '2026-08-07T09:40:00Z'),
    pair(RESEARCH, '4f77b0c3', 'Tracing a redaction rule regression', 'approved', 2, '2026-07-26T15:45:00Z'),
    pair(RESEARCH, 'c02d5188', 'Measuring push latency across regions', 'approved', 1, '2026-07-11T17:20:00Z'),
    // The withdrawn pair the withdrawn counter above accounts for: refused
    // once, then withdrawn by the owner — no current-state row, but a real
    // row here with a "withdrawn" chip.
    pair(RESEARCH, 'b7c8d9e0', 'A first pass at the pull contract', 'retracted', 2, '2026-07-30T10:00:00Z'),
  ],
  [QUIET]: [
    pair(QUIET, '7b3e9d04', 'Reading the store migration ledger', 'pending', 1, '2026-08-10T09:00:00Z'),
    pair(QUIET, '1d8a6c55', null, 'pending', 1, '2026-08-11T09:00:00Z'),
    pair(QUIET, 'e5b21f70', 'Notes on the redaction category split', 'pending', 1, '2026-08-12T09:00:00Z'),
  ],
  [FORMER]: [pair(FORMER, 'a1b2c3d4', null, 'retracted', 1, '2026-08-05T12:00:00Z')],
  [STRICT]: [pair(STRICT, 'b4b6e2ad', 'Chasing a refusal loop', 'retracted', 4, '2026-08-06T08:20:00Z')],
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
  // A legacy approval too: migration 036 copied the old current-state row
  // without a decision time, so its one row is honest unknown evidence.
  [`${RESEARCH}/c02d5188`]: [
    event(1, 'approved', 'moderator', '2026-07-11T08:00:00Z', null),
  ],
  [`${RESEARCH}/b7c8d9e0`]: [
    event(1, 'rejected', 'moderator', '2026-07-20T09:00:00Z', '2026-07-21T09:00:00Z'),
    event(2, 'retracted', 'owner', '2026-07-30T10:00:00Z', '2026-07-30T10:00:00Z'),
  ],
  [`${FORMER}/a1b2c3d4`]: [
    event(1, 'retracted', 'owner', '2026-08-05T12:00:00Z', '2026-08-05T12:00:00Z'),
  ],
  // Attempt 1 is the legacy rejection migration 036 copied: its status came
  // from the pre-ledger row, so it never had a decision time. The later rows
  // were decided by the current write path and each carry one.
  [`${STRICT}/b4b6e2ad`]: [
    event(1, 'rejected', 'moderator', '2026-07-01T09:00:00Z', null),
    event(2, 'rejected', 'moderator', '2026-07-10T09:00:00Z', '2026-07-11T10:00:00Z'),
    event(3, 'rejected', 'moderator', '2026-07-20T09:00:00Z', '2026-07-21T10:00:00Z'),
    event(4, 'retracted', 'owner', '2026-08-06T07:05:00Z', '2026-08-06T08:20:00Z'),
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

/* A tiny inline SVG stands in for GitHub's avatar so a capture stays offline
   and deterministic. The real column carries an avatars.githubusercontent.com
   URL; only the bytes differ, and the page renders whatever URL it is given. */
const orgAvatar = (initials, color) =>
  `data:image/svg+xml;utf8,${encodeURIComponent(
    `<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32"><rect width="32" height="32" fill="${color}"/><text x="16" y="21" font-family="monospace" font-size="13" fill="#ffffff" text-anchor="middle">${initials}</text></svg>`,
  )}`

const server = createServer((req, res) => {
  const url = new URL(req.url, `http://localhost:${PORT}`)
  const path = url.pathname.replace(/^\/api\/v1/, '')
  if (req.method === 'OPTIONS') return send(res, 204, null)
  console.log(`${req.method} ${url.pathname}`)

  if (req.method !== 'GET') return send(res, 404, { error: `no mock route for ${req.method} ${url.pathname}` })

  if (path === '/auth/me') return send(res, 200, user)
  // The caller's own organizations, linked and not, as the settings page reads
  // them. One is linked so the switch shows both states in one capture.
  if (path === '/auth/orgs')
    return send(res, 200, [
      { org_id: 1, org_login: 'peasant-labs', avatar_url: orgAvatar('PL', '#6b7a52'), visible: true, fetched_at: '2026-09-01T00:00:00Z' },
      { org_id: 2, org_login: 'acme-collective', avatar_url: orgAvatar('AC', '#8a6a4b'), visible: false, fetched_at: '2026-09-01T00:00:00Z' },
    ])
  // The rest of the settings page's reads, so its own capture shows the page
  // rather than two error rows beside the organizations.
  if (path === '/users/me/settings')
    return send(res, 200, { auto_attach_pull_requests: false, preview_before_attach: true })
  if (path === '/auth/api-keys') return send(res, 200, [])
  // A person's LINKED organizations, as the profile page reads them: only the
  // visible rows, and this user has one.
  if (/^\/users\/[^/]+\/orgs$/.test(path))
    return send(res, 200, [{ org_login: 'peasant-labs', avatar_url: orgAvatar('PL', '#6b7a52') }])
  if (path === '/collectives') return send(res, 200, [])
  if (path === '/users/me/collectives/contributions') return send(res, 200, { collectives: contributions })

  const events = path.match(/^\/users\/me\/collectives\/([^/]+)\/transcripts\/([^/]+)\/events$/)
  if (events) return send(res, 200, eventsByGroupAndTranscript[`${events[1]}/${events[2]}`] ?? [])

  const submissions = path.match(/^\/users\/me\/collectives\/([^/]+)\/submissions$/)
  if (submissions) {
    const pairs = submissionsByGroup[submissions[1]]
    // The real server 404s — never a 200 with `[]` — when the owner has no
    // pairs for the collective, so an unknown group id models that here too.
    if (pairs === undefined) return send(res, 404, { error: 'not found' })
    return send(res, 200, pairs)
  }

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
