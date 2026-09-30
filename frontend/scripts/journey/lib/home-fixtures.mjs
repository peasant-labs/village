/* Home and settings fixtures for the journey harness.
 *
 * The signed-in person here is the explore mock's `alice-dev`, so the header,
 * the home page and the settings page all describe the same account. The rows
 * follow the home wireframe: six transcripts across four projects, with none,
 * one and several pull requests, shared with one or two collectives or with
 * none. Ages are relative to the harness's frozen clock (2024-01-01T12:00Z).
 *
 * Scenarios (set by a journey through `setScenario`):
 *   default     the rows above, two peasant sign-ins, one transcript waiting
 *   empty       no transcripts at all (the composed mock's own empty scenario)
 *   no-keys     as default, but peasant is signed in nowhere
 *
 * `handleHomeRequest` composes into `scripts/journey/mock.mjs` the way
 * `handleExploreRequest` does: it answers true when it served the request. It
 * only claims the OWNER-scoped transcript list; the browse list stays with the
 * explore half.
 */

const PORT = Number(process.env.JOURNEY_MOCK_PORT || 8799)

export const HOME_VIEWER = 'alice-dev'

const at = (minutesAgo) => new Date(Date.UTC(2024, 0, 1, 12, 0, 0) - minutesAgo * 60_000).toISOString()
const HOUR = 60
const DAY = 24 * HOUR

const hash = (c) => c.repeat(64)

const COLLECTIVES = [
  { id: '11111111-1111-4111-8111-111111111111', name: 'Acme Platform', role: 'owner', member_count: 12 },
  { id: '22222222-2222-4222-8222-222222222222', name: 'Acme Company', role: 'member', member_count: 87 },
  { id: '33333333-3333-4333-8333-333333333333', name: 'ML Reading Group', role: 'contributor', member_count: 21 },
]

const pr = (repo, number) => {
  const [owner, name] = repo.split('/')
  return { owner, name, number }
}

/** The home rows, newest first as the server answers them. */
export const HOME_ROWS = [
  {
    id: 'ct-home-1',
    title: 'Fix flaky ingest test',
    project: ['ingest-api', hash('a')],
    branch: 'fix/flaky-ingest',
    minutesAgo: 12,
    sharedWith: ['Acme Platform', 'Acme Company'],
    pullRequests: { count: 4, recent: [pr('acme/ingest-api', 45), pr('acme/ingest-api', 42), pr('acme/web', 7)] },
  },
  {
    id: 'ct-home-2',
    title: 'Guard empty turns in the digest',
    project: ['ingest-api', hash('a')],
    branch: 'fix/flaky-ingest',
    minutesAgo: 2 * HOUR,
    sharedWith: ['Acme Platform', 'Acme Company'],
    pullRequests: { count: 1, recent: [pr('acme/ingest-api', 42)] },
  },
  {
    id: 'ct-home-3',
    title: 'Paginate the collectives list',
    project: ['web', hash('b')],
    branch: 'feat/paginate',
    minutesAgo: DAY + HOUR,
    sharedWith: ['Acme Platform'],
    pullRequests: { count: 2, recent: [pr('acme/web', 33), pr('acme/web', 31)] },
  },
  {
    id: 'ct-home-4',
    title: 'Migrate retry queue to worker',
    project: ['worker', hash('c')],
    branch: 'develop',
    minutesAgo: 2 * DAY,
    sharedWith: ['Acme Platform'],
    pullRequests: { count: 0, recent: [] },
  },
  {
    id: 'ct-home-5',
    title: 'Reproduce the tokenizer benchmark',
    project: ['ml-notes', hash('d')],
    branch: 'main',
    minutesAgo: 3 * DAY,
    sharedWith: [],
    pullRequests: { count: 0, recent: [] },
  },
  {
    id: 'ct-home-6',
    title: 'Refactoring database queries',
    project: ['ingest-api', hash('a')],
    branch: 'develop',
    minutesAgo: 8 * DAY,
    sharedWith: ['Acme Platform'],
    pullRequests: { count: 3, recent: [pr('acme/ingest-api', 38), pr('acme/ingest-api', 36), pr('acme/ingest-api', 30)] },
  },
]

