/* Collective fixtures for the journey harness: the collectives list, a
 * collective seen by its owner, by a member and by someone who has not joined,
 * its settings, and the repo picker.
 *
 * The viewer is the composed mock's signed-in user (`alice-dev`, id
 * `user-demo`, from scripts/visual/mock-rest-explore.mjs). Every timestamp is
 * relative to the journey's frozen clock, so "12m ago" reads the same on every
 * run. The world is stateful for one run (a link, an unlink, a settings write
 * lands); `resetCollectiveWorld` puts it back, and the mock calls it whenever a
 * journey sets a scenario.
 *
 * `handleCollectiveRequest` composes into scripts/journey/mock.mjs the same way
 * the explore and project handlers do: it returns true when it served the
 * request, and leaves every other route (including a search that matches none
 * of these collectives) to the handlers after it.
 */
import { readFileSync } from 'node:fs'
import { parse } from 'yaml'
import { createHash } from 'node:crypto'
import { zVillageGroupedGroupDetailResponse, zVillageSessionListPayload, zVillageHelperMembersPayload } from '@peasant-labs/schema'
import { makeTranscriptFixture } from '../../../src/test/transcriptRowFixture.ts'
import { FROZEN_EPOCH_MS } from './determinism-constants.mjs'

const retainedWorld = parse(readFileSync(new URL('../testdata/collective-retained-flows.yaml', import.meta.url), 'utf8'), { strict: true })
if (Object.keys(retainedWorld).sort().join() !== 'contributions,grouped') throw new Error('invalid retained collective fixture world')
export const RETAINED_CONTRIBUTIONS = retainedWorld.contributions
export const RETAINED_GROUPED = retainedWorld.grouped
if (Object.keys(RETAINED_GROUPED).sort().join() !== 'groupID,memberID,memberScope,pageSize,purpose,title,totalItems' || RETAINED_GROUPED.pageSize !== 100 || RETAINED_GROUPED.totalItems !== 101 || RETAINED_GROUPED.purpose !== 'helper_review' || !RETAINED_GROUPED.groupID || !RETAINED_GROUPED.memberScope || !RETAINED_GROUPED.memberID || !RETAINED_GROUPED.title) throw new Error('invalid retained grouped fixture')
// Legacy mock IDs are aliases; grouped wire rows use deterministic real UUIDs.
const validated = (schema, value) => { schema.parse(value); return value }
const fixtureUUID = (id) => { const hex = createHash('sha256').update(id).digest('hex'); return `${hex.slice(0,8)}-${hex.slice(8,12)}-4${hex.slice(13,16)}-8${hex.slice(17,20)}-${hex.slice(20,32)}` }

if (!Array.isArray(RETAINED_CONTRIBUTIONS) || JSON.stringify(RETAINED_CONTRIBUTIONS.map((row) => row.name).sort()) !== JSON.stringify(['approved-private-child', 'pending-private-parent'])) throw new Error('retained collective contribution fixtures are missing or duplicated')
for (const row of RETAINED_CONTRIBUTIONS) {
  if (Object.keys(row).sort().join(',') !== ['name', 'id', 'localID', 'parentSessionID', 'title', 'status'].sort().join(',') || !['pending', 'approved'].includes(row.status) || !row.id || !row.localID || !row.title || !(row.parentSessionID === null || typeof row.parentSessionID === 'string')) throw new Error('invalid retained collective contribution fixture')
}
const emptyGrouped = (url) => ({ items: [], page: Number(url.searchParams.get('page') || 1), limit: Number(url.searchParams.get('limit') || 100), totalItems: 0, ordinarySessionTotal: 0, helperThreadTotal: 0 })
function retainedGrouped(url) {
  const page = Number(url.searchParams.get('page') || 1)
  const limit = Number(url.searchParams.get('limit') || RETAINED_GROUPED.pageSize)
  const items = page === 1 ? Array.from({ length: limit }, (_, index) => ({ kind: 'transcript', transcript: { session: makeTranscriptFixture({ id: fixtureUUID(`retained-ordinary-${index}`), owner_id: fixtureUUID('user-demo'), local_id: `ses_ordinary${index}`, visibility: 'private', title: `ordinary grouped contribution ${index}` }) } })) : [{ kind: 'context_container', context: { groupId: RETAINED_GROUPED.groupID, ownerStatus: 'known_unavailable' }, helperGroups: [{ groupId: RETAINED_GROUPED.groupID, purpose: RETAINED_GROUPED.purpose, helperThreadCount: 1, memberScope: RETAINED_GROUPED.memberScope }] }]
  return validated(zVillageSessionListPayload, { items, page, limit, totalItems: RETAINED_GROUPED.totalItems, ordinarySessionTotal: RETAINED_GROUPED.pageSize, helperThreadTotal: 1 })
}
const VIEWER_ID = 'user-demo'
const ago = (minutes) => new Date(FROZEN_EPOCH_MS - minutes * 60_000).toISOString()

