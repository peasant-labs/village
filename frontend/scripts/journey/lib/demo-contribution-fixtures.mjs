/* Opt-in, disposable contribution data for a hands-on local preview.
 * Normal journey scenarios never claim these routes through this handler.
 */
import {
  zVillageBatchReviewRequest, zVillageBatchReviewResponse,
  zVillageBatchShareRequest, zVillageBatchShareResponse,
  zVillageContributableResponse, zVillageContributableTranscript,
  zVillageGroupedContributableResponse, zVillagePendingShare,
  zVillageSessionListPayload, zVillageTranscriptMetadataResponse,
} from '@peasant-labs/schema'
import { makeTranscriptFixture } from '../../../src/test/transcriptRowFixture.ts'
import { profileOwner } from '../../../src/test/publicOwnerFixture.ts'
import { HOME_ROWS } from './home-fixtures.mjs'
import { fixtureUUID, JOURNEY_COLLECTIVES, JOURNEY_PENDING, RETAINED_CONTRIBUTIONS } from './collective-fixtures.mjs'
import { FROZEN_EPOCH_MS } from './determinism-constants.mjs'

const enabled = () => process.env.MOCK_DEMO_ATTACHMENTS === '1'
const at = (minutes) => new Date(FROZEN_EPOCH_MS - minutes * 60_000).toISOString()
const viewer = profileOwner({ id: fixtureUUID('user-demo'), github_username: 'alice-dev' })
const ownerID = JOURNEY_COLLECTIVES.owner.id
const memberID = JOURNEY_COLLECTIVES.member.id
const live = (status) => status === 'approved' || status === 'pending'
const ownRows = [
  ...HOME_ROWS.map((row) => ({
    id: fixtureUUID(row.id), alias: row.id, localID: fixtureUUID(`local-${row.id}`),
    title: row.title, project: row.project, branch: row.branch,
    visibility: row.sharedWith.length ? 'shared' : 'private', minutes: row.minutesAgo,
    sharedWith: row.sharedWith,
  })),
  ...RETAINED_CONTRIBUTIONS.map((row) => ({
    id: row.id, alias: row.id, localID: fixtureUUID(row.localID), parentSessionID: row.parentSessionID ? fixtureUUID(row.parentSessionID) : null,
    title: row.title, project: ['ingest-api', 'a'.repeat(64)], branch: 'main',
    visibility: 'private', minutes: 30, sharedWith: [JOURNEY_COLLECTIVES.owner.name],
  })),
]
const pendingSamples = JOURNEY_PENDING.map((row) => zVillagePendingShare.parse({
  transcript_id: row.transcript_id, title: row.title, model_provider: row.provider,
  owner: profileOwner({ id: fixtureUUID(row.owner), github_username: row.login }),
  local_id: row.transcript_id, parent_session_id: null,
  parent_transcript_id: null, project_hash: 'b'.repeat(64), project_name: row.project,
  branch: row.branch, shared_at: at(row.minutes),
}))
let shared
let pending
export function resetDemoContributions() {
  shared = new Map([ownerID, memberID].map((id) => [id, new Map(ownRows
    .filter((row) => row.sharedWith.includes(id === ownerID ? JOURNEY_COLLECTIVES.owner.name : JOURNEY_COLLECTIVES.member.name))
    .map((row) => [row.id, RETAINED_CONTRIBUTIONS.find((item) => item.id === row.id)?.status ?? 'approved']))]))
  pending = new Map(pendingSamples.map((row) => [row.transcript_id, row]))
}
resetDemoContributions()

function contribution(row, groupID) {
  return zVillageContributableTranscript.parse({
    id: row.id, local_id: row.localID, title: row.title, visibility: row.visibility,
    project_hash: row.project[1], project_display_name: row.project[0], project_name_source: 'consented',
    git_branch: row.branch, parent_session_id: row.parentSessionID ?? null,
    session_origin: 'user', model_provider: 'claude-code', published_at: at(row.minutes),
    already_shared: live(shared.get(groupID)?.get(row.id)),
  })
}
function session(row) {
  return makeTranscriptFixture({
    id: row.id, local_id: row.localID, title: row.title, visibility: row.visibility,
    project_hash: row.project[1], project_name: row.project[0], project_display_name: row.project[0],
    git_branch: row.branch, parent_session_id: row.parentSessionID ?? null, published_at: at(row.minutes),
  })
}
function grouped(url, items) {
  const page = Number(url.searchParams.get('page') || 1)
  const limit = Number(url.searchParams.get('limit') || 20)
  return zVillageSessionListPayload.parse({
    items: items.slice((page - 1) * limit, page * limit), page, limit,
    totalItems: items.length, ordinarySessionTotal: items.length, helperThreadTotal: 0,
  })
}
const preview = (id) => {
  const own = ownRows.find((row) => row.id === id || row.alias === id)
  if (own) return { transcript: session(own), owner: viewer }
  const queued = pendingSamples.find((row) => row.transcript_id === id)
  if (!queued) return null
  return {
    transcript: makeTranscriptFixture({ id, title: queued.title, local_id: queued.local_id,
      project_hash: queued.project_hash, project_name: queued.project_name,
      project_display_name: queued.project_name, git_branch: queued.branch, model_provider: queued.model_provider }),
    owner: queued.owner,
  }
}
/** Existing synthetic transcript content, served by the composed mock's proxy. */
export function demoContributionContentPath(path) {
  const match = path.match(/^\/transcripts\/([^/]+)\/content$/)
  return enabled() && match && preview(match[1]) ? '/api/v1/transcripts/demo/content' : null
}
const answer = (res, status, body) => {
  res.writeHead(status, { 'content-type': 'application/json', 'access-control-allow-origin': '*' })
  res.end(JSON.stringify(body))
}
const readJSON = async (req) => {
  let body = ''
  for await (const chunk of req) body += chunk
  return JSON.parse(body || '{}')
}

