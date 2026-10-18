import { zVillageGroupedContributableResponse, zVillageSessionListPayload } from '@peasant-labs/schema'
import { readFileSync } from 'node:fs'
import { parse } from 'yaml'

const { contributionContext } = parse(readFileSync(new URL('../testdata/collectives-built-support.yaml', import.meta.url), 'utf8'))

/** The two published operations have different envelopes, even when empty. */
export function collectiveQueueFixture(url, showContext = false) {
  const match = url.pathname.match(/^\/api\/v1\/collectives\/([^/]+)\/(pending|contributable)$/)
  if (!match) return undefined
  if (url.searchParams.get('view') !== 'grouped') return match[2] === 'pending' ? [] : { transcripts: [] }
  const transcriptList = zVillageSessionListPayload.parse({
    items: showContext && match[2] === 'contributable' ? [contributionContext] : [],
    page: Number(url.searchParams.get('page') || 1), limit: Number(url.searchParams.get('limit') || 100),
    totalItems: showContext && match[2] === 'contributable' ? 1 : 0, ordinarySessionTotal: 0,
    helperThreadTotal: showContext && match[2] === 'contributable' ? 1 : 0,
  })
  return match[2] === 'pending' ? transcriptList
    : zVillageGroupedContributableResponse.parse({ group_id: match[1], transcriptList })
}
