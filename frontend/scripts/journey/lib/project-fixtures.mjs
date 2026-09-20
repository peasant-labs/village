/* Project-page fixtures for the journey harness.
 *
 * The project page's orphan-sessions case is the one project flow a journey
 * drives: the page folds a session started by another session under its
 * genuine root, and keeps a session whose complete ancestry leaves the project
 * reachable in one collapsed display-only group.
 *
 * The rows below give that group a genuine root plus two different reasons a
 * row has no root to fold under — a named parent the project does not carry,
 * and an agent session with no parent at all (an agent row is never a genuine
 * root, so it stays an orphan rather than being promoted). The session ids are
 * `ct-*` on purpose: the composed journey mock proxies `/transcripts/<id>` to
 * the transcript-detail mock, which already serves every `ct-*` id, so opening
 * an orphan lands on the real viewer route with no second fixture set.
 *
 * `handleProjectRequest` composes into `scripts/journey/mock.mjs` the same way
 * `handleExploreRequest` does: it returns true when it served the request.
 */

const PORT = Number(process.env.JOURNEY_MOCK_PORT || 8799)

export const JOURNEY_PROJECT = {
  owner: 'alice-dev',
  hash: '4'.repeat(64),
  displayName: 'orphan-grouping',
  remoteLabel: 'github.com/alice-dev/orphan-grouping',
  /** The one genuine root: a user session with no named parent. */
  rootTranscriptID: 'ct-root-1',
  /** Both orphans, in render order: missing-parent first, parentless agent second. */
  orphanTranscriptIDs: ['ct-orphan-1', 'ct-orphan-2'],
}

export const JOURNEY_PROJECT_TOTAL =
  1 + JOURNEY_PROJECT.orphanTranscriptIDs.length

const owner = {
  id: `user-${JOURNEY_PROJECT.owner}`,
  github_id: 1,
  github_username: JOURNEY_PROJECT.owner,
  display_name: 'Alice Developer',
  avatar_url: null,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  is_discoverable: true,
  username_chosen: true,
  provider_username: JOURNEY_PROJECT.owner,
}

/** One complete wire row, with only the fields this case varies stated. */
const transcript = (overrides) => ({
  id: 'transcript-0',
  owner_id: owner.id,
  local_id: 'local-0',
  title: 'fixture session',
  description: null,
  visibility: 'public',
  model_provider: 'claude-code',
  model_name: 'claude-fable-5',
  harness_version: null,
  session_start: '2026-08-20T09:00:00Z',
  session_end: '2026-08-20T09:30:00Z',
  turn_count: 12,
  token_count: 900,
  blob_size_bytes: null,
  schema_version: '0.14.0',
  published_at: '2026-08-20T10:00:00Z',
  updated_at: '2026-08-20T10:00:00Z',
  parent_session_id: null,
  ingested_at: null,
  source_format: null,
  git_branch: null,
  git_remote: null,
  project_hash: JOURNEY_PROJECT.hash,
  project_name: JOURNEY_PROJECT.displayName,
  project_display_name: JOURNEY_PROJECT.displayName,
  project_name_source: 'consented',
  project_remote_label: JOURNEY_PROJECT.remoteLabel,
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
  ...overrides,
})

const rows = [
  transcript({
    id: JOURNEY_PROJECT.rootTranscriptID,
    local_id: 'local-root',
    title: 'Set up the project page',
  }),
  transcript({
    id: JOURNEY_PROJECT.orphanTranscriptIDs[0],
    local_id: 'local-orphan-1',
    title: 'Recover the unreadable parent chain',
    model_provider: 'codex',
    parent_session_id: 'local-absent-parent',
    session_origin: 'user',
  }),
  transcript({
    id: JOURNEY_PROJECT.orphanTranscriptIDs[1],
    local_id: 'local-orphan-2',
    title: 'Audit the orphan ancestry',
    session_origin: 'agent',
  }),
]

const projectPayload = () => ({
  project: {
    project_hash: JOURNEY_PROJECT.hash,
    project_display_name: JOURNEY_PROJECT.displayName,
    project_name_source: 'consented',
    project_remote_label: JOURNEY_PROJECT.remoteLabel,
  },
  owner,
  transcripts: rows,
  collectives: [],
})

/**
 * The page's own grouped helper read. It carries no helper groups in this case,
 * but it must still echo the page and limit the page asked for: the query
 * rejects a response whose pagination does not match its request before caching
 * it, so a fixed pair would read as a server error.
 */
const groupedPayload = (url) => ({
  items: [],
  page: Number(url.searchParams.get('page') || '1'),
  limit: Number(url.searchParams.get('limit') || '20'),
  totalItems: 0,
  ordinarySessionTotal: 0,
  helperThreadTotal: 0,
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

/** Handle one project request. Returns true when the route was served. */
export function handleProjectRequest(req, res) {
  const url = new URL(req.url, `http://localhost:${PORT}`)
  const path = url.pathname.replace(/^\/api\/v1/, '')
  if (req.method === 'OPTIONS') {
    send(res, 204, null)
    return true
  }

  if (
    req.method === 'GET' &&
    path === `/users/${JOURNEY_PROJECT.owner}/projects/${JOURNEY_PROJECT.hash}`
  ) {
    send(res, 200, projectPayload())
    return true
  }
  // The project page's grouped helper read only. The plain browse list keeps
  // going to the explore half, so one project-scoped `view=grouped` request is
  // not answered with the browse shape.
  if (
    req.method === 'GET' &&
    path === '/transcripts' &&
    url.searchParams.get('view') === 'grouped' &&
    url.searchParams.get('project_hash')
  ) {
    send(res, 200, groupedPayload(url))
    return true
  }

  return false
}