export const JOURNEY_COLLECTIVES = {
  /** The viewer owns it: settings, the picker and the org total are theirs. */
  owner: { id: 'c011ec71-0000-4000-8000-000000000001', name: 'Acme Platform' },
  /** The viewer is a member: no settings, no manage. */
  member: { id: 'c011ec71-0000-4000-8000-000000000002', name: 'Acme Company' },
  /** The viewer has not joined: an open collective they may join. */
  visitor: { id: 'c011ec71-0000-4000-8000-000000000003', name: 'ML Reading Group' },
}

const person = (id, login, name, role) => ({
  id,
  github_username: login,
  display_name: name,
  avatar_url: null,
  github_orgs: [],
  joined_at: ago(60 * 24 * 90),
  role,
})

const PLATFORM_MEMBERS = [
  person(VIEWER_ID, 'alice-dev', 'Alice Developer', 'owner'),
  person('user-bob', 'bob-ai', 'Bob Ainsley', 'member'),
  person('user-carol', 'carol-ml', 'Carol Mendes', 'member'),
  person('user-dan', 'dan-ops', 'Dan Okafor', 'contributor'),
  person('user-erin', 'erin-web', 'Erin Walsh', 'member'),
  person('user-farid', 'farid-k', 'Farid Karimi', 'contributor'),
]

const pullRequests = (repo, numbers, count = numbers.length) => {
  const [owner, name] = repo.split('/')
  return { count, recent: numbers.slice(0, 3).map((number) => ({ owner, name, number })) }
}

const transcript = (id, title, provider, turns, author, prs, minutes) => ({
  id,
  title,
  model_provider: provider,
  turn_count: turns,
  owner_id: author.id,
  owner_username: author.github_username,
  owner_avatar_url: null,
  owner_is_discoverable: true,
  pull_requests: prs,
  local_id: id,
  parent_session_id: id === "c41a9f20-0000-4000-8000-000000000002" ? "3f9c0a17-0000-4000-8000-000000000001" : null,
  project_hash: "a".repeat(64),
  project_display_name: prs.recent[0]?.name ?? "worker",
  project_name_source: "remote",
  git_remote: prs.recent[0] ? `https://github.com/${prs.recent[0].owner}/${prs.recent[0].name}` : null,
  git_branch: "main",
  model_name: null,
  published_at: ago(minutes),
  session_start: ago(minutes + 40),
  token_count: turns * 12000,
  tokens_in: null,
  tokens_out: null,
  visibility: 'shared',
})

const alice = PLATFORM_MEMBERS[0]
const bob = PLATFORM_MEMBERS[1]
const carol = PLATFORM_MEMBERS[2]

const PLATFORM_TRANSCRIPTS = [
  transcript('3f9c0a17-0000-4000-8000-000000000001', 'Fix flaky ingest test', 'claude-code', 37, alice, pullRequests('acme/ingest-api', [42, 45, 51], 4), 12),
  transcript('c41a9f20-0000-4000-8000-000000000002', 'Guard empty turns in the digest', 'claude-code', 22, alice, pullRequests('acme/ingest-api', [42]), 120),
  transcript('5d82a4b0-0000-4000-8000-000000000003', 'Paginate the collectives list', 'codex', 51, bob, pullRequests('acme/web', [31, 33]), 60 * 26),
  transcript('91be07c4-0000-4000-8000-000000000004', 'Migrate retry queue to worker', 'opencode', 64, bob, { count: 0, recent: [] }, 60 * 50),
  transcript('b4d9e310-0000-4000-8000-000000000005', 'Refactoring database queries', 'gemini-cli', 91, carol, pullRequests('acme/ingest-api', [36, 38, 40]), 60 * 24 * 8),
]

