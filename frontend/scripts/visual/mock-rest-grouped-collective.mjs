/* REST mock for the collective grouped-helper capture.

   The real routes under capture are:
     GET /api/v1/groups/{id}                  (the collective browse list)
     GET /api/v1/groups/{id}?view=grouped     (the same metadata, grouped page)
     GET /api/v1/groups/{id}/contributable    (the contribute tree)
     GET /api/v1/groups/{id}/contributable?view=grouped
     GET /api/v1/groups/{id}/pending          (the review queue)
     GET /api/v1/groups/{id}/pending?view=grouped
     GET /api/v1/groups/{id}/my-shares
     GET /api/v1/transcript-groups/{groupId}/members?scope=...
     GET /api/v1/auth/me

   The viewer is the collective's OWNER, so the contribute page's member panel
   and the review page's owner-only queue both render, and the same group is
   offered on all three surfaces.

   Every row carries a canonical UUID identity because the opt-in grouped
   payload validates the whole row; the flat lists the page already serves are
   mapped through the SAME identity so the group attaches to the row it was
   grouped with.

   Usage:
     MOCK_REST_PORT=8847 node scripts/visual/mock-rest-grouped-collective.mjs
*/
import { createServer } from 'node:http'

const PORT = Number(process.env.MOCK_REST_PORT || 8847)
const GROUP_ID = '50000000-0000-4000-8000-000000000001'
const OWNER_ID = '30000000-0000-4000-8000-000000000099'
const VISITOR_ID = '30000000-0000-4000-8000-000000000010'
const HASH = 'a'.repeat(64)
/* Capture states for the grouped collective exits:
     MOCK_COLLECTIVE_EMPTY_FLAT=1  every flat list is empty, while the grouped
                                   pages still carry the saved helper groups.
     MOCK_COLLECTIVE_LATER_PAGE=1  the grouped reads page: a full first page of
                                   ordinary owners, then the grouped content on
                                   page two, so the continuation is the only way
                                   to reach it.
     MOCK_COLLECTIVE_MY_SHARES=1   the viewer's own contributions carry a saved
                                   helper group, so the collective page's
                                   "your contributions" panel mounts its grouped
                                   disclosure under the contribution row. */
const EMPTY_FLAT = process.env.MOCK_COLLECTIVE_EMPTY_FLAT === '1'
const LATER_PAGE = process.env.MOCK_COLLECTIVE_LATER_PAGE === '1'
const MY_SHARES = process.env.MOCK_COLLECTIVE_MY_SHARES === '1'
const LIST_PAGE_SIZE = 100

