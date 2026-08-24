/* Minimal REST mock for the Explore visual gate.

   The Explore page fetches three browse endpoints on first load:
     - GET /api/v1/transcripts?...
     - GET /api/v1/groups/search?q=...
     - GET /api/v1/tags/popular?limit=...

   This mock serves a representative dataset for those requests so the visual
   boot arm can exercise the real page, adapter, and query hooks without a real
   backend. It intentionally stays self-contained and does not share code with
   the transcript boot mock.

   Usage:
     MOCK_REST_PORT=8789 node scripts/visual/mock-rest-explore.mjs
*/
import { createServer } from 'node:http'

const PORT = Number(process.env.MOCK_REST_PORT || 8789)

const owners = {
  'alice-dev': { github_username: 'alice-dev', display_name: 'Alice Developer', avatar_url: null },
  'bob-ai': { github_username: 'bob-ai', display_name: 'Bob AI', avatar_url: null },
  'charlie-ml': { github_username: 'charlie-ml', display_name: 'Charlie ML', avatar_url: null },
  'dana-ops': { github_username: 'dana-ops', display_name: 'Dana Ops', avatar_url: null },
}

// The Explore capture is now taken signed-in (explore-shoot.mjs sets the same peasant_token
// cookie manage-shoot.mjs uses) so the nav shows its full explore/collectives/publish/profile
// set, matching the demo's unconditional full nav and matching manage's own capture state —
// the "cex-explore navbar wrong: missing collectives/publish/profile" finding was this mock server
// never serving /auth/me at all (every route fell through to 404, so useAuth() always resolved
// signed-out regardless of the cookie).
const user = {
  id: 'user-demo',
  github_id: 123456,
  github_username: 'alice-dev',
  display_name: 'Alice Developer',
  avatar_url: null,
  created_at: '2026-06-01T12:00:00Z',
  updated_at: '2026-06-01T12:00:00Z',
  is_discoverable: true,
  // Without these, UsernameGate (mounted in LayoutShell for every page, including Explore)
  // treats the mock user as still needing to claim a handle and renders the "claim your
  // handle" onboarding screen instead of the actual page content.
  username_chosen: true,
  provider_username: 'alice-dev',
}

const transcripts = [
  {
    id: 'd41a8e',
    title: 'Building a REST API from scratch',
    visibility: 'public',
    model_provider: 'claude-code',
    model_name: 'Claude Opus 4.5',
    harness_version: '2026.06',
    session_start: '2026-06-15T00:00:00Z',
    session_end: '2026-06-15T01:20:00Z',
    turn_count: 128,
    token_count: 412300,
    tool_call_count: 37,
    duration_ms: 4_800_000,
    git_branch: 'main',
    project_name: 'go-rest-api',
    tags: ['greenfield', 'claude-code'],
    owner: owners['alice-dev'],
  },
  {
    id: '7c2b90',
    title: 'Debugging auth middleware with Claude Code',
    visibility: 'shared',
    model_provider: 'claude-code',
    model_name: 'Claude Sonnet 4.5',
    harness_version: '2026.06',
    session_start: '2026-06-14T00:00:00Z',
    session_end: '2026-06-14T00:45:00Z',
    turn_count: 64,
    token_count: 138400,
    tool_call_count: 19,
    duration_ms: 2_700_000,
    git_branch: 'develop',
    project_name: 'village',
    tags: ['debugging', 'claude-code'],
    owner: owners['alice-dev'],
  },
  {
    id: 'b9f33c',
    title: 'Refactoring database queries with Gemini',
    visibility: 'public',
    model_provider: 'gemini-cli',
    model_name: 'Gemini 2.5 Pro',
    harness_version: '2026.06',
    session_start: '2026-06-13T00:00:00Z',
    session_end: '2026-06-13T01:36:00Z',
    turn_count: 91,
    token_count: 221700,
    tool_call_count: 28,
    duration_ms: 5_760_000,
    git_branch: 'develop',
    project_name: 'api-server',
    tags: ['refactoring', 'gemini-cli'],
    owner: owners['charlie-ml'],
  },
  {
    id: 'e2107a',
    title: 'Greenfield React app setup',
    visibility: 'public',
    model_provider: 'opencode',
    model_name: 'OpenCode',
    harness_version: '2026.06',
    session_start: '2026-06-12T00:00:00Z',
    session_end: '2026-06-12T00:38:00Z',
    turn_count: 47,
    token_count: 96200,
    tool_call_count: 14,
    duration_ms: 2_280_000,
    git_branch: 'feature/lift',
    project_name: 'frontend-app',
    tags: ['greenfield', 'iterative-refinement'],
    owner: owners['bob-ai'],
  },
  {
    id: 'a5d8c1',
    title: 'Multi-agent debugging session',
    visibility: 'public',
    model_provider: 'claude-code',
    model_name: 'Claude Opus 4.5',
    harness_version: '2026.06',
    session_start: '2026-06-11T00:00:00Z',
    session_end: '2026-06-11T03:31:00Z',
    turn_count: 203,
    token_count: 688900,
    tool_call_count: 64,
    duration_ms: 12_660_000,
    git_branch: 'main',
    project_name: 'platform',
    tags: ['multi-agent', 'debugging'],
    owner: owners['charlie-ml'],
  },
  {
    id: 'f0b412',
    title: 'Untitled transcript',
    visibility: 'private',
    model_provider: 'codex',
    model_name: 'Codex',
    harness_version: '2026.06',
    session_start: '2026-06-10T00:00:00Z',
    session_end: '2026-06-10T00:12:00Z',
    turn_count: 18,
    token_count: 21400,
    tool_call_count: 5,
    duration_ms: 720_000,
    git_branch: 'scratch',
    project_name: 'scratch',
    tags: ['iterative-refinement'],
    owner: { github_username: 'anon', display_name: null, avatar_url: null },
  },
]

