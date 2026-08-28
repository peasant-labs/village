/* Minimal REST mock for the project-page visual gate.

   The project page (`/users/{username}/projects/{projectHash}`) fetches:
     - GET    /api/v1/auth/me
     - GET    /api/v1/users/{username}/projects/{projectHash}
     - PATCH  /api/v1/users/me/projects/{projectHash}
     - DELETE /api/v1/users/me/projects/{projectHash}/display-name

   The PROFILE page (`/users/{username}`), whose project cards link into the
   project page, fetches:
     - GET    /api/v1/users/{username}
     - GET    /api/v1/transcripts?owner={username}

   The correction routes are served live (in memory) so a capture can be taken
   before and after a reset, and so the control is exercised the way a user
   exercises it rather than being screenshotted in one frozen state.

   MOCK_PROJECT_VIEWER selects who is looking:
     owner  (default) the project's owner, so the correction control renders
     other               a different signed-in user
     anon                nobody signed in

   MOCK_PROJECT_ROLLUP=empty serves an empty collectives roll-up, the ordinary
   answer for a viewer the visibility gate and the contributor opt-in exclude.

   MOCK_PROJECT_IDENTITY selects which evidence the project's name is resolved
   from:
     remote (default) an owner rename over a project that also has a git remote,
            so the heading carries a chosen name and the subtitle a repository
            label
     path   a project with NO git remote and no chosen or disclosed name, whose
            name is therefore the redacted local path its publisher recorded
            ("/<PATH>/sample-app"). There is no repository label to render, so
            the subtitle is absent — that absence is the point of the capture.

   Usage:
     MOCK_REST_PORT=8790 node scripts/visual/mock-rest-project.mjs
*/
import { createServer } from 'node:http'

const PORT = Number(process.env.MOCK_REST_PORT || 8790)
const VIEWER = process.env.MOCK_PROJECT_VIEWER || 'owner'
const ROLLUP = process.env.MOCK_PROJECT_ROLLUP || 'full'

export const PROJECT_HASH = 'a3f1c07d5b9e42618c0d7f4a2b6e8901d3c5a7f9b1e2d4c6a8f0b2d4e6f80123'
const OWNER_USERNAME = 'alice-dev'

const makeUser = (username, displayName) => ({
  id: `user-${username}`,
  github_id: 123456,
  github_username: username,
  display_name: displayName,
  avatar_url: null,
  created_at: '2026-06-01T12:00:00Z',
  updated_at: '2026-06-01T12:00:00Z',
  is_discoverable: true,
  // Without these, UsernameGate (mounted in LayoutShell on every page) renders
  // the handle-claim onboarding instead of the route under capture.
  username_chosen: true,
  provider_username: username,
})

const owner = makeUser(OWNER_USERNAME, 'Alice Developer')
const otherViewer = makeUser('bob-reviewer', 'Bob Reviewer')

// Live identity state. The correction routes mutate it, and the page re-reads
// it, so a capture taken after a reset shows the resolved default rather than
// an echo of what was typed.
// The override name carries CAPITALS on purpose. A project's display name is
// user content, and the design system lowercases h1/h2/h3 as chrome, so a
// lowercase-only fixture cannot show whether the name survives as typed. Both
// the project page heading and the profile project card render this string.
const IDENTITY = process.env.MOCK_PROJECT_IDENTITY || 'remote'

// The path-tier project. Its name IS the redacted local path its publisher
// recorded: no owner rename, no disclosed project name, and no git remote to
// label. The path is served exactly as a publishing client would send it —
// already redacted, with no account name and no folders above the project — so
// the capture shows what a real reader of this page would see, and reviewing the
// screenshot is a real check that Village renders that value verbatim.
const PATH_TIER = { displayName: '/<PATH>/sample-app', nameSource: 'path' }

let displayName = IDENTITY === 'path' ? PATH_TIER.displayName : 'The Village'
let nameSource = IDENTITY === 'path' ? PATH_TIER.nameSource : 'override'
// A project resolved from its path has no repository to label. The empty string
// (never null) is what the wire carries for "no label"; the page must render no
// subtitle at all rather than an empty one.
const REMOTE_LABEL = IDENTITY === 'path' ? '' : 'github.com:peasant-labs/village'
const RESOLVED_DEFAULT =
  IDENTITY === 'path' ? PATH_TIER : { displayName: 'village', nameSource: 'consented' }

const sessions = [
  { title: 'Wire the publish guard to the project hash', provider: 'claude-code', turns: 42, published: '2026-08-21T10:00:00Z' },
  { title: 'Rework the share-attempt history', provider: 'claude-code', turns: 28, published: '2026-08-19T16:30:00Z' },
  { title: 'Collectives roll-up query', provider: 'opencode', turns: 17, published: '2026-08-17T09:05:00Z' },
  { title: 'Breadcrumb routing for project pages', provider: 'gemini-cli', turns: 9, published: '2026-08-14T13:45:00Z' },
]

