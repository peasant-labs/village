/* REST mock for the capture that shows a session under the session that
   started it, on every list that now folds.

   One mock, six surfaces, because the six answers are one design and a reviewer
   reads them together:

     profile library        GET /users/{username}, GET /transcripts?owner=...
     collective data list   GET /groups/{id}
     collective repos view  the same response, read by repository
     pending-share queue    GET /groups/{id}/pending
     your contributions     GET /groups/{id}/my-shares
     contribute tree        GET /groups/{id}/contributable

   EVERY one of those datasets carries the same two cases, because the contrast
   is the evidence:

     a parent with TWO started sessions, which fold under it, and
     a row naming a parent this response does NOT carry, which keeps its
     ordinary row rather than disappearing.

   The collective is `curated` and the viewer is its OWNER, so the review queue
   and the owner-only selection boxes render.

   Usage:
     MOCK_REST_PORT=8846 node scripts/visual/mock-rest-child-sessions.mjs
*/
import { createServer } from 'node:http'

const PORT = Number(process.env.MOCK_REST_PORT || 8846)
const GROUP_ID = process.env.MOCK_GROUP_ID || 'demo-group'

const OWNER_ID = 'user-demo'
const OTHER_ID = 'member-2'

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

const otherUser = {
  id: OTHER_ID,
  github_id: 654321,
  github_username: 'bob-ai',
  display_name: 'Bob AI',
  avatar_url: null,
  created_at: '2026-06-01T12:00:00Z',
  updated_at: '2026-06-01T12:00:00Z',
  is_discoverable: true,
  username_chosen: true,
  provider_username: 'bob-ai',
}

const PEASANT_HASH = 'b'.repeat(64)
const VILLAGE_HASH = 'c'.repeat(64)
const PEASANT_REMOTE = 'git@github.com:peasant-labs/peasant.git'
const VILLAGE_REMOTE = 'git@github.com:peasant-labs/village.git'

/* One transcript row. Only the fields a case actually turns on are passed in;
   everything else is the same for every row, so a reader comparing two rows in
   a capture is comparing the thing under test rather than incidental noise. */
const transcript = ({
  id,
  ownerID,
  localID,
  parent,
  title,
  provider,
  model,
  projectHash,
  projectName,
  remote,
  branch = 'main',
  turns,
  tokensIn,
  tokensOut,
  published,
}) => ({
  id,
  owner_id: ownerID,
  local_id: localID,
  title,
  description: null,
  visibility: 'public',
  model_provider: provider,
  model_name: model,
  harness_version: '1.0.0',
  session_start: published,
  session_end: published,
  turn_count: turns,
  token_count: tokensIn + tokensOut,
  blob_key: `blob-${id}`,
  blob_size_bytes: 4096,
  schema_version: '0.13.0',
  published_at: published,
  updated_at: published,
  parent_session_id: parent,
  ingested_at: published,
  source_file_path: null,
  source_format: 'json',
  git_branch: branch,
  git_remote: remote,
  git_worktree: null,
  project_hash: projectHash,
  project_name: projectName,
  project_display_name: projectName,
  project_name_source: 'consented',
  project_remote_label: remote
    ? `github.com:${remote.replace(/^git@github\.com:/, '').replace(/\.git$/, '')}`
    : '',
  tool_call_count: 4,
  subagent_count: 0,
  duration_ms: 480000,
  tokens_in: tokensIn,
  tokens_out: tokensOut,
  subagents: [],
  diagnostics_warnings: [],
  diagnostics_partial: false,
  title_generated: null,
  outcome: 'resolved',
  files_touched: 3,
  lines_changed: 40,
  retry_loops: 0,
  retry_tokens_wasted: 0,
  within_session_reverts: 0,
  signal_density: 0.6,
  session_origin: 'user',
})

// ── The profile library ──────────────────────────────────────────────────────
// Two projects. The first holds a parent, the two sessions it started, and a
// row whose parent this response does not carry.

