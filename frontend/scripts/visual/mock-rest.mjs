/* Minimal REST mock for the host-integration BOOT check (boot-village.mjs).

   The capture harness (village-shoot.mjs) drives a backend-free dev FIXTURE route for determinism, so
   it bypasses village's REAL data path: the `/transcripts/[id]` route → React Query (`useTranscript` +
   `useTranscriptContent`) → REST `GET /transcripts/{id}` + `/transcripts/{id}/content` → the
   SessionDetailV2 adapter → Fairtrade's canonical `<TranscriptViewer>` with fairtrade's own graph
   engine. This mock serves exactly those REST
   endpoints (a representative session) so `boot-village.mjs` can exercise that REAL route+adapter+
   React-Query path with no Postgres/MinIO/auth stack — village's analog of peasant's `--mock-data-store`.

   Point the village frontend at it via `NEXT_PUBLIC_API_URL` (the only env the app reads for its API
   base), then run boot-village against the real route:
     MOCK_REST_PORT=8788 node scripts/visual/mock-rest.mjs &
     NEXT_PUBLIC_API_URL=http://localhost:8788/api/v1 pnpm dev &
     CHROME_PATH=... VILLAGE_TRANSCRIPT=demo node scripts/visual/boot-village.mjs

   env: MOCK_REST_PORT (default 8788), MOCK_TRANSCRIPT_ID (default `demo`), MOCK_TITLE_HERO_DIAGNOSTIC
   (default off; set `1` for the title-hero + breadcrumb evidence case — see below), MOCK_ROLE
   (`owner` | `member`, default `owner`). Runs until killed.

   MOCK_ROLE decides what the signed-in viewer is IN THE COLLECTIVE, so a capture can show either
   header-action path. It rewrites every role-derived field of the group payload together — the
   viewer's `your_role`, the collective's own `role` mirror, and the viewer's row in `members` — so
   the three can never disagree the way a single hand-edited field would:
     MOCK_ROLE=owner  MOCK_REST_PORT=8788 node scripts/visual/mock-rest.mjs &   # default
     MOCK_ROLE=member MOCK_REST_PORT=8788 node scripts/visual/mock-rest.mjs &
   An unknown value is refused at startup rather than silently captured as the default. */
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