const repo = (name, isPrivate, publishers = 0) => ({ owner: 'acme', name, is_private: isPrivate, publisher_count: publishers })

const ACME_REPOS = [
  repo('ingest-api', true, 4),
  repo('web', false, 2),
  repo('worker', false, 3),
  repo('cli', false, 1),
  repo('infra', true),
  repo('docs', false),
  repo('billing', true),
  repo('auth', false),
  repo('mobile', false),
  repo('design-tokens', false),
  repo('status-page', false),
  repo('sdk-js', false),
  repo('sdk-go', false),
  repo('terraform', true),
]

const linkedRow = (groupId, name) => {
  const found = ACME_REPOS.find((r) => r.name === name)
  return {
    id: `${groupId.slice(0, 24)}${name.padEnd(12, '0').slice(0, 12)}`,
    group_id: groupId,
    owner: 'acme',
    name,
    is_private: found?.is_private ?? false,
    installation_id: 4242,
    linked_by: VIEWER_ID,
    created_at: ago(60 * 24 * 30),
    last_synced_at: ago(60 * 3),
  }
}

const group = (id, name, description, overrides) => ({
  id,
  name,
  description,
  acceptance_mode: 'open',
  data_access: 'members_only',
  linked_github_org: null,
  display_members: true,
  transcript_deletion_policy: 'user_choice',
  post_prompts_check: true,
  prompts_check_mode: 'informational',
  created_by: VIEWER_ID,
  created_at: ago(60 * 24 * 200),
  updated_at: ago(60 * 24 * 2),
  ...overrides,
})

// ── discover and new-collective fixtures ────────────────────────────────────
//
// Collectives the viewer may see but has not joined, one per way of joining,
// so /discover has a card for each: an open one anybody joins, a verified one
// tied to an org the viewer is not in (the join is refused with the server's
// reason), and a curated one with no join at all. The verified and curated ones
// are public, which is what makes them visible to a non-member.

export const DISCOVER_COLLECTIVES = {
  /** Open: the viewer may join it. */
  open: { id: 'c011ec71-0000-4000-8000-000000000011', name: 'Open Evals 2026' },
  /** Verified: tied to an org the viewer is not in, so joining is refused. */
  verified: { id: 'c011ec71-0000-4000-8000-000000000012', name: 'Rust Review Club', org: 'rust-club' },
  /** Curated: invite only, so the card offers no join. */
  curated: { id: 'c011ec71-0000-4000-8000-000000000013', name: 'Agents In Class' },
}

/** Handles of people who have never signed in: adding one is refused, as the server refuses it. */
const NEVER_SIGNED_IN = new Set(['nobody-here'])

function discoverEntries() {
  const { open, verified, curated } = DISCOVER_COLLECTIVES
  const crowd = (prefix, count) => Array.from({ length: count }, (_, n) => person(`user-${prefix}-${n}`, `${prefix}-${n}`, null, n === 0 ? 'owner' : 'member'))
  const entry = (row, description, overrides, members, transcripts) => ({
    group: group(row.id, row.name, description, { created_by: members[0].id, ...overrides }),
    role: '',
    members,
    transcripts: [],
    stats: { total_transcripts: transcripts, contributor_count: members.length, total_turns: transcripts * 30, total_duration_ms: 3_600_000 * transcripts, total_tokens: transcripts * 400_000, pull_request_count: 0 },
    linked: [],
    memberSince: null,
  })
  return {
    [open.id]: entry(open, 'Shared evaluation runs for agent benchmarks, published as they finish.', {}, crowd('evals', 12), 1940),
    [verified.id]: entry(verified, null, { acceptance_mode: 'verified_only', data_access: 'public', linked_github_org: verified.org }, crowd('rust', 4), 310),
    [curated.id]: entry(curated, 'A course on working with coding agents. Students publish; the teacher approves each session.', { acceptance_mode: 'curated', data_access: 'public' }, crowd('class', 6), 488),
  }
}