/** Same flat/grouped reads and explicit batch mutations as the mounted app. */
export async function handleDemoContributionRequest(req, res) {
  if (!enabled()) return false
  const url = new URL(req.url, 'http://fixture')
  const path = url.pathname.replace(/^\/api\/v1/, '')
  const metadata = path.match(/^\/transcripts\/([^/]+)(\/(?:annotations|pull-requests|collectives))?$/)
  if (metadata && req.method === 'GET') {
    const row = preview(metadata[1])
    if (!row) return false
    const suffix = metadata[2]
    if (suffix === '/annotations') answer(res, 200, [])
    else if (suffix === '/pull-requests') answer(res, 200, { pull_requests: [], unverified_pull_requests: [] })
    else if (suffix === '/collectives') answer(res, 200, { collectives: [] })
    else answer(res, 200, zVillageTranscriptMetadataResponse.parse({ ...row, tags: [], shares: [], enriched_shares: [], attestations: [], owner_orgs: [] }))
    return true
  }
  const match = path.match(/^\/collectives\/([^/]+)\/(contributable|pending|shares)$/)
  if (!match) return false
  const [, groupID, action] = match
  if (![ownerID, memberID].includes(groupID)) {
    answer(res, 403, { error: 'membership required for this preview collective' }); return true
  }
  if (action === 'contributable' && req.method === 'GET') {
    const rows = ownRows.map((row) => contribution(row, groupID))
    if (url.searchParams.get('view') === 'grouped') {
      const items = rows.map((row, index) => ({ kind: 'transcript', transcript: { owner: viewer, session: session(ownRows[index]), contributable: row } }))
      answer(res, 200, zVillageGroupedContributableResponse.parse({ group_id: groupID, transcriptList: grouped(url, items) }))
    } else answer(res, 200, zVillageContributableResponse.parse({ group_id: groupID, transcripts: rows }))
    return true
  }
  if (action === 'pending' && req.method === 'GET') {
    if (groupID !== ownerID) { answer(res, 403, { error: 'owner access required' }); return true }
    const rows = [...pending.values()]
    const items = rows.map((row) => ({ kind: 'transcript', transcript: { owner: row.owner, session: preview(row.transcript_id)?.transcript ?? session(ownRows.find((item) => item.id === row.transcript_id)), pending: row } }))
    answer(res, 200, url.searchParams.get('view') === 'grouped' ? grouped(url, items) : rows)
    return true
  }
  if (action !== 'shares' || !['POST', 'PATCH'].includes(req.method)) return false
  try {
    if (req.method === 'POST') {
      const body = zVillageBatchShareRequest.parse(await readJSON(req))
      const rows = body.transcript_ids.map((id) => ownRows.find((row) => row.id === id && row.project[1] === body.project_hash))
      if (rows.some((row) => !row)) { answer(res, 400, { error: 'select your own transcripts from one project' }); return true }
      if (rows.some((row) => row.visibility === 'private') && !body.visibility_confirmed) { answer(res, 409, { error: 'confirm visibility before contributing private transcripts' }); return true }
      const already_shared = body.transcript_ids.filter((id) => live(shared.get(groupID).get(id)))
      const status = groupID === ownerID ? 'pending' : 'approved'
      const added = rows.filter((row) => !already_shared.includes(row.id))
      for (const row of added) {
        shared.get(groupID).set(row.id, status)
        if (status === 'pending') pending.set(row.id, zVillagePendingShare.parse({
          transcript_id: row.id, title: row.title, model_provider: 'claude-code', owner: viewer,
          local_id: row.localID, parent_session_id: row.parentSessionID ?? null, parent_transcript_id: null,
          project_hash: row.project[1], project_name: row.project[0], branch: row.branch, shared_at: at(0),
        }))
      }
      answer(res, 200, zVillageBatchShareResponse.parse({ project_hash: body.project_hash, shared: added.map((row) => ({ transcript_id: row.id, status })), already_shared }))
    } else {
      if (groupID !== ownerID) { answer(res, 403, { error: 'owner access required' }); return true }
      const body = zVillageBatchReviewRequest.parse(await readJSON(req))
      const decided = body.transcript_ids.filter((id) => pending.has(id))
      const already_decided = body.transcript_ids.filter((id) => !pending.has(id))
      for (const id of decided) { pending.delete(id); if (shared.get(groupID).has(id)) shared.get(groupID).set(id, body.status) }
      answer(res, 200, zVillageBatchReviewResponse.parse({ decided, already_decided }))
    }
  } catch { answer(res, 400, { error: 'invalid preview contribution request' }) }
  return true
}