const libraryRows = [
  transcript({
    id: 'lib-parent',
    ownerID: OWNER_ID,
    localID: 'sess_lib_parent',
    parent: null,
    title: 'Rework the redaction rule ordering',
    provider: 'claude-code',
    model: 'claude-opus-4-8',
    projectHash: PEASANT_HASH,
    projectName: 'peasant',
    remote: PEASANT_REMOTE,
    turns: 42,
    tokensIn: 18000,
    tokensOut: 9000,
    published: '2026-06-24T09:00:00Z',
  }),
  transcript({
    id: 'lib-child-1',
    ownerID: OWNER_ID,
    localID: 'sess_lib_child_1',
    parent: 'sess_lib_parent',
    title: 'Search the rule table for the git-context rules',
    provider: 'claude-code',
    model: 'claude-sonnet-5',
    projectHash: PEASANT_HASH,
    projectName: 'peasant',
    remote: PEASANT_REMOTE,
    turns: 6,
    tokensIn: 2400,
    tokensOut: 900,
    published: '2026-06-24T09:20:00Z',
  }),
  transcript({
    id: 'lib-child-2',
    ownerID: OWNER_ID,
    localID: 'sess_lib_child_2',
    parent: 'sess_lib_parent',
    title: 'Write the activation-level table test',
    provider: 'claude-code',
    model: 'claude-sonnet-5',
    projectHash: PEASANT_HASH,
    projectName: 'peasant',
    remote: PEASANT_REMOTE,
    turns: 9,
    tokensIn: 3100,
    tokensOut: 1400,
    published: '2026-06-24T09:35:00Z',
  }),
  // The contrast case: it names a starter this response does not carry, so it
  // keeps an ordinary row.
  transcript({
    id: 'lib-unmatched',
    ownerID: OWNER_ID,
    localID: 'sess_lib_unmatched',
    parent: 'sess_lib_absent',
    title: 'A session whose starter is not on this page',
    provider: 'gemini-cli',
    model: 'gemini-2.5-pro',
    projectHash: PEASANT_HASH,
    projectName: 'peasant',
    remote: PEASANT_REMOTE,
    turns: 11,
    tokensIn: 4200,
    tokensOut: 2100,
    published: '2026-06-23T09:00:00Z',
  }),
  transcript({
    id: 'lib-solo',
    ownerID: OWNER_ID,
    localID: 'sess_lib_solo',
    parent: null,
    title: 'Serve the signed-in home page at the root',
    provider: 'codex',
    model: 'gpt-5-codex',
    projectHash: VILLAGE_HASH,
    projectName: 'village',
    remote: VILLAGE_REMOTE,
    turns: 17,
    tokensIn: 6400,
    tokensOut: 3300,
    published: '2026-06-22T09:00:00Z',
  }),
]

const libraryResponse = {
  transcripts: libraryRows.map((t) => ({ transcript: t, tags: [], owner: user, shares: [] })),
  total: libraryRows.length,
  agent_total: 0,
  page: 1,
  limit: 100,
}

// ── The collective's own contributions ───────────────────────────────────────
// Five rows, because the page previews the first five before "browse data" is
// opened. Three sit on one repository so the repos view has a fold too.

const withOwner = (t, username, discoverable = true) => ({
  ...t,
  owner_username: username,
  owner_avatar_url: null,
  owner_is_discoverable: discoverable,
})

const groupTranscripts = [
  withOwner(
    transcript({
      id: 'gt-parent',
      ownerID: OWNER_ID,
      localID: 'sess_col_parent',
      parent: null,
      title: 'Fold a started session under the session that started it',
      provider: 'claude-code',
      model: 'claude-opus-4-8',
      projectHash: PEASANT_HASH,
      projectName: 'peasant',
      remote: PEASANT_REMOTE,
      turns: 38,
      tokensIn: 16000,
      tokensOut: 8200,
      published: '2026-06-26T09:00:00Z',
    }),
    'alice-dev',
  ),
  withOwner(
    transcript({
      id: 'gt-child-1',
      ownerID: OWNER_ID,
      localID: 'sess_col_child_1',
      parent: 'sess_col_parent',
      title: 'Read the collapsed-group control',
      provider: 'claude-code',
      model: 'claude-sonnet-5',
      projectHash: PEASANT_HASH,
      projectName: 'peasant',
      remote: PEASANT_REMOTE,
      turns: 7,
      tokensIn: 2600,
      tokensOut: 1100,
      published: '2026-06-26T09:18:00Z',
    }),
    'alice-dev',
  ),
  withOwner(
    transcript({
      id: 'gt-child-2',
      ownerID: OWNER_ID,
      localID: 'sess_col_child_2',
      parent: 'sess_col_parent',
      title: 'Draw the selection box on a folded row',
      provider: 'claude-code',
      model: 'claude-sonnet-5',
      projectHash: PEASANT_HASH,
      projectName: 'peasant',
      remote: PEASANT_REMOTE,
      turns: 12,
      tokensIn: 3900,
      tokensOut: 1700,
      published: '2026-06-26T09:31:00Z',
    }),
    'alice-dev',
  ),
  withOwner(
    transcript({
      id: 'gt-unmatched',
      ownerID: OTHER_ID,
      localID: 'sess_col_unmatched',
      parent: 'sess_col_absent',
      title: 'A contribution whose starter this collective does not hold',
      provider: 'gemini-cli',
      model: 'gemini-2.5-pro',
      projectHash: VILLAGE_HASH,
      projectName: 'village',
      remote: VILLAGE_REMOTE,
      turns: 14,
      tokensIn: 5100,
      tokensOut: 2400,
      published: '2026-06-25T09:00:00Z',
    }),
    'bob-ai',
  ),
  withOwner(
    transcript({
      id: 'gt-solo',
      ownerID: OTHER_ID,
      localID: 'sess_col_solo',
      parent: null,
      title: 'List every visible collective with its badges',
      provider: 'codex',
      model: 'gpt-5-codex',
      projectHash: VILLAGE_HASH,
      projectName: 'village',
      remote: VILLAGE_REMOTE,
      turns: 21,
      tokensIn: 7300,
      tokensOut: 3800,
      published: '2026-06-24T09:00:00Z',
    }),
    'bob-ai',
  ),
]