// The viewer's role in the collective. Closed set: an unrecognised value is a
// typo that would otherwise be captured as a silently different surface.
const VIEWER_ROLES = ['owner', 'member']
const viewerRole = process.env.MOCK_ROLE || 'owner'
if (!VIEWER_ROLES.includes(viewerRole)) {
  console.error(
    `ERROR [mock-rest.mjs] MOCK_ROLE="${viewerRole}" is not a role this mock can serve. ` +
    `Valid values are ${VIEWER_ROLES.join(' | ')} (default owner); the value decides the signed-in ` +
    `viewer's role in the collective, which decides which header contribute action renders. ` +
    `Re-run with MOCK_ROLE=owner or MOCK_ROLE=member.`,
  )
  process.exit(1)
}

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
    role: viewerRole,
    member_since: '2026-06-01T12:00:00Z',
    member_count: 12,
    transcript_count: 248,
  },
  members: [
    {
      id: 'member-1',
      role: viewerRole,
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
      project_name: 'fairtrade-design-system',
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
  your_role: viewerRole,
  pending_members: [],
}

const groupsList = [groupDetail.group]

// One transcript the viewer (alice-dev) owns but has not yet shared with the
// demo collective, so a `GET /transcripts?owner=alice-dev` (the contribute
// page's own shareable-list query) has something to render for the
// `manage-contribute` shot -- otherwise the panel would only ever capture
// its empty state.
const shareableTranscriptListResponse = {
  transcripts: [
    {
      transcript: {
        id: 'gt-3',
        owner_id: 'member-1',
        local_id: 'sess_demo_0003',
        title: 'Draft the contribute route shell',
        description: null,
        visibility: 'private',
        model_provider: 'claude-code',
        model_name: 'claude-fable-5',
        harness_version: '1.0.0',
        session_start: '2026-06-19T09:12:00Z',
        session_end: '2026-06-19T09:18:00Z',
        turn_count: 3,
        token_count: 800,
        blob_size_bytes: 2048,
        schema_version: '0.13.0',
        published_at: '2026-06-19T09:20:00Z',
        updated_at: '2026-06-19T09:20:00Z',
        parent_session_id: null,
        ingested_at: '2026-06-19T09:21:00Z',
        source_format: 'json',
        git_branch: 'main',
        git_remote: null,
        project_hash: 'a'.repeat(64),
        project_name: 'village',
        project_display_name: 'village',
        project_name_source: 'consented',
        project_remote_label: '',
        tool_call_count: 0,
        subagent_count: 0,
        duration_ms: 360000,
        tokens_in: 500,
        tokens_out: 300,
        subagents: [],
        diagnostics_warnings: [],
        diagnostics_partial: false,
        session_origin: 'user',
      },
      tags: [],
      owner: user,
      shares: [],
    },
  ],
  total: 1,
  agent_total: 0,
  page: 1,
  limit: 100,
}

// The tree-based contribute page's own corpus (village#66): two projects (one
// with two branches, so the two-column @container layout and the tree's
// grouping both have something real to show), plus one orphaned row so the
// synthetic "orphaned transcripts" node renders in the capture too.
const contributableResponse = {
  group_id: groupId,
  transcripts: [
    {
      id: 'ct-main-1',
      local_id: 'sess_ct_0001',
      title: 'fix the redaction rule ordering',
      visibility: 'public',
      project_hash: 'b'.repeat(64),
      project_display_name: 'peasant',
      project_name_source: 'consented',
      git_branch: 'main',
      parent_session_id: null,
      session_origin: 'user',
      model_provider: 'claude-code',
      published_at: '2026-06-20T09:00:00Z',
      already_shared: false,
    },
    {
      id: 'ct-feature-1',
      local_id: 'sess_ct_0002',
      title: 'draft the contribute tree page',
      visibility: 'private',
      project_hash: 'b'.repeat(64),
      project_display_name: 'peasant',
      project_name_source: 'consented',
      git_branch: 'feature/contribute-tree',
      parent_session_id: null,
      session_origin: 'user',
      model_provider: 'gemini-cli',
      published_at: '2026-06-21T09:00:00Z',
      already_shared: false,
    },
    {
      id: 'ct-orphan-1',
      local_id: 'sess_ct_0003',
      title: 'a session whose parent was never fetched',
      visibility: 'public',
      project_hash: 'b'.repeat(64),
      project_display_name: 'peasant',
      project_name_source: 'consented',
      git_branch: 'main',
      parent_session_id: 'sess_ct_missing',
      session_origin: 'user',
      model_provider: 'codex',
      published_at: '2026-06-22T09:00:00Z',
      already_shared: false,
    },
    {
      id: 'ct-shared-1',
      local_id: 'sess_ct_0004',
      title: 'already shared with this collective',
      visibility: 'shared',
      project_hash: 'c'.repeat(64),
      project_display_name: 'village',
      project_name_source: 'consented',
      git_branch: 'main',
      parent_session_id: null,
      session_origin: 'user',
      model_provider: 'claude-code',
      published_at: '2026-06-18T09:00:00Z',
      already_shared: true,
    },
  ],
}

const ts = (min) => new Date(Date.parse('2026-06-17T09:12:00Z') + min * 60_000).toISOString()

// A small, realistic session detail for the contribute tree's OWN fixture ids
// (`ct-*`) -- so the `manage-contribute-preview` capture shows real turns +
// a tool call + a saved label chip in the preview column, not the harness's
// unrelated demo transcript's shimmer/"not found" state. Keyed by id so a
// future capture can distinguish rows if it needs to; every `ct-*` id
// currently answers the SAME payload (content is not part of what any tree
// case has to distinguish).
const previewTranscriptDetail = (id) => ({
  transcript: {
    id,
    local_id: `local-${id}`,
    visibility: 'private',
    title: 'draft the contribute tree page',
    description: 'A small worked example for the visual capture.',
    project_name: 'peasant',
  },
  owner: { id: 'owner-demo' },
  enriched_shares: [],
})

const previewTranscriptContent = {
  id: 'sess_ct_preview',
  harness: 'gemini-cli',
  startTime: ts(10),
  endTime: ts(16),
  durationMins: 6,
  totalTokens: 640,
  tokensIn: 400,
  tokensOut: 240,
  turnCount: 3,
  toolCallCount: 1,
  project: 'peasant',
  model: 'gemini-2.5-pro',
  workingDirectory: '/workspace/peasant',
  outcome: 'resolved',
  turns: [
    {
      index: 0,
      role: 'user',
      content: 'Draft the contribute tree page and wire the preview column.',
      timestamp: ts(10),
      depth: 0,
      tokensIn: 60,
      tokensOut: 0,
    },
    {
      index: 1,
      role: 'assistant',
      content: 'Building the project > branch > session tree, then reading the existing page shell.',
      timestamp: ts(12),
      depth: 0,
      tokensIn: 0,
      tokensOut: 90,
      toolCalls: [
        {
          id: 'tool-1',
          name: 'Read',
          arguments: '{"path":"src/app/groups/[id]/contribute/page.tsx"}',
          result: 'read 344 lines',
          durationMs: 40,
          filePath: 'src/app/groups/[id]/contribute/page.tsx',
          isError: false,
          toolKind: 'read',
        },
      ],
    },
    {
      index: 2,
      role: 'assistant',
      content: 'The tree groups sessions by project and branch, with a synthetic node for orphaned rows.',
      timestamp: ts(15),
      depth: 0,
      tokensIn: 0,
      tokensOut: 150,
    },
  ],
}

const previewAnnotations = {
  annotations: [
    {
      annotatorKind: 'human',
      annotatorName: 'alice-dev',
      createdAt: Date.parse('2026-06-21T09:05:00Z'),
      id: 'annotation-preview-1',
      isPrimary: true,
      targetEntryIndex: 1,
      targetKind: 'entry',
      typeId: 'outcome',
      typeName: 'outcome',
      value: 'good',
    },
  ],
}

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
  // The contribute tree's own fixture ids (contributableResponse, above):
  // real turns + a tool call + a saved label, so the preview column's
  // capture shows actual content instead of a loading/not-found state.
  const ctMatch = /^\/transcripts\/(ct-[a-z0-9-]+)(\/content|\/annotations)?$/.exec(p)
  if (req.method === 'GET' && ctMatch) {
    const [, ctId, suffix] = ctMatch
    if (suffix === '/content') return send(res, 200, previewTranscriptContent)
    if (suffix === '/annotations') return send(res, 200, previewAnnotations)
    return send(res, 200, previewTranscriptDetail(ctId))
  }
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
  if (req.method === 'GET' && p === `/groups/${groupId}/contributable`) return send(res, 200, contributableResponse)
  if (req.method === 'POST' && p === `/groups/${groupId}/shares`) {
    let body = ''
    req.on('data', (chunk) => { body += chunk })
    req.on('end', () => {
      const { project_hash, transcript_ids } = JSON.parse(body || '{}')
      // Artificial delay so the contribute page's progress bar has a real,
      // screenshot-able "in flight" window instead of settling before the
      // browser paints a frame -- a real backend round-trip has this window
      // too, an instant mock just doesn't reproduce it by default.
      setTimeout(() => {
        send(res, 200, {
          project_hash,
          shared: (transcript_ids || []).map((id) => ({ transcript_id: id, status: 'approved' })),
          already_shared: [],
        })
      }, 1200)
    })
    return
  }
  if (req.method === 'GET' && p === '/transcripts') return send(res, 200, shareableTranscriptListResponse)
  // /me (and anything else) → 401: an unauthenticated viewer still renders the transcript (auth only
  // gates the per-turn ACTIONS, not the render).
  return send(res, 401, { error: 'unauthenticated (mock)' })
})

server.listen(PORT, () => console.log(`mock-rest: serving transcript "${ID}" on http://localhost:${PORT}/api/v1 (point NEXT_PUBLIC_API_URL here)`))
