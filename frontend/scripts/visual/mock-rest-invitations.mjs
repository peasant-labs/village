/* REST mock for the collective invitation and notification email surfaces.

   It answers the reads of four mounted pages:
     - /collectives/{id}/settings?section=members  the owner's invite form, the
       roster and the open invitations (GET /collectives/{id},
       GET /collectives/{id}/invitations)
     - /collectives                                the invitee's own invitations
       (GET /users/me/invitations) above their collectives
     - /settings                                   the email section
       (GET /users/me/settings with a verified notification address)
     - /email/unsubscribe?token=...                the signed-out one-click page;
       it reads nothing until the button is pressed

   MOCK_VIEWER picks who is signed in: owner (default, the collective's
   creator) or invitee (the person the owner invited). The unsubscribe capture
   sends no cookie, so /auth/me answers 401 for it whatever the viewer.

   Dates sit just before the harness's frozen clock (determinism.mjs,
   2024-01-01T12:00Z), so expiry reads as it would on a live invitation.

   Every request is logged; a route this mock does not know answers 404 and
   names itself, so a page that starts reading something new is visible.

   Usage:
     MOCK_REST_PORT=8795 [MOCK_VIEWER=invitee] node scripts/visual/mock-rest-invitations.mjs
*/
import { createServer } from 'node:http'

const PORT = Number(process.env.MOCK_REST_PORT || 8795)
const VIEWER = process.env.MOCK_VIEWER || 'owner'

const COLLECTIVE = '7a000000-0000-4000-8000-0000000000c1'
const OTHER = '7a000000-0000-4000-8000-0000000000c2'

function account(id, username, displayName) {
  return {
    id,
    github_id: 1,
    github_username: username,
    display_name: displayName,
    avatar_url: null,
    created_at: '2026-06-01T12:00:00Z',
    updated_at: '2026-06-01T12:00:00Z',
    is_discoverable: true,
    username_chosen: true,
    provider_username: username,
  }
}

function profile(user) {
  return { kind: 'profile', id: user.id, username: user.github_username, display_name: user.display_name, avatar_url: null }
}

const owner = account('user-owner', 'tomas-r', 'Tomás Rivera')
const coOwner = account('user-co-owner', 'priya-k', 'Priya Kapoor')
const member = account('user-member', 'lee-w', 'Lee Wong')
const invitee = account('user-invitee', 'sam-o', 'Sam Okafor')
const viewer = VIEWER === 'invitee' ? invitee : owner

const group = {
  id: COLLECTIVE,
  name: 'Eval Crowd',
  description: 'evaluation transcripts, reviewed before they are shared',
  acceptance_mode: 'curated',
  data_access: 'members_only',
  linked_github_org: null,
  display_members: true,
  transcript_deletion_policy: 'user_choice',
  post_prompts_check: true,
  prompts_check_mode: 'informational',
  creator: profile(owner),
  created_at: '2026-09-01T12:00:00Z',
  updated_at: '2026-09-01T12:00:00Z',
  role: VIEWER === 'invitee' ? '' : 'owner',
  member_since: VIEWER === 'invitee' ? null : '2026-09-01T12:00:00Z',
}

const members = [
  { member: profile(owner), github_orgs: [], joined_at: '2026-09-01T12:00:00Z', role: 'owner' },
  { member: profile(coOwner), github_orgs: [], joined_at: '2026-09-03T12:00:00Z', role: 'owner' },
  { member: profile(member), github_orgs: [], joined_at: '2026-09-10T12:00:00Z', role: 'contributor' },
]

// The owner's list: one typed handle that an account holds and one that none
// does. The list never says which is which.
const sentInvitations = [
  { id: '1e000000-0000-4000-8000-0000000000a1', invited_username: 'sam-o', invited_by: profile(owner), created_at: '2023-12-30T09:00:00Z', expires_at: '2024-01-29T09:00:00Z' },
  { id: '1e000000-0000-4000-8000-0000000000a2', invited_username: 'not-yet-signed-up', invited_by: profile(coOwner), created_at: '2023-12-31T15:30:00Z', expires_at: '2024-01-30T15:30:00Z' },
]

