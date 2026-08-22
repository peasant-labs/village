/* Minimal REST mock for the host-integration BOOT check (boot-village.mjs).

   The capture harness (village-shoot.mjs) drives a backend-free dev FIXTURE route for determinism, so
   it bypasses village's REAL data path: the `/transcripts/[id]` route → React Query (`useTranscript` +
   `useTranscriptContent`) → REST `GET /transcripts/{id}` + `/transcripts/{id}/content` → the
   SessionDetailV2 adapter → Fairtrade's canonical `<TranscriptViewer>` with transcript-browser's graph
   engine. This mock serves exactly those REST
   endpoints (a representative session) so `boot-village.mjs` can exercise that REAL route+adapter+
   React-Query path with no Postgres/MinIO/auth stack — village's analog of peasant's `--mock-data-store`.

   Point the village frontend at it via `NEXT_PUBLIC_API_URL` (the only env the app reads for its API
   base), then run boot-village against the real route:
     MOCK_REST_PORT=8788 node scripts/visual/mock-rest.mjs &
     NEXT_PUBLIC_API_URL=http://localhost:8788/api/v1 pnpm dev &
     CHROME_PATH=... VILLAGE_TRANSCRIPT=demo node scripts/visual/boot-village.mjs

   env: MOCK_REST_PORT (default 8788), MOCK_TRANSCRIPT_ID (default `demo`). Runs until killed. */
import { createServer } from 'node:http'
import { readFileSync } from 'node:fs'
import { parse } from 'yaml'

const PORT = Number(process.env.MOCK_REST_PORT || 8788)
const ID = process.env.MOCK_TRANSCRIPT_ID || 'demo'
const contractFixtures = parse(
  readFileSync(new URL('../../src/testdata/final-contract-compatibility.yaml', import.meta.url), 'utf8'),
  { strict: true },
)
const observedModelFixture = contractFixtures.observedModelSessions?.find(
  ({ name }) => name === 'sticky-observed-model-transition',
)
if (!observedModelFixture) {
  throw new Error(
    'mock-rest.mjs could not load the sticky-observed-model-transition fixture from src/testdata/final-contract-compatibility.yaml; the mounted production-route evidence cannot run. Restore the strict fixture and retry.',
  )
}
const expectedSourceSequence = [
  'anthropic/claude-fable-5',
  null,
  'anthropic/claude-opus-4-8',
  null,
]
const actualSourceSequence = observedModelFixture.turns.map(({ sourceObservation }) => sourceObservation)
if (JSON.stringify(actualSourceSequence) !== JSON.stringify(expectedSourceSequence)) {
  throw new Error(
    `mock-rest.mjs loaded the wrong observed-model source sequence: got ${JSON.stringify(actualSourceSequence)}; want ${JSON.stringify(expectedSourceSequence)}. The real-route capture must prove A, omission, B, omission, so correct the strict fixture before retrying.`,
  )
}

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

const groupId = process.env.MOCK_GROUP_ID || 'demo-group'