function freshWorld() {
  const { owner, member, visitor } = JOURNEY_COLLECTIVES
  return {
    collectives: {
      [owner.id]: {
        group: group(owner.id, owner.name, 'Transcripts behind the ingest and web services.', { linked_github_org: 'acme' }),
        role: 'owner',
        members: PLATFORM_MEMBERS,
        transcripts: [...PLATFORM_TRANSCRIPTS],
        myShares: RETAINED_CONTRIBUTIONS.map((row) => ({ id: row.id, title: row.title, model_provider: "claude-code", owner_id: VIEWER_ID, local_id: row.localID, parent_session_id: row.parentSessionID, visibility: "private", status: row.status, shared_at: ago(30) })),
        stats: { total_transcripts: 248, contributor_count: 9, total_turns: 9120, total_duration_ms: 3_600_000 * 41, total_tokens: 124_000_000, pull_request_count: 31 },
        linked: ['ingest-api', 'web'],
        memberSince: ago(60 * 24 * 150),
      },
      [member.id]: {
        group: group(member.id, member.name, 'Every team at Acme.', { acceptance_mode: 'verified_only', linked_github_org: 'acme', created_by: 'user-bob' }),
        role: 'member',
        members: [person('user-bob', 'bob-ai', 'Bob Ainsley', 'owner'), person(VIEWER_ID, 'alice-dev', 'Alice Developer', 'member'), person('user-carol', 'carol-ml', 'Carol Mendes', 'contributor')],
        transcripts: PLATFORM_TRANSCRIPTS.slice(0, 3),
        stats: { total_transcripts: 1904, contributor_count: 60, total_turns: 80000, total_duration_ms: 3_600_000 * 300, total_tokens: 900_000_000, pull_request_count: 212 },
        linked: ['ingest-api', 'web', 'worker'],
        memberSince: ago(60 * 24 * 40),
      },
      [visitor.id]: {
        group: group(visitor.id, visitor.name, 'Papers we read and the sessions that reproduce them.', { created_by: 'user-carol' }),
        role: '',
        members: [person('user-carol', 'carol-ml', 'Carol Mendes', 'owner'), person('user-dan', 'dan-ops', 'Dan Okafor', 'member')],
        transcripts: [],
        stats: { total_transcripts: 54, contributor_count: 12, total_turns: 3000, total_duration_ms: 3_600_000 * 20, total_tokens: 21_000_000, pull_request_count: 0 },
        linked: [],
        memberSince: null,
      },
      ...discoverEntries(),
    },
  }
}

let world = freshWorld()

/** Put every collective back as it started. */
export function resetCollectiveWorld() {
  world = freshWorld()
}

const canRead = (entry) => {
  const role = entry.role
  if (entry.group.data_access === 'public') return true
  if (entry.group.data_access === 'contributors') return ['contributor', 'member', 'owner'].includes(role)
  return role === 'member' || role === 'owner'
}

function visibleRow(entry) {
  return {
    ...entry.group,
    role: entry.role || null,
    member_since: entry.memberSince,
    member_count: entry.members.length,
    transcript_count: entry.stats.total_transcripts,
  }
}

function detail(entry, url) {
  const readable = canRead(entry)
  const limit = Number(url.searchParams.get('limit') || 20)
  const offset = Number(url.searchParams.get('offset') || 0)
  return {
    group: entry.group,
    members: entry.members,
    transcripts: readable ? entry.transcripts.slice(offset, offset + limit) : [],
    stats: entry.stats,
    models: [],
    contributors: [],
    can_read: readable,
    your_role: entry.role,
    ...(entry.role === 'owner' ? { pending_members: [] } : {}),
  }
}

const send = (res, code, body) => {
  res.writeHead(code, {
    'content-type': 'application/json',
    'access-control-allow-origin': '*',
    'access-control-allow-headers': '*',
    'access-control-allow-methods': 'GET,POST,PATCH,DELETE,OPTIONS',
  })
  res.end(body == null ? '' : JSON.stringify(body))
}

const readJSON = (req) =>
  new Promise((resolve) => {
    let data = ''
    req.on('data', (chunk) => { data += chunk })
    req.on('end', () => {
      try { resolve(data ? JSON.parse(data) : {}) } catch { resolve({}) }
    })
  })