const owner = {
  id: 'user-demo',
  github_id: 123456,
  github_username: HOME_VIEWER,
  display_name: 'Alice Developer',
  avatar_url: null,
  created_at: '2026-06-01T12:00:00Z',
  updated_at: '2026-06-01T12:00:00Z',
  is_discoverable: true,
  username_chosen: true,
  provider_username: HOME_VIEWER,
}

const transcript = (row) => ({
  id: row.id,
  owner_id: owner.id,
  local_id: `local-${row.id}`,
  title: row.title,
  description: null,
  visibility: row.sharedWith.length > 0 ? 'shared' : 'private',
  model_provider: 'claude-code',
  model_name: 'claude-fable-5',
  harness_version: null,
  session_start: at(row.minutesAgo + 40),
  session_end: at(row.minutesAgo + 5),
  turn_count: 24,
  token_count: 18000,
  blob_size_bytes: null,
  schema_version: '0.14.0',
  published_at: at(row.minutesAgo),
  updated_at: at(row.minutesAgo),
  parent_session_id: null,
  ingested_at: null,
  source_format: null,
  git_branch: row.branch,
  git_remote: null,
  project_hash: row.project[1],
  project_name: row.project[0],
  project_display_name: row.project[0],
  project_name_source: 'consented',
  project_remote_label: `github.com/acme/${row.project[0]}`,
  tool_call_count: null,
  subagent_count: null,
  duration_ms: 35 * 60_000,
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
  session_origin: 'user',
})

const listItem = (row) => ({
  transcript: transcript(row),
  tags: [],
  owner,
  owner_orgs: null,
  attestations: null,
  shares: row.sharedWith.map((name) => {
    const collective = COLLECTIVES.find((c) => c.name === name)
    return {
      transcript_id: row.id,
      group_id: collective.id,
      group_name: name,
      acceptance_mode: 'open',
      status: 'approved',
      shared_at: at(row.minutesAgo),
    }
  }),
  pull_requests: row.pullRequests,
})

const groupRow = (c) => ({
  id: c.id,
  name: c.name,
  description: null,
  created_by: 'user-demo',
  created_at: '2023-06-01T12:00:00Z',
  updated_at: '2023-06-01T12:00:00Z',
  acceptance_mode: c.name === 'ML Reading Group' ? 'curated' : 'open',
  data_access: 'members',
  display_members: true,
  linked_github_org: c.name.startsWith('Acme') ? 'acme' : null,
  transcript_deletion_policy: 'retain',
  role: c.role,
  member_since: '2023-06-01T12:00:00Z',
  member_count: c.member_count,
  transcript_count: 10,
})

const KEYS = [
  { id: 'key-laptop', key_prefix: 'pk_laptop', label: 'peasant-cli', created_at: '2023-12-01T12:00:00Z', last_used: at(12), revoked_at: null },
  { id: 'key-desktop', key_prefix: 'pk_desktop', label: 'peasant-cli', created_at: '2023-11-01T12:00:00Z', last_used: at(3 * DAY), revoked_at: null },
  { id: 'key-old', key_prefix: 'pk_old', label: 'peasant-cli', created_at: '2023-06-01T12:00:00Z', last_used: at(90 * DAY), revoked_at: '2023-09-01T12:00:00Z' },
]

const send = (res, code, body) => {
  res.writeHead(code, {
    'content-type': 'application/json',
    'access-control-allow-origin': '*',
    'access-control-allow-headers': '*',
    'access-control-allow-methods': 'GET,POST,PATCH,DELETE,OPTIONS',
  })
  res.end(body == null ? '' : JSON.stringify(body))
}

const readBody = (req) =>
  new Promise((resolve) => {
    let data = ''
    req.on('data', (chunk) => { data += chunk })
    req.on('end', () => resolve(data))
  })