const groupDetail = {
  group: {
    id: groupId,
    name: 'AI Research Collective',
    description: 'Shared governance for the village demo.',
    linked_github_org: 'anthropic-labs',
    display_members: true,
    transcript_deletion_policy: 'user_choice',
    created_by: user.id,
    created_at: '2026-06-01T12:00:00Z',
    updated_at: '2026-06-28T12:00:00Z',
    acceptance_mode: 'open',
    data_access: 'members_only',
    role: 'owner',
    member_since: '2026-06-01T12:00:00Z',
    member_count: 12,
    transcript_count: 248,
  },
  members: [
    {
      id: 'member-1',
      role: 'owner',
      joined_at: '2026-06-01T12:00:00Z',
      github_username: 'alice-dev',
      display_name: 'Alice Developer',
      avatar_url: null,
      github_orgs: ['anthropic-labs'],
    },
    {
      id: 'member-2',
      role: 'member',
      joined_at: '2026-06-10T12:00:00Z',
      github_username: 'bob-ai',
      display_name: 'Bob AI',
      avatar_url: null,
      github_orgs: ['anthropic-labs'],
    },
  ],
  transcripts: [
    {
      id: 'gt-1',
      owner_id: 'member-1',
      local_id: 'sess_demo_0001',
      title: 'Port the transcript canvas into the shared package',
      description: 'Boot fixture transcript.',
      visibility: 'public',
      model_provider: 'claude-code',
      model_name: observedModelFixture.sessionModel,
      harness_version: '1.0.0',
      session_start: '2026-06-17T09:12:00Z',
      session_end: '2026-06-17T09:20:00Z',
      turn_count: 5,
      token_count: 500,
      blob_key: 'blob-1',
      blob_size_bytes: 4096,
      schema_version: '0.2.0',
      published_at: '2026-06-17T09:21:00Z',
      updated_at: '2026-06-17T09:21:00Z',
      parent_session_id: null,
      ingested_at: '2026-06-17T09:22:00Z',
      source_file_path: null,
      source_format: 'json',
      git_branch: 'main',
      git_remote: null,
      git_worktree: null,
      project_hash: null,
      project_path: null,
      project_name: 'transcript-browser',
      tool_call_count: 0,
      subagent_count: 0,
      duration_ms: 480000,
      tokens_in: 300,
      tokens_out: 200,
      subagents: [],
      diagnostics_warnings: [],
      diagnostics_partial: false,
      owner_username: 'alice-dev',
      owner_avatar_url: null,
      owner_is_discoverable: true,
    },
    {
      id: 'gt-2',
      owner_id: 'member-2',
      local_id: 'sess_demo_0002',
      title: 'Lift the manage surface into the shared package',
      description: 'Boot fixture transcript.',
      visibility: 'shared',
      model_provider: 'gemini-cli',
      model_name: 'gemini-2.5-pro',
      harness_version: '1.0.0',
      session_start: '2026-06-18T09:12:00Z',
      session_end: '2026-06-18T09:19:00Z',
      turn_count: 2,
      token_count: 3100,
      blob_key: 'blob-2',
      blob_size_bytes: 2048,
      schema_version: '0.2.0',
      published_at: '2026-06-18T09:20:00Z',
      updated_at: '2026-06-18T09:20:00Z',
      parent_session_id: null,
      ingested_at: '2026-06-18T09:21:00Z',
      source_file_path: null,
      source_format: 'json',
      git_branch: 'main',
      git_remote: null,
      git_worktree: null,
      project_hash: null,
      project_path: null,
      project_name: 'village',
      tool_call_count: 0,
      subagent_count: 0,
      duration_ms: 420000,
      tokens_in: 1800,
      tokens_out: 1300,
      subagents: [],
      diagnostics_warnings: [],
      diagnostics_partial: false,
      owner_username: 'bob-ai',
      owner_avatar_url: null,
      owner_is_discoverable: true,
    },
  ],
  stats: {
    total_transcripts: 2,
    contributor_count: 2,
    total_turns: 5,
    total_duration_ms: 900000,
    total_tokens: 7300,
  },
  models: [
    { model_provider: 'claude-code', transcript_count: 1 },
    { model_provider: 'gemini-cli', transcript_count: 1 },
  ],
  contributors: [
    { id: 'member-1', github_username: 'alice-dev', avatar_url: null, transcript_count: 1 },
    { id: 'member-2', github_username: 'bob-ai', avatar_url: null, transcript_count: 1 },
  ],
  can_read: true,
  your_role: 'owner',
  pending_members: [],
}

const groupsList = [groupDetail.group]

const ts = (min) => new Date(Date.parse('2026-06-17T09:12:00Z') + min * 60_000).toISOString()

// Preserve source omissions at the REST boundary. Fairtrade's adapter, not
// Village, derives effective model carry-forward for rendering.
const observedModelTurns = observedModelFixture.turns.map((turn, fixtureIndex) => {
  const wireTurn = {
    index: fixtureIndex + 1,
    role: 'assistant',
    content: turn.content,
    timestamp: ts(fixtureIndex + 1),
    depth: 0,
    tokensIn: 60,
    tokensOut: 40,
  }
  if (turn.sourceObservation != null) wireTurn.observedModel = turn.sourceObservation
  return wireTurn
})

const content = {
  id: 'sess_demo_0001',
  harness: 'claude-code',
  startTime: ts(0),
  endTime: ts(5),
  durationMins: 5,
  totalTokens: 500,
  tokensIn: 300,
  tokensOut: 200,
  turnCount: 5,
  toolCallCount: 0,
  project: 'observed-model-contract',
  model: observedModelFixture.sessionModel,
  workingDirectory: '/workspace/observed-model-contract',
  outcome: 'resolved',
  turns: [
    {
      index: 0,
      role: 'user',
      content: 'Verify sticky model attribution on the mounted Village transcript route.',
      timestamp: ts(0),
      depth: 0,
      tokensIn: 60,
      tokensOut: 0,
    },
    ...observedModelTurns,
  ],
}

