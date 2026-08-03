/* Minimal REST mock for the host-integration BOOT check (boot-village.mjs).

   The capture harness (village-shoot.mjs) drives a backend-free dev FIXTURE route for determinism, so
   it bypasses village's REAL data path: the `/transcripts/[id]` route → React Query (`useTranscript` +
   `useTranscriptContent`) → REST `GET /transcripts/{id}` + `/transcripts/{id}/content` → the
   SessionDetailV2 adapter → the shared `<SessionDetail>` composer. This mock serves exactly those REST
   endpoints (a representative session) so `boot-village.mjs` can exercise that REAL route+adapter+
   React-Query path with no Postgres/MinIO/auth stack — village's analog of peasant's `--mock-data-store`.

   Point the village frontend at it via `NEXT_PUBLIC_API_URL` (the only env the app reads for its API
   base), then run boot-village against the real route:
     MOCK_REST_PORT=8788 node scripts/visual/mock-rest.mjs &
     NEXT_PUBLIC_API_URL=http://localhost:8788/api/v1 pnpm dev &
     CHROME_PATH=... VILLAGE_TRANSCRIPT=demo node scripts/visual/boot-village.mjs

   env: MOCK_REST_PORT (default 8788), MOCK_TRANSCRIPT_ID (default `demo`). Runs until killed. */
import { createServer } from 'node:http'

const PORT = Number(process.env.MOCK_REST_PORT || 8788)
const ID = process.env.MOCK_TRANSCRIPT_ID || 'demo'

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
      model_name: 'claude-opus-4-7',
      harness_version: '1.0.0',
      session_start: '2026-06-17T09:12:00Z',
      session_end: '2026-06-17T09:20:00Z',
      turn_count: 3,
      token_count: 4200,
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
      tool_call_count: 1,
      subagent_count: 0,
      duration_ms: 480000,
      tokens_in: 2600,
      tokens_out: 1600,
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

// A representative SessionDetailPayload — enough turns + a tool call for the composer to render real
// content (this proves the REST → adapter → <SessionDetail> path; it is NOT the canonical capture
// fixture). The viewer's only structural requirement is a `turns` array.
const content = {
  id: 'sess_demo_0001',
  harness: 'claude-code',
  startTime: ts(0),
  endTime: ts(8),
  durationMins: 8,
  totalTokens: 4200,
  tokensIn: 2600,
  tokensOut: 1600,
  turnCount: 3,
  toolCallCount: 1,
  project: 'village',
  model: 'claude-opus-4-7',
  workingDirectory: '/workspace/village',
  outcome: 'resolved',
  turns: [
    { index: 0, role: 'user', content: 'Review the Village transcript label popover before updating its interaction.', timestamp: ts(0), depth: 0, tokensIn: 280, tokensOut: 0 },
    {
      index: 1, role: 'assistant', depth: 0, timestamp: ts(1), tokensIn: 1840, tokensOut: 920,
      content: 'Reading **TurnLabelPopover.tsx** to preserve its save and cancel behavior.',
      toolCalls: [{
        id: 't1a', name: 'Read', toolKind: 'read',
        filePath: 'frontend/src/components/transcript/TurnLabelPopover.tsx',
        arguments: JSON.stringify({ file_path: 'frontend/src/components/transcript/TurnLabelPopover.tsx', offset: 1, limit: 40 }),
        result: JSON.stringify('export function TurnLabelPopover({ onSave, onCancel }: TurnLabelPopoverProps) {\n  return <form aria-label="label turn">…</form>\n}'),
      }],
    },
    { index: 2, role: 'assistant', depth: 0, timestamp: ts(2), tokensIn: 980, tokensOut: 720, stopReason: 'end_turn', content: 'The popover behavior is preserved and the Village frontend still typechecks.' },
  ],
}

const detail = {
  transcript: {
    id: ID,
    local_id: 'sess_demo_0001',
    visibility: 'public',
    title: 'Review the transcript label popover',
    description: 'Host-integration boot fixture.',
    project_name: 'village',
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
