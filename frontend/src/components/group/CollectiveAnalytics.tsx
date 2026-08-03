"use client";

import { useMemo } from "react";
import {
  ProjectOverview,
  type AnalyticsOverviewPayload,
} from "@peasant-labs/fairtrade/analytics";
import type { GroupTranscript } from "@/lib/types";

interface CollectiveAnalyticsProps {
  /** Transcripts visible to the current viewer (recent page or full browse). */
  transcripts: GroupTranscript[];
}

/**
 * Village glue around the design system's `<ProjectOverview>` dashboard
 * (`@peasant-labs/fairtrade/analytics`). Maps a collective's shared
 * transcripts onto the fairtrade-owned session-record shape and feeds the
 * dashboard through its cooked payload prop.
 *
 * Honest mapping notes: village's group transcript records carry turns, tokens,
 * tool-call counts, provider, owner, duration and session timestamps. They do
 * NOT carry per-session outcomes or commit data, so those optional fields are
 * omitted — outcomes bucket as `unknown` (the resolved column renders its
 * placeholder rather than a false 0%) and commit counts read 0. Duration
 * prefers the stored `duration_ms`, falling back to the session start/end span.
 */
export default function CollectiveAnalytics({
  transcripts,
}: CollectiveAnalyticsProps) {
  const sessions = useMemo(
    () =>
      transcripts.map((t) => {
        const start = t.session_start ? Date.parse(t.session_start) : NaN;
        const end = t.session_end ? Date.parse(t.session_end) : NaN;
        const spanMins =
          Number.isFinite(start) && Number.isFinite(end) && end > start
            ? Math.round((end - start) / 60000)
            : 0;
        const durationMins =
          t.duration_ms != null ? Math.round(t.duration_ms / 60000) : spanMins;
        const totalTokens =
          t.token_count ?? (t.tokens_in ?? 0) + (t.tokens_out ?? 0);
        return {
          id: t.id,
          // session_start is the truest session time; fall back to publish time.
          startTime: t.session_start ?? t.published_at,
          projectKey: t.project_name ?? "transcript",
          contributorId: t.owner_id,
          durationMins,
          totalTokens,
          turnCount: t.turn_count ?? 0,
          toolCallCount: t.tool_call_count ?? 0,
        };
      }),
    [transcripts],
  );
  const payload = useMemo<AnalyticsOverviewPayload>(
    () => ({ sessions }),
    [sessions],
  );

  if (sessions.length === 0) return null;

  return (
    <ProjectOverview
      payload={payload}
      title="Collective analytics"
      subtitle="Activity and contributor trends across shared transcripts."
    />
  );
}