const groupDetail = {
  group: {
    id: GROUP_ID,
    name: 'AI Research Collective',
    description: 'Shared governance for the village demo.',
    linked_github_org: 'anthropic-labs',
    display_members: true,
    transcript_deletion_policy: 'user_choice',
    created_by: OWNER_ID,
    created_at: '2026-06-01T12:00:00Z',
    updated_at: '2026-06-28T12:00:00Z',
    // Curated, so the review queue this capture needs is served at all.
    acceptance_mode: 'curated',
    data_access: 'members_only',
    role: 'owner',
    member_since: '2026-06-01T12:00:00Z',
    member_count: 12,
    transcript_count: groupTranscripts.length,
  },
  members: [
    {
      id: OWNER_ID,
      role: 'owner',
      joined_at: '2026-06-01T12:00:00Z',
      github_username: 'alice-dev',
      display_name: 'Alice Developer',
      avatar_url: null,
      github_orgs: ['anthropic-labs'],
    },
    {
      id: OTHER_ID,
      role: 'member',
      joined_at: '2026-06-10T12:00:00Z',
      github_username: 'bob-ai',
      display_name: 'Bob AI',
      avatar_url: null,
      github_orgs: ['anthropic-labs'],
    },
  ],
  transcripts: groupTranscripts,
  stats: {
    total_transcripts: groupTranscripts.length,
    contributor_count: 2,
    total_turns: groupTranscripts.reduce((sum, t) => sum + (t.turn_count ?? 0), 0),
    total_duration_ms: 2400000,
    total_tokens: groupTranscripts.reduce((sum, t) => sum + (t.token_count ?? 0), 0),
  },
  models: [
    { model_provider: 'claude-code', transcript_count: 3 },
    { model_provider: 'gemini-cli', transcript_count: 1 },
    { model_provider: 'codex', transcript_count: 1 },
  ],
  contributors: [
    { id: OWNER_ID, github_username: 'alice-dev', avatar_url: null, transcript_count: 3 },
    { id: OTHER_ID, github_username: 'bob-ai', avatar_url: null, transcript_count: 2 },
  ],
  can_read: true,
  your_role: 'owner',
  pending_members: [],
}

// ── The review queue ─────────────────────────────────────────────────────────

const pendingShare = ({ id, localID, parent, title, provider, at }) => ({
  transcript_id: id,
  title,
  model_provider: provider,
  owner_id: OTHER_ID,
  local_id: localID,
  parent_session_id: parent,
  owner_username: 'bob-ai',
  owner_is_discoverable: true,
  shared_at: at,
})