/** Serve a collective route from the world; false leaves the request to the next handler. */
export function handleCollectiveRequest(req, res) {
  const url = new URL(req.url, 'http://localhost')
  const path = url.pathname.replace(/^\/api\/v1/, '')
  const entries = Object.values(world.collectives)

  if (req.method === 'GET' && path === '/collectives/visible') {
    send(res, 200, entries.map(visibleRow))
    return true
  }
  if (req.method === 'GET' && path === '/users/me/collectives/contributions') {
    send(res, 200, { collectives: [] })
    return true
  }
  if (req.method === 'GET' && path === '/collectives/search') {
    const q = (url.searchParams.get('q') || '').trim().toLowerCase()
    const hits = entries.filter((entry) =>
      entry.group.name.toLowerCase().includes(q) || (entry.group.linked_github_org || '').toLowerCase().includes(q))
    if (!q || hits.length === 0) return false
    send(res, 200, {
      collectives: hits.map((entry) => ({
        id: entry.group.id,
        name: entry.group.name,
        description: entry.group.description,
        linked_github_org: entry.group.linked_github_org,
        member_count: entry.members.length,
        transcript_count: entry.stats.total_transcripts,
      })),
    })
    return true
  }

  if (req.method === 'GET' && path === `/transcript-groups/${RETAINED_GROUPED.groupID}/members`) {
    if (url.searchParams.get('scope') !== RETAINED_GROUPED.memberScope) { send(res, 409, { error: 'scope expired' }); return true }
    const member = { kind: 'transcript', transcript: { session: makeTranscriptFixture({ id: RETAINED_GROUPED.memberID, owner_id: fixtureUUID(VIEWER_ID), local_id: 'ses_laterhelper', title: RETAINED_GROUPED.title, visibility: 'private' }) } }
    send(res, 200, validated(zVillageHelperMembersPayload, { members: [member], page: 1, limit: Number(url.searchParams.get('limit')), total: 1 }))
    return true
  }
  const withdrawal = path.match(/^\/transcripts\/([^/]+)\/share\/([^/]+)$/)
  if (withdrawal && req.method === 'DELETE') {
    const entry = world.collectives[withdrawal[2]]
    if (!entry) return false
    entry.myShares = (entry.myShares || []).filter((row) => row.id !== withdrawal[1])
    send(res, 200, { status: 'removed' })
    return true
  }
  const match = path.match(/^\/collectives\/([^/]+)(\/.*)?$/)
  if (!match) return false
  const entry = world.collectives[match[1]]
  if (!entry) return false
  const rest = match[2] || ''

  if (rest === '' && req.method === 'GET') {
    send(res, 200, url.searchParams.get("view") === "grouped" ? validated(zVillageGroupedGroupDetailResponse, { ...detail(entry, url), group: { ...entry.group, created_by: fixtureUUID(entry.group.created_by) }, members: entry.members.map((member) => ({ ...member, id: fixtureUUID(member.id) })), transcriptList: emptyGrouped(url) }) : detail(entry, url))
    return true
  }
  if (rest === '' && req.method === 'PATCH') {
    readJSON(req).then((body) => {
      if (body.name !== undefined) entry.group.name = body.name
      if (body.description !== undefined) entry.group.description = body.description || null
      for (const key of ['acceptance_mode', 'data_access', 'transcript_deletion_policy', 'post_prompts_check', 'display_members']) {
        if (body[key] !== undefined) entry.group[key] = body[key]
      }
      send(res, 200, entry.group)
    })
    return true
  }
  if (rest === '/my-shares' && req.method === 'GET') {
    send(res, 200, url.searchParams.get("view") === "grouped" ? (entry.group.id === JOURNEY_COLLECTIVES.owner.id ? retainedGrouped(url) : emptyGrouped(url)) : (entry.myShares || []))
    return true
  }
  const removedTranscript = rest.match(/^\/transcripts\/([^/]+)$/)
  if (removedTranscript && req.method === 'DELETE') {
    entry.transcripts = entry.transcripts.filter((row) => row.id !== removedTranscript[1])
    send(res, 200, { status: 'removed' })
    return true
  }
  if (rest === '/repositories' && req.method === 'GET') {
    if (!entry.role) { send(res, 403, { error: 'Membership required' }); return true }
    send(res, 200, { repositories: entry.linked.map((name) => linkedRow(entry.group.id, name)) })
    return true
  }
  if (rest === '/repositories/available' && req.method === 'GET') {
    if (entry.role !== 'owner') { send(res, 403, { error: 'Owner access required' }); return true }
    send(res, 200, { repositories: entry.group.linked_github_org ? ACME_REPOS : [] })
    return true
  }
  if (rest === '/repositories' && req.method === 'POST') {
    readJSON(req).then((body) => {
      if (!entry.linked.includes(body.name)) entry.linked.push(body.name)
      send(res, 201, linkedRow(entry.group.id, body.name))
    })
    return true
  }
  const unlink = rest.match(/^\/repositories\/([^/]+)\/([^/]+)$/)
  if (unlink && req.method === 'DELETE') {
    entry.linked = entry.linked.filter((name) => name !== decodeURIComponent(unlink[2]))
    send(res, 200, { status: 'unlinked' })
    return true
  }
  if (rest === '/members' && req.method === 'POST') {
    readJSON(req).then((body) => {
      entry.members.push(person(`invited-${body.username}`, body.username, null, 'member'))
      send(res, 200, { status: 'added' })
    })
    return true
  }
  const memberRole = rest.match(/^\/members\/([^/]+)\/role$/)
  if (memberRole && req.method === 'PATCH') {
    readJSON(req).then((body) => {
      entry.members = entry.members.map((m) => (m.id === memberRole[1] ? { ...m, role: body.role } : m))
      send(res, 200, { status: 'updated', role: body.role })
    })
    return true
  }
  return false
}