const user = {
  id: OWNER_ID,
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

/* Each route variant mints its OWN opaque member scope for the same group, so
   the member page can answer with the route's own row arm: the contribute route
   gets contributable rows, the review route gets pending rows. */
const ownerGroup = (variant) => ({
  groupId: 'hg_capture_owner',
  purpose: 'helper_review',
  helperThreadCount: 2,
  memberScope: `scope-capture-owner-${variant}`,
})
const contextGroup = (variant) => ({
  groupId: 'hg_capture_context',
  purpose: 'helper_review',
  helperThreadCount: 1,
  memberScope: `scope-capture-context-${variant}`,
})
/* The caller's own contribution carries a DISTINCT group id, so a page that
   draws it beside the collective's browse list never has two groups claiming
   one identity. */
const myShareGroup = () => ({
  groupId: 'hg_capture_my_share',
  purpose: 'helper_review',
  helperThreadCount: 2,
  memberScope: 'scope-capture-my-share',
})

/* Rows, in one table so the flat list and the grouped page cannot disagree
   about an identity. */
const owned = [
  { id: '22222222-2222-4222-8222-000000000001', local: 'ses_captureparent', title: 'Rework the grouped collective browse' },
  { id: '22222222-2222-4222-8222-000000000002', local: 'ses_capturereview', title: 'Decide the review queue in one action' },
]
const members = [
  { id: '22222222-2222-4222-8222-0000000000a1', local: 'ses_capturehelper1', title: 'Guardian review of the schema contract' },
  { id: '22222222-2222-4222-8222-0000000000a2', local: 'ses_capturehelper2', title: 'Guardian review of the storage lock' },
]

const session = ({ id, local, title, ownerID = VISITOR_ID, parent = null }) => ({
  id,
  owner_id: ownerID,
  local_id: local,
  title,
  description: null,
  visibility: 'shared',
  model_provider: 'claude-code',
  model_name: 'claude-opus-4-8',
  harness_version: '2026.08',
  session_start: '2026-09-01T10:00:00Z',
  session_end: '2026-09-01T10:30:00Z',
  turn_count: 12,
  token_count: 96000,
  blob_size_bytes: 0,
  schema_version: '0.13.0',
  published_at: '2026-09-01T11:00:00Z',
  updated_at: '2026-09-01T11:00:00Z',
  parent_session_id: parent,
  ingested_at: '2026-09-01T11:00:00Z',
  source_format: 'json',
  git_branch: 'develop',
  git_remote: null,
  project_hash: HASH,
  project_name: 'village',
  project_display_name: 'village',
  project_name_source: 'consented',
  project_remote_label: 'github.com:peasant-labs/village',
  tool_call_count: 12,
  subagent_count: 0,
  duration_ms: 1_800_000,
  session_origin: 'user',
  tokens_in: null,
  tokens_out: null,
  subagents: null,
  diagnostics_warnings: null,
  diagnostics_partial: null,
  title_generated: null,
  outcome: null,
  files_touched: null,
  lines_changed: null,
  retry_loops: null,
  retry_tokens_wasted: null,
  within_session_reverts: null,
  signal_density: null,
  spec_quality_score: null,
  exploration_ratio: null,
  scope_breadth: null,
  discovery_turns: null,
  m2_token_outcome_ratio: null,
  m3_unique_tool_count: null,
  m4_error_recovery_count: null,
  m4_consecutive_error_max: null,
  m5_context_utilization_pct: null,
  m5_peak_context_tokens: null,
  m5_avg_message_tokens: null,
  m6_output_survival_pct: null,
  m6_lines_survived: null,
  m6_lines_total: null,
  m7_spec_word_count: null,
  m7_spec_has_examples: null,
  m7_spec_has_constraints: null,
  computed_at: null,
  compute_version: null,
  content_hash: null,
  license_id: null,
})

const contributableArm = (row) => ({
  id: row.id,
  local_id: row.local,
  title: row.title,
  visibility: 'shared',
  project_hash: HASH,
  project_display_name: 'village',
  project_name_source: 'consented',
  git_branch: 'develop',
  parent_session_id: null,
  session_origin: 'user',
  model_provider: 'claude-code',
  published_at: '2026-09-01T11:00:00Z',
  already_shared: false,
})

const pendingArm = (row) => ({
  transcript_id: row.id,
  title: row.title,
  model_provider: 'claude-code',
  owner_id: VISITOR_ID,
  local_id: row.local,
  parent_session_id: null,
  project_hash: HASH,
  project_name: 'village',
  branch: 'develop',
  owner_username: 'bob-ai',
  owner_is_discoverable: true,
  shared_at: '2026-09-02T10:00:00Z',
})

/* The caller's OWN contribution, as `GET /groups/{id}/my-shares` serves it. The
   viewer is the collective's owner here, so the contribution belongs to them. */
const myShareArm = (row, status = 'approved') => ({
  id: row.id,
  owner_id: OWNER_ID,
  local_id: row.local,
  parent_session_id: null,
  title: row.title,
  model_provider: 'claude-code',
  model_name: 'claude-opus-4-8',
  visibility: 'shared',
  published_at: '2026-09-01T11:00:00Z',
  turn_count: 12,
  tokens_in: null,
  tokens_out: null,
  status,
  shared_at: '2026-09-02T10:00:00Z',
})

const myShareRows = [
  { id: '22222222-2222-4222-8222-000000000001', local: 'ses_captureparent', title: 'Rework the grouped collective browse' },
]
const myShareMembers = [
  { id: '22222222-2222-4222-8222-0000000000b1', local: 'ses_capturemysharehelper1', title: 'Guardian review of the contribution flow' },
  { id: '22222222-2222-4222-8222-0000000000b2', local: 'ses_capturemysharehelper2', title: 'Guardian review of the unshare path' },
]

/* The flat contribute tree: the same submission set, drawn the way it was
   before grouping existed. An empty-flat capture serves none of it, so the
   grouped content has no row to hang under. */
const flatContributable = {
  group_id: GROUP_ID,
  transcripts: EMPTY_FLAT ? [] : [...owned, ...members].map(contributableArm),
}

const flatPending = EMPTY_FLAT ? [] : [...owned, ...members].map(pendingArm)

/* The grouped pages: each owner row keeps its place with the saved helper
   group nested, and one helper-only container names an owner this page does
   not carry. */
/* The route arm a row is served with: the grouped page and the member page
   both nest it under the session row, never flattening its fields onto the row. */
const withArm = (row, arm) => {
  if (arm === 'contributable') return { contributable: contributableArm(row) }
  if (arm === 'pending') return { pending: pendingArm(row) }
  if (arm === 'my-share') return { myShare: myShareArm(row) }
  return {}
}

const groupedItems = (arm, variant) => [
  { kind: 'transcript', transcript: { session: session(owned[0]), ...withArm(owned[0], arm) }, helperGroups: [ownerGroup(variant)] },
  { kind: 'transcript', transcript: { session: session(owned[1]), ...withArm(owned[1], arm) } },
  {
    kind: 'context_container',
    context: { groupId: contextGroup(variant).groupId, ownerStatus: 'known_unavailable' },
    helperGroups: [contextGroup(variant)],
  },
]

/* A page-one ordinary owner that carries no saved helpers, so it mounts no
   grouped disclosure and draws no owner fallback row. */
const ordinaryOwner = (index) => ({
  id: `33333333-3333-4333-8333-${String(index).padStart(12, '0')}`,
  local: `ses_laterordinary${index}`,
  title: `ordinary grouped owner ${index}`,
})

const ordinaryPage = () =>
  Array.from({ length: LIST_PAGE_SIZE }, (_, index) => ({
    kind: 'transcript',
    transcript: { session: session(ordinaryOwner(index)) },
  }))

const listPage = (arm, variant, requestedPage = 1) => {
  if (!LATER_PAGE) {
    const items = groupedItems(arm, variant)
    return { items, page: 1, limit: LIST_PAGE_SIZE, totalItems: items.length, ordinarySessionTotal: owned.length, helperThreadTotal: 3 }
  }
  const page = requestedPage
  const items = page === 1 ? ordinaryPage() : groupedItems(arm, variant)
  return {
    items,
    page,
    limit: LIST_PAGE_SIZE,
    totalItems: LIST_PAGE_SIZE + 1,
    ordinarySessionTotal: page === 1 ? LIST_PAGE_SIZE : owned.length,
    helperThreadTotal: page === 1 ? 0 : 3,
  }
}

const groupRecord = {
  id: GROUP_ID,
  name: 'AI Research Collective',
  description: 'Shared governance for the village demo.',
  created_by: OWNER_ID,
  created_at: '2026-06-01T12:00:00Z',
  updated_at: '2026-06-28T12:00:00Z',
  acceptance_mode: 'curated',
  data_access: 'members_only',
  linked_github_org: null,
  display_members: true,
  transcript_deletion_policy: 'user_choice',
}

const flatGroupDetail = {
  group: { ...groupRecord, role: 'owner', member_since: '2026-06-01T12:00:00Z', member_count: 1, transcript_count: owned.length },
  members: [{ id: OWNER_ID, role: 'owner', joined_at: '2026-06-01T12:00:00Z', github_username: 'alice-dev', display_name: 'Alice Developer', avatar_url: null, github_orgs: [] }],
  transcripts: EMPTY_FLAT ? [] : owned.map((row) => ({ ...session(row), owner_username: 'bob-ai', owner_avatar_url: null, owner_is_discoverable: true })),
  stats: { total_transcripts: owned.length, contributor_count: 1, total_turns: 0, total_duration_ms: 0, total_tokens: 0 },
  models: [],
  contributors: [],
  can_read: true,
  your_role: 'owner',
  pending_members: [],
}

const groupedGroupDetail = (page = 1) => ({
  ...flatGroupDetail,
  transcripts: undefined,
  transcriptList: listPage(null, 'collective', page),
})

const memberPayload = (arm) => ({
  members: members.map((row) => ({ kind: 'transcript', transcript: { session: session(row), ...withArm(row, arm) } })),
  page: 1,
  limit: 20,
  total: members.length,
})

/* The caller's own contributions, for the collective page's "your
   contributions" panel: the flat list the panel always drew, and the grouped
   page that nests the saved helper group under the contribution the server
   grouped it with. Both the panel's rows and the nested members belong to the
   signed-in owner. */
const myShareSession = (row) => session({ ...row, ownerID: OWNER_ID })
const flatMyShares = MY_SHARES ? myShareRows.map((row) => myShareArm(row, 'pending')) : []
const groupedMySharesPage = () => {
  const items = MY_SHARES
    ? [
        {
          kind: 'transcript',
          transcript: {
            session: myShareSession(myShareRows[0]),
            myShare: myShareArm(myShareRows[0], 'pending'),
          },
          helperGroups: [myShareGroup()],
        },
      ]
    : []
  return {
    items,
    page: 1,
    limit: LIST_PAGE_SIZE,
    totalItems: items.length,
    ordinarySessionTotal: items.length,
    helperThreadTotal: MY_SHARES ? 2 : 0,
  }
}
const myShareMemberPayload = {
  members: myShareMembers.map((row) => ({
    kind: 'transcript',
    transcript: { session: myShareSession(row), myShare: myShareArm(row) },
  })),
  page: 1,
  limit: 20,
  total: myShareMembers.length,
}

const send = (res, status, body) => {
  res.writeHead(status, {
    'content-type': 'application/json',
    'access-control-allow-origin': '*',
    'access-control-allow-headers': '*',
    'access-control-allow-methods': 'GET,POST,PATCH,DELETE,OPTIONS',
  })
  res.end(body == null ? '' : JSON.stringify(body))
}

const server = createServer((req, res) => {
  const url = new URL(req.url, `http://localhost:${PORT}`)
  const path = url.pathname.replace(/^\/api\/v1/, '')
  if (req.method === 'OPTIONS') return send(res, 204, null)
  console.log(`${req.method} ${url.pathname}${url.search}`)

  if (path === '/auth/me') return send(res, 200, user)
  if (path === '/auth/orgs') return send(res, 200, [])
  if (path === '/groups/visible' || path === '/groups') return send(res, 200, [flatGroupDetail.group])

  if (req.method === 'GET' && path === `/groups/${GROUP_ID}`) {
    const grouped = url.searchParams.get('view') === 'grouped'
    const requestedPage = Number(url.searchParams.get('page') || 1)
    return send(res, 200, grouped ? groupedGroupDetail(requestedPage) : flatGroupDetail)
  }
  if (req.method === 'GET' && path === `/groups/${GROUP_ID}/contributable`) {
    if (url.searchParams.get('view') === 'grouped') {
      const requestedPage = Number(url.searchParams.get('page') || 1)
      return send(res, 200, { groupId: GROUP_ID, transcriptList: listPage('contributable', 'contributable', requestedPage) })
    }
    return send(res, 200, flatContributable)
  }
  if (req.method === 'GET' && path === `/groups/${GROUP_ID}/pending`) {
    if (url.searchParams.get('view') === 'grouped') {
      const requestedPage = Number(url.searchParams.get('page') || 1)
      return send(res, 200, listPage('pending', 'pending', requestedPage))
    }
    return send(res, 200, flatPending)
  }
  if (req.method === 'GET' && path === `/groups/${GROUP_ID}/my-shares`) {
    if (url.searchParams.get('view') === 'grouped') return send(res, 200, groupedMySharesPage())
    return send(res, 200, flatMyShares)
  }
  if (req.method === 'GET' && path === `/groups/${GROUP_ID}/repositories`) {
    // The repository feature is optional server-side; 501 is a clean
    // not-configured state the panel states itself.
    return send(res, 501, { error: 'not configured' })
  }

  const membersMatch = path.match(/^\/transcript-groups\/([^/]+)\/members$/)
  if (req.method === 'GET' && membersMatch) {
    const scope = url.searchParams.get('scope') ?? ''
    const known = [
      ownerGroup('collective'),
      ownerGroup('contributable'),
      ownerGroup('pending'),
      myShareGroup(),
      contextGroup('collective'),
      contextGroup('contributable'),
      contextGroup('pending'),
    ]
    if (!known.some((candidate) => candidate.groupId === membersMatch[1] && candidate.memberScope === scope)) {
      return send(res, 409, { code: 'group_scope_expired', error: 'refresh the originating list' })
    }
    if (scope.endsWith('-my-share')) return send(res, 200, myShareMemberPayload)
    return send(res, 200, scope.endsWith('-pending') ? memberPayload('pending') : memberPayload('contributable'))
  }

  return send(res, 404, { error: `no mock route for ${req.method} ${url.pathname}` })
})

server.on('error', (err) => {
  if (err.code === 'EADDRINUSE') {
    console.error(
      `ERROR [mock-rest-grouped-collective.mjs] port ${PORT} is already in use.\n` +
        `  Why: another process is bound to it, so this mock never served a byte.\n` +
        `  Means: the app would read that other server's fixtures and the capture would show the wrong data.\n` +
        `  Fix: stop the process holding port ${PORT}, or set MOCK_REST_PORT to a free port, and retry.`,
    )
    process.exit(2)
  }
  throw err
})

server.listen(PORT, () => {
  console.log(`mock-rest-grouped-collective: serving collective "${GROUP_ID}" on http://localhost:${PORT}/api/v1`)
})
