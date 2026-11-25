/* The positioned transcripts both mocks serve: the transcript page's subject, the snapped deep
   link's subject and the review queue's first submission, each with its metadata, its stored copy and the five positioned reads. Every
   positioned body comes from the one shared builder (positioned-bodies.mjs), so a body Schema's
   parsers refuse stops the mock before it listens. */
import { readFileSync } from 'node:fs'
import { parse } from 'yaml'
import { TranscriptSegmentPartitionKind, zVillageTranscriptMetadataResponse } from '@peasant-labs/schema'
import { makeTranscriptFixture } from '../../../src/test/transcriptRowFixture.ts'
import { anonymousOwner, profileOwner } from '../../../src/test/publicOwnerFixture.ts'
import { buildPositionedBodies, PositionedRoute } from './positioned-bodies.mjs'

const FIXTURE = new URL('../testdata/positioned-transcript.yaml', import.meta.url)
export const POSITIONED = parse(readFileSync(FIXTURE, 'utf8'), { strict: true })

export { HttpMethod } from '../../visual/httpMethod.mjs'

/** The world a positioned request is answered in. Closed. */
export const PositionedWorld = Object.freeze({
  Served: 'served',
  Revoked: 'revoked',
  Withheld: 'withheld',
})

/** The fixture's generated turns: prompts and replies in turn, each marked with its step. */
function turnsOf(fixture) {
  const count = fixture.rowsPerSegment * fixture.segments
  return Array.from({ length: count }, (_, position) => {
    const step = Math.floor(position / 2)
    const user = position % 2 === 0
    const pool = user ? fixture.prompts : fixture.replies
    return {
      role: user ? 'user' : 'assistant',
      content: `${pool[step % pool.length]} [step ${step + 1}]`,
      sourceEntryRef: `positioned-entry-${position}`,
    }
  })
}

/** The positioned spec for one fixture, with its session fields kept or withheld. */
export function positionedSpec(fixture, { withheld = false } = {}) {
  return {
    transcriptId: fixture.transcriptId,
    rowsPerSegment: fixture.rowsPerSegment,
    turns: turnsOf(fixture),
    firstRecordedIndex: fixture.firstRecordedIndex ?? 0,
    numberAbsent: fixture.numberAbsent ?? [],
    history: fixture.history ?? [],
    session: withheld ? {} : fixture.session,
  }
}

function metadataOf(fixture, bodies, { withheld }) {
  return zVillageTranscriptMetadataResponse.parse({
    transcript: makeTranscriptFixture({
      id: fixture.transcriptId, local_id: `sess_${fixture.transcriptId.replaceAll("-", "")}`, title: fixture.title, visibility: 'public',
      model_name: 'claude-opus-4-5', turn_count: bodies.context.totalTurns, displayed_turn_count: bodies.context.totalTurns,
      ...(withheld
        ? { project_hash: null, project_name: null, project_display_name: null, project_name_source: null, project_remote_label: null, git_branch: null, git_remote: null }
        : { project_name: fixture.session.project, project_display_name: fixture.session.project, git_branch: fixture.session.gitBranch ?? null }),
      content_hash: 'c'.repeat(64),
    }),
    owner: withheld ? anonymousOwner : profileOwner({ id: fixture.ownerId, github_username: fixture.owner }),
    tags: [], shares: [], enriched_shares: [], attestations: [], owner_orgs: [],
  })
}

function served(fixture, withheld) {
  const bodies = buildPositionedBodies(positionedSpec(fixture, { withheld }))
  return { fixture, bodies, metadata: metadataOf(fixture, bodies, { withheld }) }
}

// Built once at import: a refused body throws here, before the mock listens.
const transcripts = {
  [PositionedWorld.Served]: [served(POSITIONED.main, false), served(POSITIONED.snapped, false), served(POSITIONED.preview, false)],
  [PositionedWorld.Withheld]: [served(POSITIONED.main, true), served(POSITIONED.snapped, true), served(POSITIONED.preview, true)],
}

