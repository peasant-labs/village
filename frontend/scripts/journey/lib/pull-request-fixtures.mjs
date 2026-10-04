/* Pull request page fixtures for the journey harness.
 *
 * One pull request, acme/ingest-api #42, with two of its author's transcripts
 * tracing three of its four commits. The composed mock serves it two ways:
 *
 *   default              the author's view while it is a preview: the digest
 *                        names both transcripts, nothing is bound yet, and the
 *                        page reads each transcript and its collectives to say
 *                        who can read them.
 *   pull-request-reader  a reviewer's view once it is attached: both
 *                        transcripts are bound and titled for this reader.
 *
 * The transcript ids are not `ct-*`, so the composed mock answers their reads
 * here instead of proxying them to the transcript-detail mock.
 *
 * `handlePullRequestRequest` composes into scripts/journey/mock.mjs the same way
 * `handleProjectRequest` does: it returns true when it served the request.
 */

export const JOURNEY_PULL = {
  owner: 'acme',
  name: 'ingest-api',
  number: 42,
  title: 'Fix flaky ingest test',
  headRef: 'fix/flaky-ingest',
  /** The collectives both transcripts were published to, as the author sees them. */
  collectives: ['Acme Platform', 'Acme Company'],
  transcripts: [
    { id: '3f9c0a17-2b64-4e1d-9a53-8c0e21a4b7d2', title: 'Fix flaky ingest test' },
    { id: 'c41a9f20-6d3e-4b18-8f07-5e2a91c3d460', title: 'Guard empty turns in the digest' },
  ],
}

export const JOURNEY_PULL_PATH = `/pulls/${JOURNEY_PULL.owner}/${JOURNEY_PULL.name}/${JOURNEY_PULL.number}`

const [FIX, GUARD] = JOURNEY_PULL.transcripts.map((row) => row.id)
const AUTHOR_ID = 'a11ce000-0000-4000-8000-000000000001'

const send = (res, code, body) => {
  res.writeHead(code, {
    'content-type': 'application/json',
    'access-control-allow-origin': '*',
    'access-control-allow-headers': '*',
    'access-control-allow-methods': 'GET,POST,DELETE,OPTIONS',
  })
  res.end(body == null ? '' : JSON.stringify(body))
}

/* The digest as village serves it: session boundaries carry village's own
   "session N" label, and the page names each one by its transcript's title. */
const digest = {
  header: {
    sessionCount: 2,
    promptCount: 6,
    commitsCovered: 3,
    commitsTotal: 4,
    harness: 'claude-code',
    redactionLevel: 'standard',
    villageUrl: `https://village.peasantlabs.org${JOURNEY_PULL_PATH}`,
  },
  skills: [{ name: '/toolkit:write-plan', invocationCount: 1 }],
  items: [
    { kind: 'session', transcriptId: FIX, timestamp: '2026-09-28T10:00:00Z', text: 'session 1', promptCount: 4, commitCount: 2 },
    { kind: 'prompt', transcriptId: FIX, timestamp: '2026-09-28T10:00:10Z', text: 'the ingest test flakes on CI about 1 in 5 runs', turnIndex: 1, ordinal: 1 },
    { kind: 'prompt', transcriptId: FIX, timestamp: '2026-09-28T10:06:00Z', text: 'check the retry backoff', turnIndex: 4, ordinal: 2 },
    { kind: 'skill', transcriptId: FIX, timestamp: '2026-09-28T10:06:30Z', text: '/toolkit:write-plan', turnIndex: 5 },
    { kind: 'prompt', transcriptId: FIX, timestamp: '2026-09-28T10:14:00Z', text: 'run it 50 times locally', turnIndex: 10, ordinal: 3 },
    { kind: 'prompt', transcriptId: FIX, timestamp: '2026-09-28T10:38:00Z', text: 'ok commit it', turnIndex: 33, ordinal: 4 },
    { kind: 'commit', transcriptId: FIX, timestamp: '2026-09-28T10:40:00Z', text: '9f3c2ab41d7e08c5a6b2f9e0d3c7a1b4e5f60718', commitSha: '9f3c2ab41d7e08c5a6b2f9e0d3c7a1b4e5f60718', additions: 18, deletions: 6, filesChanged: 2 },
    { kind: 'commit', transcriptId: FIX, timestamp: '2026-09-28T10:42:00Z', text: '1b7e0d4c9a2f36e18b5d7c0a4e9f2b3d6c8a1e05', commitSha: '1b7e0d4c9a2f36e18b5d7c0a4e9f2b3d6c8a1e05', additions: 9, deletions: 2, filesChanged: 1 },
    { kind: 'session', transcriptId: GUARD, timestamp: '2026-09-28T12:00:00Z', text: 'session 2', promptCount: 2, commitCount: 1 },
    { kind: 'prompt', transcriptId: GUARD, timestamp: '2026-09-28T12:02:00Z', text: 'an empty turn breaks the digest builder', turnIndex: 1, ordinal: 5 },
    { kind: 'prompt', transcriptId: GUARD, timestamp: '2026-09-28T12:20:00Z', text: 'skip turns with no text and add a test', turnIndex: 6, ordinal: 6 },
    { kind: 'commit', transcriptId: GUARD, timestamp: '2026-09-28T12:40:00Z', text: 'c41a9f2e7b3d05c8a61f9e2d4b7c0a3e5f8d1b62', commitSha: 'c41a9f2e7b3d05c8a61f9e2d4b7c0a3e5f8d1b62', additions: 12, deletions: 1, filesChanged: 2 },
  ],
}