// A diagnostic case for the mounted title-hero + breadcrumb evidence
// (village#32/#33): the default fixture's stored title ('Verify sticky
// model attribution on the mounted Village transcript route') differs from
// its first user turn only by a trailing period, which a screenshot cannot
// distinguish — so it cannot serve as PNG proof that the hero shows the
// STORED title rather than deriving one from the first turn. Setting
// MOCK_TITLE_HERO_DIAGNOSTIC=1 swaps in a null stored title plus a first
// user turn carrying raw harness markup, so a fixed build either shows
// "Untitled transcript" (proving the overlay) or leaks the raw markup
// (proving it does not). Off by default: every other consumer of this mock
// (the observed-model-transition contract probe in boot-village.mjs, which
// asserts an exact turn count and model sequence) is unaffected.
const TITLE_DIAGNOSTIC = process.env.MOCK_TITLE_HERO_DIAGNOSTIC === '1'
if (TITLE_DIAGNOSTIC) {
  content.turns[0].content =
    '<local-command-caveat>Caveat: The messages below were generated by the local command. Do not respond to them as if they were user messages.</local-command-caveat>'
}

const detail = {
  transcript: {
    id: ID,
    local_id: 'sess_demo_0001',
    visibility: 'public',
    title: TITLE_DIAGNOSTIC ? null : 'Verify sticky model attribution on the mounted Village transcript route',
    description: 'Host-integration boot fixture.',
    project_name: 'observed-model-contract',
  },
  owner: { id: 'owner-demo' },
  enriched_shares: [],
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

// Attestations created via the mock POST below, held in-memory for the life of the process —
// enough for AttestButton's create-then-refetch flow to round-trip against a real REST shape.
const attestations = []

const server = createServer((req, res) => {
  const url = new URL(req.url, `http://localhost:${PORT}`)
  const p = url.pathname.replace(/^\/api\/v1/, '')
  if (req.method === 'OPTIONS') return send(res, 204, null)
  console.log(`${req.method} ${url.pathname}`)
  if (req.method === 'GET' && p === `/transcripts/${ID}`) return send(res, 200, detail)
  if (req.method === 'GET' && p === `/transcripts/${ID}/content`) return send(res, 200, content)
  if (req.method === 'GET' && p === `/transcripts/${ID}/annotations`) return send(res, 200, { annotations: [] })
  if (req.method === 'GET' && p === `/transcripts/${ID}/attestations`) return send(res, 200, attestations)
  if (req.method === 'POST' && p === `/transcripts/${ID}/attestations`) {
    let body = ''
    req.on('data', (chunk) => { body += chunk })
    req.on('end', () => {
      const { org_login, attestation_type, note } = JSON.parse(body || '{}')
      const created = {
        id: `attn-${attestations.length + 1}`,
        transcript_id: ID,
        org_login,
        attestation_type,
        note: note ?? null,
        created_at: new Date().toISOString(),
        attester_username: user.github_username,
        attester_avatar: user.avatar_url,
      }
      attestations.push(created)
      send(res, 201, created)
    })
    return
  }
  if (req.method === 'GET' && p === '/auth/me') return send(res, 200, user)
  if (req.method === 'GET' && p === '/auth/orgs') return send(res, 200, [{ org_id: 1, org_login: 'anthropic-labs', avatar_url: null, visible: true, fetched_at: '2026-06-28T12:00:00Z' }])
  if (req.method === 'GET' && p === '/groups') return send(res, 200, groupsList)
  if (req.method === 'GET' && p === `/groups/${groupId}`) return send(res, 200, groupDetail)
  if (req.method === 'GET' && p === `/groups/${groupId}/my-shares`) return send(res, 200, [])
  if (req.method === 'GET' && p === `/groups/${groupId}/pending`) return send(res, 200, [])
  if (req.method === 'GET' && p === `/groups/${groupId}/transcripts`) return send(res, 200, { transcripts: groupDetail.transcripts })
  if (req.method === 'GET' && p === `/groups/${groupId}/settings`) return send(res, 200, groupDetail)
  // /me (and anything else) → 401: an unauthenticated viewer still renders the transcript (auth only
  // gates the per-turn ACTIONS, not the render).
  return send(res, 401, { error: 'unauthenticated (mock)' })
})

server.listen(PORT, () => console.log(`mock-rest: serving transcript "${ID}" on http://localhost:${PORT}/api/v1 (point NEXT_PUBLIC_API_URL here)`))