const answer = (res, status, body, headers = {}) => {
  res.writeHead(status, {
    'content-type': 'application/json',
    'access-control-allow-origin': '*',
    'access-control-allow-headers': '*',
    'access-control-allow-methods': 'GET,POST,PATCH,DELETE,OPTIONS',
    ...headers,
  })
  res.end(body === undefined ? '' : JSON.stringify(body))
}

const readBody = async (req) => {
  let text = ''
  for await (const chunk of req) text += chunk
  return text
}

/** The partition a positioned read names in its query. */
function partitionOf(url) {
  const kind = url.searchParams.get('partition') ?? TranscriptSegmentPartitionKind.Main
  return kind === TranscriptSegmentPartitionKind.History ? { kind, sectionIndex: Number(url.searchParams.get('section')) } : { kind }
}

const ROUTE = /^\/transcripts\/([^/]+)(\/(?:content|annotations|pulls|collectives|header|window|segments(?:\/locate|\/\d+)?))?$/

/**
 * Answer the advertisement and the positioned transcripts' reads in one world. Declines (returns
 * false) every other request, so the composed mock's own routes answer it.
 * @param {import('node:http').IncomingMessage} req
 * @param {import('node:http').ServerResponse} res
 * @param {string} world  a PositionedWorld member
 */
export async function handlePositionedRequest(req, res, world) {
  const url = new URL(req.url, 'http://fixture')
  const path = url.pathname.replace(/^\/api\/v1/, '')
  if (req.method === HttpMethod.Get && path === '/schema/version') {
    answer(res, 200, POSITIONED.advertisement)
    return true
  }
  const match = path.match(ROUTE)
  if (!match) return false
  const entry = transcripts[world === PositionedWorld.Withheld ? world : PositionedWorld.Served].find((item) => item.fixture.transcriptId === match[1])
  if (!entry) return false
  // A revoked transcript answers every read as one the reader may not open.
  if (world === PositionedWorld.Revoked) {
    answer(res, 404, { error: 'transcript not found' })
    return true
  }
  const { bodies, metadata } = entry
  const suffix = match[2] ?? ''
  // A request a real server would refuse is refused here too, so the page cannot stop sending
  // what the positioned routes require and still pass against this mock.
  const refused = (route, body) => {
    const refusal = bodies.requestRefusal(route, url.searchParams, body)
    if (refusal) answer(res, refusal.status, { error: refusal.error })
    return refusal !== undefined
  }
  if (req.method === HttpMethod.Post && suffix === '/segments/locate') {
    const request = JSON.parse((await readBody(req)) || '{}')
    if (refused(PositionedRoute.Locate, request)) return true
    const found = bodies.locate(request.reference)
    if (found) answer(res, 200, found)
    else answer(res, 404, { error: 'no entry matches the reference' })
    return true
  }
  if (req.method !== HttpMethod.Get) return false
  const partition = partitionOf(url)
  const missing = () => answer(res, 404, { error: 'no such positioned read' })
  switch (suffix) {
    case '': answer(res, 200, metadata); return true
    case '/content': answer(res, 200, bodies.content); return true
    case '/annotations': answer(res, 200, { annotations: [] }); return true
    case '/pulls': answer(res, 200, { pull_requests: [], unverified_pull_requests: [] }); return true
    case '/collectives': answer(res, 200, { collectives: [] }); return true
    case '/header':
      if (!refused(PositionedRoute.Header)) answer(res, 200, bodies.header)
      return true
    case '/segments': {
      if (refused(PositionedRoute.Directory)) return true
      const body = bodies.directoryFor(partition, Number(url.searchParams.get('page') ?? '1'))
      if (body) answer(res, 200, body)
      else missing()
      return true
    }
    case '/window': {
      if (refused(PositionedRoute.Window)) return true
      const anchor = url.searchParams.get('anchor')
      const body = bodies.windowFor(partition, anchor === null ? undefined : Number(anchor))
      if (body) answer(res, 200, body)
      else missing()
      return true
    }
    default: {
      if (refused(PositionedRoute.Segment)) return true
      const body = bodies.segmentFor(partition, Number(suffix.slice('/segments/'.length)))
      if (body) answer(res, 200, body)
      else missing()
      return true
    }
  }
}