/** The handle somebody else already holds, so a journey can show the refusal. */
export const TAKEN_HANDLE = 'bob-ai'

/**
 * Handle one home or settings request. Resolves true when the route was served.
 * `scenario` is the composed mock's current scenario name.
 */
export async function handleHomeRequest(req, res, scenario) {
  const url = new URL(req.url, `http://localhost:${PORT}`)
  const path = url.pathname.replace(/^\/api\/v1/, '')

  // The owner-scoped list, a page at a time, searched by title.
  if (
    req.method === 'GET' &&
    path === '/transcripts' &&
    url.searchParams.get('owner') === HOME_VIEWER &&
    url.searchParams.get('view') !== 'grouped'
  ) {
    const page = Math.max(1, Number(url.searchParams.get('page') || '1'))
    const limit = Math.max(1, Number(url.searchParams.get('limit') || '20'))
    const q = (url.searchParams.get('q') || '').toLowerCase()
    const matching = scenario === 'empty' ? [] : HOME_ROWS.filter((row) => row.title.toLowerCase().includes(q))
    send(res, 200, {
      transcripts: matching.slice((page - 1) * limit, page * limit).map(listItem),
      total: matching.length,
      agent_total: 0,
      page,
      limit,
    })
    return true
  }
  if (
    req.method === 'GET' &&
    path === '/transcripts' &&
    url.searchParams.get('owner') === HOME_VIEWER &&
    url.searchParams.get('view') === 'grouped'
  ) {
    send(res, 200, {
      items: [],
      page: Number(url.searchParams.get('page') || '1'),
      limit: Number(url.searchParams.get('limit') || '100'),
      totalItems: 0,
      ordinarySessionTotal: 0,
      helperThreadTotal: 0,
    })
    return true
  }
  if (req.method === 'GET' && path === '/users/me/stats') {
    const empty = scenario === 'empty'
    send(res, 200, {
      total_transcripts: empty ? 0 : 38,
      total_turns: empty ? 0 : 1240,
      total_duration_ms: empty ? 0 : 31 * 3_600_000 + 12 * 60_000,
      total_tokens: empty ? 0 : 19_100_000,
      pull_request_count: empty ? 0 : 9,
    })
    return true
  }
  if (req.method === 'GET' && path === '/groups') {
    send(res, 200, COLLECTIVES.map(groupRow))
    return true
  }
  if (req.method === 'GET' && path === '/users/me/collectives/contributions') {
    send(res, 200, {
      collectives: [
        { id: COLLECTIVES[2].id, name: COLLECTIVES[2].name, description: null, linked_github_org: null, approved_count: 3, pending_count: scenario === 'empty' ? 0 : 1, rejected_attempt_count: 0, withdrawn_attempt_count: 0 },
        { id: COLLECTIVES[0].id, name: COLLECTIVES[0].name, description: null, linked_github_org: 'acme', approved_count: 30, pending_count: 0, rejected_attempt_count: 0, withdrawn_attempt_count: 0 },
      ],
    })
    return true
  }
  if (req.method === 'GET' && path === '/users/me/settings') {
    send(res, 200, { auto_attach_pull_requests: false, preview_before_attach: false })
    return true
  }
  if (req.method === 'GET' && path === '/auth/api-keys') {
    send(res, 200, scenario === 'no-keys' ? [] : KEYS)
    return true
  }
  if (req.method === 'PATCH' && path === '/auth/me/username') {
    const body = JSON.parse((await readBody(req)) || '{}')
    if (body.username === TAKEN_HANDLE) {
      send(res, 409, { error: 'That username is already taken' })
    } else {
      send(res, 200, { ...owner, github_username: body.username })
    }
    return true
  }
  if (req.method === 'PATCH' && path === '/auth/me/settings') {
    const body = JSON.parse((await readBody(req)) || '{}')
    send(res, 200, { ...owner, is_discoverable: Boolean(body.is_discoverable) })
    return true
  }
  return false
}
