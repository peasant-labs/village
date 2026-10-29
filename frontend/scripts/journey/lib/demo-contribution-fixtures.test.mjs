// @vitest-environment node
import { readFileSync } from 'node:fs'
import { Readable } from 'node:stream'
import { afterEach, expect, it, vi } from 'vitest'
import { parse } from 'yaml'
import {
  zVillageBatchReviewResponse, zVillageBatchShareResponse, zVillageContributableResponse,
  zVillageGroupedContributableResponse, zVillagePendingShare, zVillageSessionListPayload,
  zVillageTranscriptMetadataResponse,
} from '@peasant-labs/schema'
import { fixtureUUID, JOURNEY_COLLECTIVES, JOURNEY_PENDING } from './collective-fixtures.mjs'
import { demoContributionContentPath, handleDemoContributionRequest, resetDemoContributions } from './demo-contribution-fixtures.mjs'

const cases = parse(readFileSync(new URL('../testdata/demo-contributions.yaml', import.meta.url), 'utf8')).cases
const required = ['disabled-preview-leaves-ordinary-journeys-alone', 'flat-and-grouped-pickers-share-the-same-owned-catalog', 'private-contribution-needs-consent-and-updates-the-queue', 'selected-session-metadata-and-content-are-previewable', 'decisions-remove-queued-rows-and-repeat-safely', 'member-cannot-read-an-owner-review-queue', 'rejected-own-submission-becomes-selectable-again']
expect(cases.map((row) => row.name).sort()).toEqual(required.sort())
const eligibleID = fixtureUUID('ct-home-5')
const replace = (value) => JSON.parse(JSON.stringify(value)
  .replaceAll('{owner}', JOURNEY_COLLECTIVES.owner.id)
  .replaceAll('{member}', JOURNEY_COLLECTIVES.member.id)
  .replaceAll('{eligible}', eligibleID)
  .replaceAll('{queued}', JOURNEY_PENDING[0].transcript_id))
const schemas = {
  contributable: zVillageContributableResponse,
  groupedContributable: zVillageGroupedContributableResponse,
  share: zVillageBatchShareResponse,
  metadata: zVillageTranscriptMetadataResponse,
  review: zVillageBatchReviewResponse,
  pending: zVillagePendingShare.array(),
  groupedPending: zVillageSessionListPayload,
}
afterEach(() => vi.unstubAllEnvs())

for (const fixture of cases) {
  it(fixture.name, async () => {
    vi.stubEnv('MOCK_DEMO_ATTACHMENTS', fixture.enabled ? '1' : '')
    vi.stubEnv('MOCK_TRANSCRIPT_ID', fixture.contentId ?? '')
    resetDemoContributions()
    for (const step of replace(fixture.steps)) {
      if (step.resetBefore) resetDemoContributions()
      const req = Readable.from(step.body ? [JSON.stringify(step.body)] : [])
      req.method = step.method
      req.url = `/api/v1${step.path}`
      let status
      let body
      const handled = await handleDemoContributionRequest(req, {
        writeHead(value) { status = value },
        end(value) { body = JSON.parse(value) },
      })
      expect(handled).toBe(step.handled ?? true)
      if (!handled) continue
      expect(status).toBe(step.status)
      if (step.schema) expect(schemas[step.schema].safeParse(body).success).toBe(true)
      if ('eligible' in step || step.hasAlreadyShared) {
        const rows = body.transcripts ?? body.transcriptList.items.map((item) => item.transcript.contributable)
        if ('eligible' in step) expect(rows.find((row) => row.id === eligibleID).already_shared).toBe(!step.eligible)
        if (step.hasAlreadyShared) expect(rows.some((row) => row.already_shared)).toBe(true)
      }
      if (step.contains || step.excludes) {
        const ids = Array.isArray(body) ? body.map((row) => row.transcript_id) : body.items.map((item) => item.transcript.pending.transcript_id)
        for (const id of step.contains ?? []) expect(ids).toContain(id)
        for (const id of step.excludes ?? []) expect(ids).not.toContain(id)
      }
      if (step.shared) expect(body.shared).toEqual(step.shared)
      if (step.alreadyShared) expect(body.already_shared).toEqual(step.alreadyShared)
      if (step.decided) expect(body.decided).toEqual(step.decided)
      if (step.alreadyDecided) expect(body.already_decided).toEqual(step.alreadyDecided)
      if (step.title) expect(body.transcript.title).toBe(step.title)
      if (step.contentPath) expect(demoContributionContentPath(`${step.path}/content`)).toBe(step.contentPath)
    }
  })
}
