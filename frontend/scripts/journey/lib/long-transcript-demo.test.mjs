// @vitest-environment node
import { readFileSync } from 'node:fs'
import { Readable } from 'node:stream'
import { afterEach, expect, it, vi } from 'vitest'
import { parse } from 'yaml'
import { parseSessionDetailPayloadValue, zAnnotationSummary, zVillageTranscriptMetadataResponse, zVillageTranscriptPullRequestsResponse } from '@peasant-labs/schema'
import { adaptTranscript } from '@peasant-labs/fairtrade/ui'
import { LONG_DEMO, LONG_DEMO_CONTENT, LONG_DEMO_METADATA, handleLongDemoRequest, longDemoClaudeRecords, resetLongDemo } from './long-transcript-demo.mjs'

const fixture = parse(readFileSync(new URL('../testdata/long-transcript-demo-cases.yaml', import.meta.url), 'utf8'))
const required = ['disabled-demo-does-not-claim-the-production-route', 'named-demo-serves-schema-validated-metadata', 'named-demo-serves-the-complete-long-content', 'demo-keeps-the-mounted-pull-request-read-available', 'demo-serves-in-range-review-labels', 'demo-records-a-new-review-label', 'demo-refuses-a-label-outside-the-transcript']
expect(fixture.cases.map(({ name }) => name).sort()).toEqual(required.sort())
afterEach(() => vi.unstubAllEnvs())
for (const testCase of fixture.cases) {
  it(testCase.name, async () => {
    vi.stubEnv('MOCK_DEMO_ATTACHMENTS', testCase.enabled ? '1' : '')
    resetLongDemo()
    const req = Readable.from(testCase.body ? [JSON.stringify(testCase.body)] : [])
    req.method = testCase.method
    req.url = `/api/v1/transcripts/${LONG_DEMO.alias}${testCase.suffix}`
    let status, body
    const handled = await handleLongDemoRequest(req, { writeHead(value) { status = value }, end(value) { body = JSON.parse(value) } })
    expect(handled).toBe(testCase.handled ?? true)
    if (!handled) return
    expect(status).toBe(testCase.status)
    if (testCase.schema === 'metadata') expect(zVillageTranscriptMetadataResponse.parse(body)).toEqual(LONG_DEMO_METADATA)
    if (testCase.schema === 'content') expect(parseSessionDetailPayloadValue(body)).toEqual(LONG_DEMO_CONTENT)
    if (testCase.schema === 'pulls') expect(zVillageTranscriptPullRequestsResponse.safeParse(body).success).toBe(true)
    if (testCase.schema === 'annotation') expect(zAnnotationSummary.safeParse(body).success).toBe(true)
    if (testCase.schema === 'annotations') {
      expect(body.annotations).toHaveLength(fixture.expected.annotationCount)
      for (const annotation of body.annotations) {
        expect(zAnnotationSummary.safeParse(annotation).success).toBe(true)
        expect(annotation.targetEntryIndex).toBeGreaterThanOrEqual(0)
        expect(annotation.targetEntryIndex).toBeLessThan(fixture.expected.turnCount)
      }
    }
  })
}
it('the long content exercises real phases, files and edit diffs', () => {
  const content = parseSessionDetailPayloadValue(LONG_DEMO_CONTENT)
  expect(content.turnCount).toBe(fixture.expected.turnCount)
  expect(content.turns).toHaveLength(content.turnCount)
  expect(content.turns.flatMap((turn) => turn.toolCalls ?? [])).toHaveLength(fixture.expected.toolCallCount)
  const vm = adaptTranscript(content)
  for (const phase of fixture.expected.phaseLabels) expect(vm.analytics.phases.map((row) => row.label)).toContain(phase)
  for (const path of fixture.expected.diffPaths) expect(vm.diffs.map((row) => row.path)).toContain(path)
})
it('the native Claude recording preserves the same prompts, edits and tool results', () => {
  const records = longDemoClaudeRecords('/tmp/preview/activity-export-demo')
  expect(records).toHaveLength(fixture.expected.sourceRecordCount)
  expect(records.every((row) => row.sessionId === LONG_DEMO.id)).toBe(true)
  const prompts = records.filter((row) => row.type === 'user' && typeof row.message.content === 'string')
  expect(prompts.map((row) => row.message.content)).toEqual(LONG_DEMO.steps.map((step) => step.prompt))
  const calls = records.flatMap((row) => Array.isArray(row.message.content) ? row.message.content.filter((part) => part.type === 'tool_use') : [])
  const results = records.flatMap((row) => Array.isArray(row.message.content) ? row.message.content.filter((part) => part.type === 'tool_result') : [])
  expect(calls.map((call) => call.input)).toEqual(LONG_DEMO.steps.map((step) => step.tool.input))
  expect(results.map((result) => result.tool_use_id)).toEqual(calls.map((call) => call.id))
  expect(results.map((result) => result.content)).toEqual(LONG_DEMO.steps.map((step) => step.tool.result))
})