const pendingShares = [
  pendingShare({
    id: 'ps-parent',
    localID: 'sess_pend_parent',
    parent: null,
    title: 'Rebuild the push wizard on the shared kit',
    provider: 'claude-code',
    at: '2026-06-27T09:00:00Z',
  }),
  pendingShare({
    id: 'ps-child-1',
    localID: 'sess_pend_child_1',
    parent: 'sess_pend_parent',
    title: 'Read the consent copy the wizard shows',
    provider: 'claude-code',
    at: '2026-06-27T09:14:00Z',
  }),
  pendingShare({
    id: 'ps-child-2',
    localID: 'sess_pend_child_2',
    parent: 'sess_pend_parent',
    title: 'Draft the published-transcript preview',
    provider: 'claude-code',
    at: '2026-06-27T09:26:00Z',
  }),
  pendingShare({
    id: 'ps-unmatched',
    localID: 'sess_pend_unmatched',
    parent: 'sess_pend_absent',
    title: 'A submission whose starter was never offered here',
    provider: 'gemini-cli',
    at: '2026-06-27T10:00:00Z',
  }),
]

// ── The caller's own contributions ───────────────────────────────────────────

const myShare = ({ id, localID, parent, title, provider, status, at }) => ({
  id,
  owner_id: OWNER_ID,
  local_id: localID,
  parent_session_id: parent,
  title,
  model_provider: provider,
  model_name: null,
  visibility: 'shared',
  published_at: at,
  turn_count: 12,
  tokens_in: 4000,
  tokens_out: 2000,
  status,
  shared_at: at,
})

const myShares = [
  myShare({
    id: 'ms-parent',
    localID: 'sess_my_parent',
    parent: null,
    title: 'Group a started session under its starter on every list',
    provider: 'claude-code',
    status: 'approved',
    at: '2026-06-28T09:00:00Z',
  }),
  myShare({
    id: 'ms-child-1',
    localID: 'sess_my_child_1',
    parent: 'sess_my_parent',
    title: 'Read the profile library grouping',
    provider: 'claude-code',
    status: 'approved',
    at: '2026-06-28T09:15:00Z',
  }),
  myShare({
    id: 'ms-child-2',
    localID: 'sess_my_child_2',
    parent: 'sess_my_parent',
    title: 'Check the queue keeps approve and reject',
    provider: 'claude-code',
    status: 'pending',
    at: '2026-06-28T09:29:00Z',
  }),
  myShare({
    id: 'ms-unmatched',
    localID: 'sess_my_unmatched',
    parent: 'sess_my_absent',
    title: 'A contribution whose starter stayed private',
    provider: 'codex',
    status: 'approved',
    at: '2026-06-27T09:00:00Z',
  }),
]

// ── The contribute tree ──────────────────────────────────────────────────────

const contributable = {
  group_id: GROUP_ID,
  transcripts: [
    {
      id: 'ct-parent',
      local_id: 'sess_ct_parent',
      title: 'Carry the fold into the contribute tree',
      visibility: 'private',
      project_hash: PEASANT_HASH,
      project_display_name: 'peasant',
      project_name_source: 'consented',
      git_branch: 'main',
      parent_session_id: null,
      session_origin: 'user',
      model_provider: 'claude-code',
      published_at: '2026-06-26T09:00:00Z',
      already_shared: false,
    },
    {
      id: 'ct-child-1',
      local_id: 'sess_ct_child_1',
      title: 'Read the tree builder',
      visibility: 'private',
      project_hash: PEASANT_HASH,
      project_display_name: 'peasant',
      project_name_source: 'consented',
      git_branch: 'main',
      parent_session_id: 'sess_ct_parent',
      session_origin: 'user',
      model_provider: 'claude-code',
      published_at: '2026-06-26T09:16:00Z',
      already_shared: false,
    },
    {
      id: 'ct-child-2',
      local_id: 'sess_ct_child_2',
      title: 'Swap the label onto the shared control',
      visibility: 'private',
      project_hash: PEASANT_HASH,
      project_display_name: 'peasant',
      project_name_source: 'consented',
      git_branch: 'main',
      parent_session_id: 'sess_ct_parent',
      session_origin: 'user',
      model_provider: 'claude-code',
      published_at: '2026-06-26T09:28:00Z',
      already_shared: false,
    },
    {
      id: 'ct-unmatched',
      local_id: 'sess_ct_unmatched',
      title: 'A session whose starter was never fetched',
      visibility: 'public',
      project_hash: PEASANT_HASH,
      project_display_name: 'peasant',
      project_name_source: 'consented',
      git_branch: 'main',
      parent_session_id: 'sess_ct_absent',
      session_origin: 'user',
      model_provider: 'codex',
      published_at: '2026-06-25T09:00:00Z',
      already_shared: false,
    },
    {
      id: 'ct-village-1',
      local_id: 'sess_ct_village_1',
      title: 'Serve the collective browse list from the shared list',
      visibility: 'private',
      project_hash: VILLAGE_HASH,
      project_display_name: 'village',
      project_name_source: 'consented',
      git_branch: 'main',
      parent_session_id: null,
      session_origin: 'user',
      model_provider: 'gemini-cli',
      published_at: '2026-06-24T09:00:00Z',
      already_shared: false,
    },
  ],
}

