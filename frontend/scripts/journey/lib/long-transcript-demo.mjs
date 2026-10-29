/* An opt-in long session for the real transcript route, independent of the landing example. */
import { readFileSync } from 'node:fs'
import { randomUUID } from 'node:crypto'
import { parse } from 'yaml'
import { parseSessionDetailPayloadValue, zAnnotationSummary, zVillageTranscriptMetadataResponse } from '@peasant-labs/schema'
import { makeTranscriptFixture } from '../../../src/test/transcriptRowFixture.ts'
import { profileOwner } from '../../../src/test/publicOwnerFixture.ts'
import { fixtureUUID } from './collective-fixtures.mjs'
import { ANNOTATION_TYPES } from '../../../src/lib/annotations.ts'

export const LONG_DEMO = parse(readFileSync(new URL('../testdata/long-transcript-demo.yaml', import.meta.url), 'utf8'), { strict: true })
const timestamp = (index) => new Date(Date.parse(LONG_DEMO.start) + index * 60_000).toISOString()
const toolKind = (tool) => tool.kind ?? (tool.name === 'Read' ? 'read' : 'search')
const turns = LONG_DEMO.steps.flatMap((step, position) => {
  const index = position * 3
  const common = (offset, role, content, tokensIn, tokensOut) => ({ index: index + offset, role, content, depth: 0,
    timestamp: timestamp(index + offset), sourceEntryRef: `long-demo-entry-${index + offset}`, tokensIn, tokensOut })
  return [
    common(0, 'user', step.prompt, 80, 0),
    { ...common(1, 'assistant', step.plan, 1200, 160), observedModel: `anthropic/${LONG_DEMO.model}`,
      toolCalls: [{ id: `long-demo-tool-${position}`, name: step.tool.name, arguments: JSON.stringify(step.tool.input),
        result: step.tool.result, durationMs: 150, isError: step.tool.error === true, toolKind: toolKind(step.tool),
        ...(step.tool.input.file_path ? { filePath: step.tool.input.file_path } : {}),
        ...(step.tool.name === 'Bash' ? { exitCode: step.tool.error ? 1 : 0 } : {}) }] },
    common(2, 'assistant', step.conclusion, 1200, 120),
  ]
})
export const LONG_DEMO_CONTENT = parseSessionDetailPayloadValue({
  id: LONG_DEMO.id, harness: 'claude-code', model: `anthropic/${LONG_DEMO.model}`, project: LONG_DEMO.project,
  workingDirectory: `/workspace/${LONG_DEMO.project}`, gitBranch: LONG_DEMO.branch,
  startTime: timestamp(0), endTime: timestamp(turns.length - 1), durationMins: turns.length - 1,
  tokensIn: turns.reduce((sum, turn) => sum + turn.tokensIn, 0),
  tokensOut: turns.reduce((sum, turn) => sum + turn.tokensOut, 0),
  totalTokens: turns.reduce((sum, turn) => sum + turn.tokensIn + turn.tokensOut, 0),
  turnCount: turns.length, inputSubmissionCount: LONG_DEMO.steps.length, toolCallCount: LONG_DEMO.steps.length,
  outcome: 'resolved', sessionOrigin: 'user', turns,
})
export const LONG_DEMO_METADATA = zVillageTranscriptMetadataResponse.parse({
  transcript: makeTranscriptFixture({ id: LONG_DEMO.id, local_id: LONG_DEMO.id, title: LONG_DEMO.title,
    description: 'Synthetic long-session preview with implementation, a reproduced failure, tests, diffs and review labels.',
    visibility: 'private', model_name: LONG_DEMO_CONTENT.model, project_name: LONG_DEMO.project,
    project_display_name: LONG_DEMO.project, git_branch: LONG_DEMO.branch,
    session_start: LONG_DEMO_CONTENT.startTime, session_end: LONG_DEMO_CONTENT.endTime,
    turn_count: turns.length, token_count: LONG_DEMO_CONTENT.totalTokens, tool_call_count: LONG_DEMO_CONTENT.toolCallCount,
    tokens_in: LONG_DEMO_CONTENT.tokensIn, tokens_out: LONG_DEMO_CONTENT.tokensOut,
    duration_ms: LONG_DEMO_CONTENT.durationMins * 60_000, outcome: 'resolved' }),
  owner: profileOwner({ id: fixtureUUID('user-demo'), github_username: 'alice-dev' }),
  tags: [], shares: [], enriched_shares: [], attestations: [], owner_orgs: [],
})
const initialAnnotations = LONG_DEMO.annotations.map((row) => ({
  id: row.id, targetKind: 'entry', targetEntryIndex: row.index, targetSessionId: LONG_DEMO.id,
  typeId: row.typeId, typeName: row.typeId, value: row.value, reason: row.reason,
  annotatorKind: 'human', annotatorName: 'alice-dev', createdAt: Date.parse(timestamp(turns.length)), isPrimary: true,
}))
for (const annotation of initialAnnotations) zAnnotationSummary.parse(annotation)
let annotations = structuredClone(initialAnnotations)
export function resetLongDemo() { annotations = structuredClone(initialAnnotations) }
const answer = (res, status, body) => {
  res.writeHead(status, { 'content-type': 'application/json', 'access-control-allow-origin': '*' })
  res.end(JSON.stringify(body))
}
export async function handleLongDemoRequest(req, res) {
  if (process.env.MOCK_DEMO_ATTACHMENTS !== '1') return false
  const path = new URL(req.url, 'http://fixture').pathname.replace(/^\/api\/v1/, '')
  const match = path.match(/^\/transcripts\/([^/]+)(\/(?:content|annotations|pulls|collectives))?$/)
  if (!match || ![LONG_DEMO.alias, LONG_DEMO.id].includes(match[1])) return false
  const suffix = match[2]
  if (req.method === 'GET') {
    if (suffix === '/content') answer(res, 200, LONG_DEMO_CONTENT)
    else if (suffix === '/annotations') answer(res, 200, { annotations })
    else if (suffix === '/pulls') answer(res, 200, { pull_requests: [], unverified_pull_requests: [] })
    else if (suffix === '/collectives') answer(res, 200, { collectives: [] })
    else answer(res, 200, LONG_DEMO_METADATA)
    return true
  }
  if (req.method === 'POST' && suffix === '/annotations') {
    let text = ''
    for await (const chunk of req) text += chunk
    try {
      const { typeId, value, entryIndex } = JSON.parse(text)
      const type = ANNOTATION_TYPES.find((row) => row.typeId === typeId)
      if (!type?.values.some((row) => row.value === value)) throw new Error('invalid label value')
      if (!Number.isInteger(entryIndex) || entryIndex < 0 || entryIndex >= turns.length || typeof typeId !== 'string' || typeof value !== 'string') throw new Error('invalid label')
      const annotation = { ...initialAnnotations[0], id: randomUUID(), targetEntryIndex: entryIndex, typeId, typeName: typeId, value, reason: null }
      zAnnotationSummary.parse(annotation)
      annotations.push(annotation)
      answer(res, 201, annotation)
    } catch { answer(res, 400, { error: 'invalid preview annotation' }) }
    return true
  }
  return false
}

