/**
 * Adapter signature for the Explore lifted surface.
 *
 * Combines three TanStack Query hook results into the single cooked
 * ExplorePayload that the lifted <Explore> component accepts as props.
 * The pattern mirrors SessionDetailV2: fetch → reshape → mount via props.
 *
 * Hooks consumed (all CONFIRMED IMPLEMENTED):
 *   • useTranscripts(params)      → TranscriptListResponse
 *   • useSearchCollectives(query) → CollectiveSearchResponse
 *   • usePopularTags(limit)       → TagWithCount[]
 *     (frontend/src/lib/queries/tags.ts — exists, do not add a new hook)
 *
 * TRANSFORM (small): all snake_case wire fields are renamed to camelCase;
 * fields not displayed by the Explore surface are omitted.
 *
 * Wire types sourced from: frontend/src/lib/types.ts
 * Payload type sourced from: @peasant-labs/fairtrade/commons.
 */

import type {
  TranscriptListResponse,
  CollectiveSearchResponse,
  TagWithCount,
} from '@/lib/types';
import type { ExplorePayload } from '@peasant-labs/fairtrade/commons';
import { isHarness, type Harness } from '@peasant-labs/schema';

function requireHarness(value: string, transcriptID: string): Harness {
  if (isHarness(value)) return value;
  throw new Error(
    `Explore transcript ${transcriptID} has unsupported harness ${JSON.stringify(value)}. ` +
    'The Village API returned a model_provider outside the published Schema harness menu; refresh after correcting the stored transcript contract value.',
  );
}

// ── Adapter signature ─────────────────────────────────────────────────────────

/**
 * Combine the three Explore hook results into the cooked ExplorePayload.
 * TRANSFORM (small): snake_case → camelCase; display-irrelevant fields omitted.
 *
 * The caller (the Explore page/shell) runs all three hooks and passes their
 * `.data` here. The adapter is called every time any of the three results
 * changes (query key changes, refetch, etc.).
 *
 * @param transcripts  useTranscripts(params).data — paginated transcript list
 * @param collectives  useSearchCollectives(query).data — collective search results
 * @param popularTags  usePopularTags(limit).data — popular tags for the facet rail
 * @returns Cooked prop payload for the lifted <Explore> surface
 */
export function adaptExplore(
  transcripts: TranscriptListResponse,
  collectives: CollectiveSearchResponse,
  popularTags: TagWithCount[],
): ExplorePayload {
  return {
    transcripts: {
      transcripts: transcripts?.transcripts?.map((item) => ({
        id: item.transcript.id,
        title: item.transcript.title,
        visibility: item.transcript.visibility,
        modelProvider: requireHarness(item.transcript.model_provider, item.transcript.id),
        modelName: item.transcript.model_name,
        harnessVersion: item.transcript.harness_version,
        sessionStart: item.transcript.session_start,
        sessionEnd: item.transcript.session_end,
        turnCount: item.transcript.turn_count,
        tokenCount: item.transcript.token_count,
        toolCallCount: item.transcript.tool_call_count,
        durationMs: item.transcript.duration_ms,
        gitBranch: item.transcript.git_branch,
        // The one server-resolved project name (`project_display_name`),
        // fed through the ONLY project field the fairtrade explore card's
        // ExploreTranscriptPayload carries. This is also what makes
        // fairtrade's own internal card grouping (which buckets by this
        // same `projectName` string) collapse a mixed-name, same-hash pair
        // into ONE group without any change to the fairtrade package — the
        // explore-card surface is satisfied through the adapter rather
        // than a fairtrade fork.
        projectName: item.transcript.project_display_name,
        tags: (item.tags ?? []).map((tag) => ({ id: tag.id, name: tag.name })),
        owner: {
          githubUsername: item.owner.github_username,
          displayName: item.owner.display_name,
          avatarUrl: item.owner.avatar_url,
        },
      })) ?? [],
      total: transcripts?.total ?? 0,
      page: transcripts?.page ?? 0,
      limit: transcripts?.limit ?? 0,
    },
    collectives: collectives?.collectives?.map((collective) => ({
      id: collective.id,
      name: collective.name,
      description: collective.description,
      linkedGithubOrg: collective.linked_github_org,
      memberCount: collective.member_count,
      transcriptCount: collective.transcript_count,
    })) ?? [],
    popularTags: (popularTags ?? []).map((tag) => ({
      id: tag.id,
      name: tag.name,
      usageCount: tag.usage_count,
    })),
  };
}
