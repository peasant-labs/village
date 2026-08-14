/* REST mock for the mounted `/` pagination visual evidence.

   The default Explore mock (mock-rest-explore.mjs) serves a single browse page,
   so the numbered pager never appears. This mock serves a larger, deterministic
   dataset (enough rows for several 24-item pages) whose row titles name their
   page, so a screenshot proves which page's rows are displayed. It also honors
   an optional PAGINATION_DELAY_MS on the /transcripts route so the "loading page
   N; showing page M" busy state can be captured in a real browser.

   Routes mirror the production discovery surface (offset page/limit, total from
   one filtered set) and the auth/profile probes LayoutShell issues so the real
   page mounts signed-in (no username-claim gate).

   Usage:
     MOCK_REST_PORT=8790 PAGINATION_DELAY_MS=650 node scripts/visual/mock-rest-pagination.mjs
*/
import { createServer } from 'node:http'

const PORT = Number(process.env.MOCK_REST_PORT || 8790)
const DELAY_MS = Math.max(0, Number(process.env.PAGINATION_DELAY_MS || '0'))
const TOTAL_ROWS = Math.max(1, Number(process.env.PAGINATION_TOTAL_ROWS || '60'))

// A valid published Schema harness menu, so the Explore adapter accepts every
// row's model_provider (an unknown provider fails closed by design).
const HARNESSES = ['claude-code', 'gemini-cli', 'codex', 'opencode', 'cursor', 'strike']
const MODEL_NAMES = ['Claude Opus 4.5', 'Gemini 2.5 Pro', 'Codex', 'OpenCode', 'Cursor', 'Strike']

const owner = { github_username: 'octo-cat', display_name: 'Octo Cat', avatar_url: null }

const user = {
  id: 'user-pagination',
  github_id: 424242,
  github_username: 'octo-cat',
  display_name: 'Octo Cat',
  avatar_url: null,
  created_at: '2026-06-01T12:00:00Z',
  updated_at: '2026-06-01T12:00:00Z',
  is_discoverable: true,
  username_chosen: true,
  provider_username: 'octo-cat',
}

// Deterministic rows. Each row's page (1-based, 24/page) is baked into its title
// so a capture visibly proves the displayed page.
const PAGE_SIZE_FOR_TITLE = 24
// A strictly decreasing publish time (and matching decreasing turn/token counts)
// so every supported sort yields the same insertion order. That keeps each row at
// a fixed position, so its baked "Page N" title always matches the page it is
// actually served on — the property the visual gate relies on.
const BASE_EPOCH_MS = Date.UTC(2026, 5, 1, 12, 0, 0)
const rows = Array.from({ length: TOTAL_ROWS }, (_, index) => {
  const pageOfRow = Math.floor(index / PAGE_SIZE_FOR_TITLE) + 1
  const seq = String(index + 1).padStart(2, '0')
  const harnessIndex = index % HARNESSES.length
  const id = `pg${pageOfRow}-row${seq}`
  const stamp = new Date(BASE_EPOCH_MS - index * 60_000).toISOString()
  return {
    transcript: {
      id,
      owner_id: 'owner-octo-cat',
      local_id: id,
      title: `Page ${pageOfRow} · Session ${seq}`,
      description: `pagination fixture row ${seq}`,
      visibility: 'public',
      model_provider: HARNESSES[harnessIndex],
      model_name: MODEL_NAMES[harnessIndex],
      harness_version: '2026.06',
      session_start: stamp,
      session_end: stamp,
      turn_count: 200 - index,
      token_count: 500000 - index * 1000,
      blob_key: `blob-${id}`,
      blob_size_bytes: 0,
      schema_version: '0.1.0',
      published_at: stamp,
      updated_at: stamp,
      parent_session_id: null,
      ingested_at: stamp,
      source_file_path: null,
      source_format: 'json',
      git_branch: 'main',
      git_remote: null,
      git_worktree: null,
      project_hash: null,
      project_path: null,
      project_name: `project-${pageOfRow}`,
      tool_call_count: 10,
      subagent_count: 0,
      duration_ms: 3_600_000,
      tokens_in: null,
      tokens_out: null,
      subagents: null,
      diagnostics_warnings: null,
      diagnostics_partial: null,
    },
    tags: [{ id: HARNESSES[harnessIndex], name: HARNESSES[harnessIndex] }],
    owner,
  }
})

const send = (res, code, body) => {
  res.writeHead(code, {
    'content-type': 'application/json',
    'access-control-allow-origin': '*',
    'access-control-allow-headers': '*',
    'access-control-allow-methods': 'GET,POST,DELETE,OPTIONS',
  })
  res.end(body == null ? '' : JSON.stringify(body))
}

const sortRows = (list, sort) =>
  list.slice().sort((a, b) => {
    if (sort === 'turns') return (b.transcript.turn_count || 0) - (a.transcript.turn_count || 0)
    if (sort === 'tokens') return (b.transcript.token_count || 0) - (a.transcript.token_count || 0)
    return String(b.transcript.published_at).localeCompare(String(a.transcript.published_at))
  })

const listTranscripts = (url) => {
  const sort = url.searchParams.get('sort') || 'recent'
  const page = Math.max(1, Number(url.searchParams.get('page') || '1'))
  const limit = Math.max(1, Number(url.searchParams.get('limit') || '24'))
  const sorted = sortRows(rows, sort)
  const start = (page - 1) * limit
  return { transcripts: sorted.slice(start, start + limit), total: sorted.length, page, limit }
}

const server = createServer((req, res) => {
  const url = new URL(req.url, `http://localhost:${PORT}`)
  const path = url.pathname.replace(/^\/api\/v1/, '')
  if (req.method === 'OPTIONS') return send(res, 204, null)

  if (req.method === 'GET' && path === '/transcripts') {
    const body = listTranscripts(url)
    if (DELAY_MS > 0) return void setTimeout(() => send(res, 200, body), DELAY_MS)
    return send(res, 200, body)
  }
  if (req.method === 'GET' && path === '/groups/search') return send(res, 200, { collectives: [] })
  if (req.method === 'GET' && path === '/tags/popular') return send(res, 200, [])
  if (req.method === 'GET' && path === '/groups') return send(res, 200, [])
  if (req.method === 'GET' && path === '/auth/me') return send(res, 200, user)
  if (req.method === 'GET' && path === '/auth/orgs') return send(res, 200, [])

  return send(res, 404, { error: `no mock route for ${req.method} ${url.pathname}` })
})

server.listen(PORT, () => {
  console.log(`mock-rest-pagination: serving ${TOTAL_ROWS} rows (delay ${DELAY_MS}ms) on http://localhost:${PORT}/api/v1`)
})