const contributions = [
  {
    id: GROUP_ID,
    name: 'AI Research Collective',
    description: null,
    linked_github_org: 'anthropic-labs',
    approved_count: 3,
    pending_count: 1,
    rejected_attempt_count: 0,
    withdrawn_attempt_count: 0,
  },
]

// ── Server ───────────────────────────────────────────────────────────────────

const send = (res, code, body) => {
  res.writeHead(code, {
    'content-type': 'application/json',
    'access-control-allow-origin': '*',
    'access-control-allow-headers': '*',
    'access-control-allow-methods': 'GET,POST,PATCH,DELETE,OPTIONS',
  })
  res.end(body == null ? '' : JSON.stringify(body))
}

const server = createServer((req, res) => {
  const url = new URL(req.url, `http://localhost:${PORT}`)
  const p = url.pathname.replace(/^\/api\/v1/, '')
  if (req.method === 'OPTIONS') return send(res, 204, null)
  console.log(`${req.method} ${url.pathname}${url.search}`)

  if (req.method === 'GET' && p === '/auth/me') return send(res, 200, user)
  if (req.method === 'GET' && p === '/auth/orgs') return send(res, 200, [])
  if (req.method === 'GET' && p === '/users/alice-dev') return send(res, 200, user)
  if (req.method === 'GET' && p === '/users/bob-ai') return send(res, 200, otherUser)
  if (req.method === 'GET' && p === '/transcripts') return send(res, 200, libraryResponse)
  if (req.method === 'GET' && p === '/users/me/collectives/contributions') {
    return send(res, 200, { collectives: contributions })
  }
  if (req.method === 'GET' && p === '/groups') return send(res, 200, [groupDetail.group])
  if (req.method === 'GET' && p === '/groups/visible') return send(res, 200, [groupDetail.group])
  if (req.method === 'GET' && p === `/groups/${GROUP_ID}`) return send(res, 200, groupDetail)
  if (req.method === 'GET' && p === `/groups/${GROUP_ID}/settings`) return send(res, 200, groupDetail)
  if (req.method === 'GET' && p === `/groups/${GROUP_ID}/pending`) return send(res, 200, pendingShares)
  if (req.method === 'GET' && p === `/groups/${GROUP_ID}/my-shares`) return send(res, 200, myShares)
  if (req.method === 'GET' && p === `/groups/${GROUP_ID}/transcripts`) {
    return send(res, 200, { transcripts: groupTranscripts })
  }
  if (req.method === 'GET' && p === `/groups/${GROUP_ID}/contributable`) return send(res, 200, contributable)
  // No repository is linked here. Answered rather than refused, so the panel
  // renders its own empty state instead of a failure the capture would have to
  // explain away.
  if (req.method === 'GET' && p === `/groups/${GROUP_ID}/repositories`) return send(res, 200, { repositories: [] })

  return send(res, 401, { error: 'unauthenticated (mock)' })
})

/* A busy port is a HARD failure, never a silent reuse.

   Binding is what proves the capture read THIS fixture: if another server
   already holds the port, the app would answer from that server's data and the
   run would produce a plausible image of the wrong dataset. Exiting non-zero
   makes the capture chain stop instead. */
server.on('error', (err) => {
  if (err.code === 'EADDRINUSE') {
    console.error(
      `ERROR [mock-rest-child-sessions.mjs] port ${PORT} is already in use.\n` +
        `  Why: another process is bound to it, so this mock never served a byte.\n` +
        `  Means: the app would read that other server's fixtures and the capture would show the wrong data.\n` +
        `  Fix: stop the process holding port ${PORT}, or set MOCK_REST_PORT to a free port, and retry.`,
    )
  } else {
    console.error(`ERROR [mock-rest-child-sessions.mjs] could not listen on port ${PORT}: ${err.message}`)
  }
  process.exit(1)
})

server.listen(PORT, () =>
  console.log(
    `mock-rest-child-sessions: serving collective "${GROUP_ID}" on http://localhost:${PORT}/api/v1 (point NEXT_PUBLIC_API_URL here)`,
  ),
)