const collectives = [
  {
    id: '11111111-1111-4111-8111-111111111111',
    name: 'AI Research Team',
    description: 'Applied agent research, published in the open.',
    linked_github_org: 'ai-research',
    transcript_count: 3,
  },
  {
    id: '22222222-2222-4222-8222-222222222222',
    name: 'Verified Contributors',
    description: null,
    linked_github_org: null,
    transcript_count: 1,
  },
]

const transcript = (s, i) => ({
  id: `transcript-${i}`,
  owner_id: owner.id,
  local_id: `local-${i}`,
  title: s.title,
  description: null,
  visibility: 'public',
  model_provider: s.provider,
  model_name: 'claude-fable-5',
  harness_version: null,
  session_start: s.published,
  session_end: s.published,
  turn_count: s.turns,
  token_count: 12000,
  blob_size_bytes: null,
  schema_version: '0.13.0',
  published_at: s.published,
  updated_at: s.published,
  parent_session_id: null,
  ingested_at: null,
  source_format: null,
  git_branch: null,
  git_remote: IDENTITY === 'path' ? null : 'git@github.com:peasant-labs/village.git',
  project_hash: PROJECT_HASH,
  // A path-tier project disclosed no project name at all; that absence is why
  // the resolver falls through to the path.
  project_name: IDENTITY === 'path' ? null : 'village',
  project_display_name: displayName,
  project_name_source: nameSource,
  project_remote_label: REMOTE_LABEL,
  tool_call_count: null,
  subagent_count: null,
  duration_ms: null,
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

const resolvedProject = () => ({
  project_hash: PROJECT_HASH,
  project_display_name: displayName,
  project_name_source: nameSource,
  project_remote_label: REMOTE_LABEL,
})

const projectPage = () => ({
  project: resolvedProject(),
  owner,
  transcripts: sessions.map(transcript),
  collectives: ROLLUP === 'empty' ? [] : collectives,
})

/** The profile route's list shape: the same sessions, wrapped as list items. */
const profileTranscripts = () => ({
  transcripts: sessions.map((s, i) => ({
    transcript: transcript(s, i),
    tags: [],
    owner,
  })),
  total: sessions.length,
  agent_total: 0,
  page: 1,
  limit: 20,
})

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
    let raw = ''
    req.on('data', (c) => {
      raw += c
    })
    req.on('end', () => {
      try {
        resolve(JSON.parse(raw || '{}'))
      } catch {
        resolve({})
      }
    })
  })

const server = createServer(async (req, res) => {
  const url = new URL(req.url, `http://localhost:${PORT}`)
  const path = url.pathname.replace(/^\/api\/v1/, '')
  if (req.method === 'OPTIONS') return send(res, 204, null)
  console.log(`${req.method} ${url.pathname}`)

  if (req.method === 'GET' && path === '/auth/me') {
    if (VIEWER === 'anon') return send(res, 401, { error: 'unauthenticated' })
    return send(res, 200, VIEWER === 'other' ? otherViewer : owner)
  }
  if (req.method === 'GET' && path === '/auth/orgs') return send(res, 200, [])
  if (req.method === 'GET' && path === '/groups') return send(res, 200, [])

  if (req.method === 'PATCH' && path === `/users/me/projects/${PROJECT_HASH}`) {
    const body = await readBody(req)
    displayName = String(body.display_name || displayName)
    nameSource = 'override'
    return send(res, 200, resolvedProject())
  }
  if (req.method === 'DELETE' && path === `/users/me/projects/${PROJECT_HASH}/display-name`) {
    displayName = RESOLVED_DEFAULT.displayName
    nameSource = RESOLVED_DEFAULT.nameSource
    return send(res, 200, resolvedProject())
  }
  if (req.method === 'GET' && path === `/users/${OWNER_USERNAME}/projects/${PROJECT_HASH}`) {
    return send(res, 200, projectPage())
  }
  // The PROFILE page, whose project cards link into the project page. It is
  // served from the SAME identity state, so the card heading and the project
  // heading always render the same string.
  if (req.method === 'GET' && path === `/users/${OWNER_USERNAME}`) {
    return send(res, 200, owner)
  }
  if (req.method === 'GET' && path === '/transcripts') {
    return send(res, 200, profileTranscripts())
  }
  // Every other project page answers the way the visibility boundary answers:
  // one 404, with no wording that says which case it was.
  if (req.method === 'GET' && /^\/users\/[^/]+\/projects\/[^/]+$/.test(path)) {
    return send(res, 404, { error: 'no such project page' })
  }

  return send(res, 404, { error: `no mock route for ${req.method} ${url.pathname}` })
})

server.listen(PORT, () => {
  console.log(
    `mock-rest-project: serving project-page fixtures on http://localhost:${PORT}/api/v1 ` +
      `(viewer=${VIEWER}, rollup=${ROLLUP}, identity=${IDENTITY}, hash=${PROJECT_HASH})`,
  )
})
