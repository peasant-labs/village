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
import { FROZEN_EPOCH_MS } from './determinism-constants.mjs'

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

function freshWorld() {
  const { owner, member, visitor } = JOURNEY_COLLECTIVES
  return {
    collectives: {
      [owner.id]: {
        group: group(owner.id, owner.name, 'Transcripts behind the ingest and web services.', { linked_github_org: 'acme' }),
        role: 'owner',
        members: PLATFORM_MEMBERS,
        transcripts: PLATFORM_TRANSCRIPTS,
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

  if (req.method === 'GET' && path === '/groups/visible') {
    send(res, 200, entries.map(visibleRow))
    return true
  }
  if (req.method === 'GET' && path === '/users/me/collectives/contributions') {
    send(res, 200, { collectives: [] })
    return true
  }
  if (req.method === 'GET' && path === '/groups/search') {
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

  const match = path.match(/^\/groups\/([^/]+)(\/.*)?$/)
  if (!match) return false
  const entry = world.collectives[match[1]]
  if (!entry) return false
  const rest = match[2] || ''

  if (rest === '' && req.method === 'GET') {
    send(res, 200, detail(entry, url))
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
    send(res, 200, [])
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