// ── a collective's own page: its waiting queue and its public reading ───────
//
// Kept apart from the list world above. Two scenarios reshape the world after
// `resetCollectiveWorld` (scripts/journey/mock.mjs calls `applyCollectivePageScenario`):
//   curated-owner                - the viewer's own collective is curated, and two
//                                  sessions wait for their approval
//   signed-out-public-collective - the collective the viewer has not joined is
//                                  public, so a reader with no account reads it

/** What waits for the owner of the curated collective, as `GET /collectives/{id}/pending` answers it. */
export const JOURNEY_PENDING = [
  { transcript_id: '7b000000-0000-4000-8000-000000000001', title: 'Add retries to the webhook', owner: 'user-dan', login: 'dan-ops', provider: 'codex', project: 'ingest-api', branch: 'retries', minutes: 40 },
  { transcript_id: '7b000000-0000-4000-8000-000000000002', title: 'Trim the onboarding copy', owner: 'user-carol', login: 'carol-ml', provider: 'claude-code', project: 'web', branch: 'main', minutes: 60 * 5 },
]

/** Reshape the world for a collective-page scenario; any other scenario leaves it as it started. */
export function applyCollectivePageScenario(name) {
  const { owner, visitor } = JOURNEY_COLLECTIVES
  if (name === 'curated-owner') {
    const entry = world.collectives[owner.id]
    entry.group.acceptance_mode = 'curated'
    entry.pending = JOURNEY_PENDING.map((row) => ({
      transcript_id: row.transcript_id,
      title: row.title,
      model_provider: row.provider,
      owner_id: row.owner,
      owner_username: row.login,
      owner_is_discoverable: true,
      local_id: `local-${row.transcript_id.slice(-4)}`,
      parent_session_id: null,
      project_hash: 'b'.repeat(64),
      project_name: row.project,
      branch: row.branch,
      shared_at: ago(row.minutes),
    }))
  }
  if (name === 'signed-out-public-collective') {
    const entry = world.collectives[visitor.id]
    entry.group.data_access = 'public'
    entry.transcripts = PLATFORM_TRANSCRIPTS.slice(2, 5)
  }
}

/** The owner's pending queue, and the settings page's removals; false leaves the request to the next handler. */
export function handleCollectivePageRequest(req, res) {
  const url = new URL(req.url, 'http://localhost')
  const path = url.pathname.replace(/^\/api\/v1/, '')
  const match = path.match(/^\/collectives\/([^/]+)(\/.*)?$/)
  if (!match) return false
  const entry = world.collectives[match[1]]
  if (!entry) return false
  const rest = match[2] || ''
  if (rest === '/pending' && req.method === 'GET') {
    if (entry.role !== 'owner') { send(res, 403, { error: 'Owner access required' }); return true }
    send(res, 200, entry.pending || [])
    return true
  }
  const member = rest.match(/^\/members\/([^/]+)$/)
  if (member && req.method === 'DELETE') {
    entry.members = entry.members.filter((m) => m.id !== decodeURIComponent(member[1]))
    send(res, 200, { status: 'removed' })
    return true
  }
  return false
}