// The invitee's own list: one from an owner they may see, one from an owner
// hidden from them.
const myInvitations = [
  {
    id: '1e000000-0000-4000-8000-0000000000a1',
    collective: { id: COLLECTIVE, name: 'Eval Crowd', description: 'evaluation transcripts, reviewed before they are shared', acceptance_mode: 'curated' },
    invited_by: profile(owner),
    created_at: '2023-12-30T09:00:00Z',
    expires_at: '2024-01-29T09:00:00Z',
  },
  {
    id: '1e000000-0000-4000-8000-0000000000b2',
    collective: { id: OTHER, name: 'Night Shift', description: null, acceptance_mode: 'open' },
    invited_by: { kind: 'anonymous' },
    created_at: '2023-12-31T20:00:00Z',
    expires_at: '2024-01-30T20:00:00Z',
  },
]

const settings = {
  preview_before_attach: true,
  auto_attach_pull_requests: true,
  notification_email: { address: 'sam.okafor@example.org', verified: true },
  email_invitations: true,
}

const groupDetail = {
  group,
  members,
  transcripts: [],
  stats: { total_transcripts: 0, contributor_count: 3, total_turns: 0, total_duration_ms: 0, total_tokens: 0, pull_request_count: 0 },
  models: [],
  contributors: [],
  can_read: true,
  your_role: group.role,
}

const emptyGrouped = (url) => ({
  items: [],
  page: Number(url.searchParams.get('page') ?? 1),
  limit: Number(url.searchParams.get('limit') ?? 100),
  totalItems: 0,
  ordinarySessionTotal: 0,
  helperThreadTotal: 0,
})

function send(res, status, body) {
  res.writeHead(status, {
    'content-type': 'application/json',
    'access-control-allow-origin': '*',
    'access-control-allow-headers': 'authorization, content-type, peasant-pull-contract',
    'access-control-allow-methods': 'GET, POST, PUT, PATCH, DELETE, OPTIONS',
  })
  res.end(status === 204 ? '' : JSON.stringify(body))
}

const server = createServer((req, res) => {
  const url = new URL(req.url, `http://localhost:${PORT}`)
  const path = url.pathname.replace(/^\/api\/v1/, '')
  if (req.method === 'OPTIONS') return send(res, 204, null)
  console.log(`${req.method} ${url.pathname}${url.search}`)
  const signedIn = (req.headers.authorization || '').startsWith('Bearer ')

  if (path === '/auth/me') return signedIn ? send(res, 200, viewer) : send(res, 401, { error: 'Authentication required' })
  if (path === '/auth/orgs') return send(res, 200, [])
  if (path === '/auth/api-keys') return send(res, 200, [])
  if (path === '/users/me/settings') return send(res, 200, settings)
  if (path === '/users/me/invitations') return send(res, 200, { invitations: VIEWER === 'invitee' ? myInvitations : [] })
  if (path === '/users/me/collectives/contributions') return send(res, 200, { collectives: [] })
  if (path === '/users/me/prompt-requests') return send(res, 200, { requests: [] })
  if (path === '/collectives/visible') return send(res, 200, VIEWER === 'invitee' ? [] : [group])
  if (path === '/collectives') return send(res, 200, VIEWER === 'invitee' ? [] : [group])
  if (path === '/collectives/search') return send(res, 200, { collectives: [] })

  const base = `/collectives/${COLLECTIVE}`
  if (path === base) {
    if (url.searchParams.get('view') === 'grouped') return send(res, 200, { group_id: COLLECTIVE, transcriptList: emptyGrouped(url) })
    return send(res, 200, groupDetail)
  }
  if (path === `${base}/invitations`) return send(res, 200, { invitations: sentInvitations })
  if (path === `${base}/repositories`) return send(res, 200, [])
  if (path === `${base}/repositories/available`) return send(res, 200, { repositories: [], installations: [] })
  if (path === `${base}/pending`) return send(res, 200, url.searchParams.get('view') === 'grouped' ? emptyGrouped(url) : [])
  if (path === `${base}/my-shares`) return send(res, 200, url.searchParams.get('view') === 'grouped' ? emptyGrouped(url) : [])

  return send(res, 404, { error: `no mock route for ${req.method} ${url.pathname}` })
})

server.listen(PORT, () => console.log(`mock-rest-invitations listening on ${PORT} as ${VIEWER}`))