const collectives = [
  {
    id: 'ai-research-team',
    name: 'AI Research Team',
    description: 'Sharing transcripts related to AI research',
    linked_github_org: 'anthropic-labs',
    member_count: 12,
    transcript_count: 48,
  },
  {
    id: 'verified-contributors',
    name: 'Verified Contributors',
    description: 'Only verified org members can share here',
    linked_github_org: 'data-collective',
    member_count: 31,
    transcript_count: 126,
  },
  {
    id: 'curated-showcase',
    name: 'Curated Showcase',
    description: 'Selected examples with clean browse cards',
    linked_github_org: null,
    member_count: 9,
    transcript_count: 21,
  },
]

const popularTags = [
  { id: 'claude-code', name: 'claude-code', usage_count: 41 },
  { id: 'debugging', name: 'debugging', usage_count: 33 },
  { id: 'gemini-cli', name: 'gemini-cli', usage_count: 28 },
  { id: 'refactoring', name: 'refactoring', usage_count: 24 },
  { id: 'greenfield', name: 'greenfield', usage_count: 17 },
  { id: 'iterative-refinement', name: 'iterative-refinement', usage_count: 14 },
]

const send = (res, code, body) => {
  res.writeHead(code, {
    'content-type': 'application/json',
    'access-control-allow-origin': '*',
    'access-control-allow-headers': '*',
    'access-control-allow-methods': 'GET,POST,DELETE,OPTIONS',
  })
  res.end(body == null ? '' : JSON.stringify(body))
}

const toListItem = (row, sessionOrigin) => ({
  transcript: {
    id: row.id,
    owner_id: `owner-${row.owner.github_username || 'anon'}`,
    local_id: row.id,
    title: row.title,
    description: `${row.project_name} browse fixture`,
    visibility: row.visibility,
    model_provider: row.model_provider,
    model_name: row.model_name,
    harness_version: row.harness_version,
    session_start: row.session_start,
    session_end: row.session_end,
    turn_count: row.turn_count,
    token_count: row.token_count,
    blob_key: `blob-${row.id}`,
    blob_size_bytes: 0,
    schema_version: '0.1.0',
    published_at: row.session_start,
    updated_at: row.session_end,
    parent_session_id: null,
    ingested_at: row.session_end,
    source_file_path: null,
    source_format: 'json',
    git_branch: row.git_branch,
    git_remote: null,
    git_worktree: null,
    project_hash: null,
    project_path: null,
    project_name: row.project_name,
    tool_call_count: row.tool_call_count,
    subagent_count: 0,
    duration_ms: row.duration_ms,
    tokens_in: null,
    tokens_out: null,
    subagents: null,
    diagnostics_warnings: null,
    diagnostics_partial: null,
    session_origin: sessionOrigin,
  },
  tags: row.tags.map((name) => ({ id: name, name })),
  owner: row.owner,
})

// Agent-driven sessions: no person prompted them in-band. The server keeps them
// out of the default list and reports how many it kept out, so the page can
// render the collapsed group. This mock serves the same two scopes.
const agentTranscripts = [
  {
    id: 'a91f30',
    title: 'Sweep the discovery handlers for unbounded queries',
    visibility: 'public',
    model_provider: 'claude-code',
    model_name: 'Claude Opus 4.5',
    harness_version: '2026.08',
    session_start: '2026-08-18T09:00:00Z',
    session_end: '2026-08-18T09:41:00Z',
    turn_count: 64,
    token_count: 188400,
    tool_call_count: 22,
    duration_ms: 2_460_000,
    git_branch: 'develop',
    project_name: 'village',
    tags: ['claude-code'],
    owner: owners['alice-dev'],
  },
  {
    id: 'b7c214',
    title: 'Port the fixture loaders onto the shared row-count guard',
    visibility: 'public',
    model_provider: 'claude-code',
    model_name: 'Claude Opus 4.5',
    harness_version: '2026.08',
    session_start: '2026-08-17T14:10:00Z',
    session_end: '2026-08-17T14:52:00Z',
    turn_count: 51,
    token_count: 142900,
    tool_call_count: 18,
    duration_ms: 2_520_000,
    git_branch: 'develop',
    project_name: 'village',
    tags: ['refactoring'],
    owner: owners['bob-ai'],
  },
  {
    id: 'c30d55',
    title: 'Reproduce the tied-row reordering across discovery pages',
    visibility: 'public',
    model_provider: 'gemini-cli',
    model_name: 'Gemini 3 Pro',
    harness_version: '2026.07',
    session_start: '2026-08-16T11:05:00Z',
    session_end: '2026-08-16T11:29:00Z',
    turn_count: 33,
    token_count: 96200,
    tool_call_count: 11,
    duration_ms: 1_440_000,
    git_branch: 'develop',
    project_name: 'village',
    tags: ['debugging'],
    owner: owners['charlie-ml'],
  },
  {
    id: 'd0e918',
    title: 'Collect the redaction rule versions used by stored transcripts',
    visibility: 'public',
    model_provider: 'claude-code',
    model_name: 'Claude Sonnet 4.5',
    harness_version: '2026.08',
    session_start: '2026-08-15T16:40:00Z',
    session_end: '2026-08-15T16:58:00Z',
    turn_count: 27,
    token_count: 71300,
    tool_call_count: 9,
    duration_ms: 1_080_000,
    git_branch: 'develop',
    project_name: 'village',
    tags: ['claude-code'],
    owner: owners['dana-ops'],
  },
]