// ── new-collective and join writes (see discoverEntries above) ──────────────

/**
 * The writes the new-collective dialog and /discover make: create a
 * collective, join one, and add a person (refused, the way the server refuses
 * it, for a handle that has never signed in). The `viewer-in-org` scenario also gives the
 * viewer a visible GitHub org, so the dialog's org picker has one to offer.
 * Returns false for everything else.
 */
export function handleNewCollectiveRequest(req, res, scenario) {
  const url = new URL(req.url, 'http://localhost')
  const path = url.pathname.replace(/^\/api\/v1/, '')

  if (req.method === 'GET' && path === '/auth/orgs' && scenario === 'viewer-in-org') {
    send(res, 200, [{ org_id: 4242, org_login: 'acme', avatar_url: null, visible: true, fetched_at: ago(60) }])
    return true
  }
  if (req.method === 'POST' && path === '/collectives') {
    readJSON(req).then((body) => {
      const name = typeof body.name === 'string' ? body.name : ''
      if (!name) { send(res, 400, { error: 'Name is required' }); return }
      if (body.linked_github_org && !(scenario === 'viewer-in-org' && body.linked_github_org === 'acme')) {
        send(res, 400, { error: 'You must be a member of the GitHub organization to link this collective to it' })
        return
      }
      const id = fixtureUUID(`created-${Object.keys(world.collectives).length}-${name}`)
      const created = group(id, name, body.description || null, {
        acceptance_mode: body.acceptance_mode || 'open',
        data_access: body.data_access || 'members_only',
        linked_github_org: body.linked_github_org || null,
        created_at: ago(0),
        updated_at: ago(0),
      })
      world.collectives[id] = {
        group: created,
        role: 'owner',
        members: [person(VIEWER_ID, 'alice-dev', 'Alice Developer', 'owner')],
        transcripts: [],
        stats: { total_transcripts: 0, contributor_count: 0, total_turns: 0, total_duration_ms: 0, total_tokens: 0, pull_request_count: 0 },
        linked: [],
        memberSince: ago(0),
        createdByDialog: true,
      }
      const { post_prompts_check, prompts_check_mode, ...record } = created
      send(res, 201, { ...record, post_prompts_check, prompts_check_mode })
    })
    return true
  }
  const join = path.match(/^\/collectives\/([^/]+)\/join$/)
  if (join && req.method === 'POST') {
    const entry = world.collectives[join[1]]
    if (!entry) { send(res, 404, { error: 'Group not found' }); return true }
    const mode = entry.group.acceptance_mode
    if (mode === 'curated') { send(res, 403, { error: 'This collective requires an invitation to join' }); return true }
    if (mode === 'verified_only' && entry.group.linked_github_org !== 'acme') {
      send(res, 403, { error: `This collective requires membership of @${entry.group.linked_github_org}` })
      return true
    }
    if (entry.role) { send(res, 409, { error: 'Already a member of this collective' }); return true }
    entry.role = 'contributor'
    entry.memberSince = ago(0)
    entry.members = [...entry.members, person(VIEWER_ID, 'alice-dev', 'Alice Developer', 'contributor')]
    send(res, 200, { status: 'joined', role: 'contributor' })
    return true
  }
  // Adding people to a collective the dialog just created; additions to the
  // fixture's own collectives fall through to handleCollectiveRequest, which
  // the settings journeys drive.
  const members = path.match(/^\/collectives\/([^/]+)\/members$/)
  if (members && req.method === 'POST' && world.collectives[members[1]]?.createdByDialog) {
    readJSON(req).then((body) => {
      const entry = world.collectives[members[1]]
      if (NEVER_SIGNED_IN.has(body.username)) {
        send(res, 404, { error: `@${body.username} hasn't joined the platform yet. They need to sign in first.` })
        return
      }
      entry.members = [...entry.members.filter((m) => m.github_username !== body.username), person(`user-${body.username}`, body.username, null, 'member')]
      send(res, 200, { status: 'added' })
    })
    return true
  }
  return false
}