function attachmentResponse(scenario) {
  const reader = scenario === 'pull-request-reader'
  return {
    attachment: {
      id: '7f3c1a2b-4d5e-4f60-8a9b-0c1d2e3f4a5b',
      owner: JOURNEY_PULL.owner,
      name: JOURNEY_PULL.name,
      number: JOURNEY_PULL.number,
      title: JOURNEY_PULL.title,
      head_ref: JOURNEY_PULL.headRef,
      head_sha: '9f3c2ab41d7e08c5a6b2f9e0d3c7a1b4e5f60718',
      is_private_repository: true,
      state: reader ? 'attached' : 'preview',
      author_user_id: AUTHOR_ID,
      requested_by_github_id: null,
      comment_id: reader ? 22 : null,
      check_run_id: reader ? 11 : null,
      created_at: '2026-09-28T09:00:00Z',
      updated_at: '2026-09-28T13:00:00Z',
      confirmed_at: reader ? '2026-09-28T13:00:00Z' : null,
      detached_at: null,
    },
    digest,
    // Nothing is bound while it is a preview; once attached, the reader's list
    // carries each transcript's title because this reader can open both.
    transcripts: reader
      ? JOURNEY_PULL.transcripts.map((row, index) => ({
          transcript_id: row.id,
          position: index,
          previous_visibility: 'shared',
          title: row.title,
          session_start: index === 0 ? '2026-09-28T10:00:00Z' : '2026-09-28T12:00:00Z',
        }))
      : [],
    viewer_is_author: !reader,
  }
}

function transcriptRead(row) {
  return {
    transcript: {
      id: row.id,
      owner_id: AUTHOR_ID,
      local_id: `local-${row.id}`,
      title: row.title,
      description: null,
      visibility: 'shared',
      model_provider: 'claude-code',
      model_name: null,
      harness_version: null,
      session_start: '2026-09-28T10:00:00Z',
      session_end: null,
      turn_count: 40,
    },
    tags: [],
    shares: [],
    enriched_shares: [],
    owner: {
      id: AUTHOR_ID,
      github_id: 1,
      github_username: 'alice-dev',
      display_name: null,
      avatar_url: null,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
      is_discoverable: true,
      username_chosen: true,
      provider_username: 'alice-dev',
    },
  }
}

function collectivesRead() {
  return {
    collectives: JOURNEY_PULL.collectives.map((name, index) => ({
      id: `c011ec70-0000-4000-8000-00000000000${index + 1}`,
      name,
      description: null,
      linked_github_org: 'acme',
      shared_at: '2026-09-28T13:00:00Z',
    })),
  }
}

export function handlePullRequestRequest(req, res, scenario) {
  const url = new URL(req.url, 'http://localhost')
  const path = url.pathname.replace(/^\/api\/v1/, '')
  if (req.method !== 'GET') return false

  if (path === JOURNEY_PULL_PATH) {
    send(res, 200, attachmentResponse(scenario))
    return true
  }
  for (const row of JOURNEY_PULL.transcripts) {
    if (path === `/transcripts/${row.id}`) {
      send(res, 200, transcriptRead(row))
      return true
    }
    if (path === `/transcripts/${row.id}/collectives`) {
      send(res, 200, collectivesRead())
      return true
    }
  }
  return false
}