/** Native Claude Code records for ingesting this same narrative into a disposable local database. */
export function longDemoClaudeRecords(cwd) {
  const records = []
  let parentUuid
  const append = (position, offset, type, message) => {
    const uuid = randomUUID()
    records.push({ sessionId: LONG_DEMO.id, cwd, version: '2.1.0', gitBranch: LONG_DEMO.branch, type, uuid,
      ...(parentUuid ? { parentUuid } : {}), timestamp: timestamp(position * 3 + offset), message })
    parentUuid = uuid
  }
  LONG_DEMO.steps.forEach((step, position) => {
    const id = `long-demo-tool-${position}`
    append(position, 0, 'user', { role: 'user', content: step.prompt })
    append(position, 1, 'assistant', { id: `long-demo-message-${position}-tool`, role: 'assistant', model: LONG_DEMO.model, stop_reason: 'tool_use',
      content: [{ type: 'text', text: step.plan }, { type: 'tool_use', id, name: step.tool.name, input: step.tool.input }], usage: { input_tokens: 1200, output_tokens: 160 } })
    append(position, 1.5, 'user', { role: 'user', content: [{ type: 'tool_result', tool_use_id: id, content: step.tool.result, is_error: step.tool.error === true }] })
    append(position, 2, 'assistant', { id: `long-demo-message-${position}-summary`, role: 'assistant', model: LONG_DEMO.model, stop_reason: 'end_turn',
      content: [{ type: 'text', text: step.conclusion }], usage: { input_tokens: 1200, output_tokens: 120 } })
  })
  return records
}