const transcriptListItems = transcripts.map((row) => toListItem(row, 'user'))
const agentListItems = agentTranscripts.map((row) => toListItem(row, 'agent'))

const filterTranscripts = (url) => {
  const q = url.searchParams.get('q')?.trim().toLowerCase() || ''
  const provider = url.searchParams.get('provider') || 'all'
  const tags = (url.searchParams.get('tags') || '')
    .split(',')
    .map((part) => part.trim())
    .filter(Boolean)
  const sort = url.searchParams.get('sort') || 'recent'
  const page = Math.max(1, Number(url.searchParams.get('page') || '1'))
  const limit = Math.max(1, Number(url.searchParams.get('limit') || '24'))
  // Discovery scope, matching the server: absent, the list carries everything
  // EXCEPT agent-driven sessions; origin=agent carries only them.
  const origin = url.searchParams.get('origin') || ''

  const matches = (row) => {
    if (provider !== 'all' && row.transcript.model_provider !== provider) return false
    if (tags.length > 0 && !row.tags.some((tag) => tags.includes(tag.name))) return false
    if (q) {
      const hay = [
        row.transcript.title,
        row.transcript.model_name,
        row.transcript.project_name,
        row.owner.github_username,
        ...row.tags.map((tag) => tag.name),
      ]
        .filter(Boolean)
        .join(' ')
        .toLowerCase()
      if (!hay.includes(q)) return false
    }
    return true
  }

  let rows = (origin === 'agent' ? agentListItems : transcriptListItems).filter(matches)
  const agentTotal = agentListItems.filter(matches).length

  rows = rows.slice().sort((a, b) => {
    if (sort === 'turns') return (b.transcript.turn_count || 0) - (a.transcript.turn_count || 0)
    if (sort === 'tokens') return (b.transcript.token_count || 0) - (a.transcript.token_count || 0)
    return String(b.transcript.session_start || b.transcript.session_end || '').localeCompare(String(a.transcript.session_start || a.transcript.session_end || ''))
  })

  const start = (page - 1) * limit
  const items = rows.slice(start, start + limit)
  return { transcripts: items, total: rows.length, agent_total: agentTotal, page, limit }
}

const filterCollectives = (url) => {
  const q = url.searchParams.get('q')?.trim().toLowerCase() || ''
  return {
    collectives: collectives
      .filter((collective) => {
        if (!q) return true
        return [collective.name, collective.description, collective.linked_github_org]
          .filter(Boolean)
          .join(' ')
          .toLowerCase()
          .includes(q)
      })
      .map((collective) => ({
        id: collective.id,
        name: collective.name,
        description: collective.description,
        linked_github_org: collective.linked_github_org,
        member_count: collective.member_count,
        transcript_count: collective.transcript_count,
      })),
  }
}

const filterPopularTags = (url) => {
  const limit = Math.max(1, Number(url.searchParams.get('limit') || '10'))
  return popularTags.slice(0, limit)
}

const server = createServer((req, res) => {
  const url = new URL(req.url, `http://localhost:${PORT}`)
  const path = url.pathname.replace(/^\/api\/v1/, '')
  if (req.method === 'OPTIONS') return send(res, 204, null)
  console.log(`${req.method} ${url.pathname}`)

  if (req.method === 'GET' && path === '/transcripts') return send(res, 200, filterTranscripts(url))
  if (req.method === 'GET' && path === '/groups/search') return send(res, 200, filterCollectives(url))
  if (req.method === 'GET' && path === '/tags/popular') return send(res, 200, filterPopularTags(url))
  if (req.method === 'GET' && path === '/groups') return send(res, 200, [])
  if (req.method === 'GET' && path === '/auth/me') return send(res, 200, user)
  if (req.method === 'GET' && path === '/auth/orgs') return send(res, 200, [])

  return send(res, 404, { error: `no mock route for ${req.method} ${url.pathname}` })
})

server.listen(PORT, () => {
  console.log(`mock-rest-explore: serving browse fixtures on http://localhost:${PORT}/api/v1`)
})
