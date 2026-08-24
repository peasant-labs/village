"use client";

import { use, useMemo } from "react";
import { useTranscript, useTranscriptContent } from "@/lib/queries/transcripts";
import { useGroups } from "@/lib/queries/groups";
import { SessionDetailV2 } from "@/components/session-detail/v2/SessionDetailV2";
import PendingApprovalBar, {
  type PendingReview,
} from "@/components/transcript/PendingApprovalBar";
import type { SessionDetailPayload } from "@/types/messages";
import { FileX2 } from "lucide-react";

/** Narrowing guard: a SessionDetailPayload always carries a `turns` array. */
function isSessionDetailPayload(value: unknown): value is SessionDetailPayload {
  return (
    !!value &&
    typeof value === "object" &&
    !Array.isArray(value) &&
    Array.isArray((value as { turns?: unknown }).turns)
  );
}

export default function TranscriptDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const { data, isLoading } = useTranscript(id);
  const { data: content, isLoading: contentLoading, error: contentError } =
    useTranscriptContent(id);
  const { data: myGroups } = useGroups();

  const pendingReviews = useMemo<PendingReview[]>(() => {
    if (!data || !myGroups) return [];
    const ownedGroupIds = new Set(
      myGroups.filter((g) => g.role === "owner").map((g) => g.id)
    );
    return (data.enriched_shares ?? [])
      .filter((s) => s.status === "pending" && ownedGroupIds.has(s.group_id))
      .map((s) => ({ groupId: s.group_id, groupName: s.group_name }));
  }, [data, myGroups]);

  // Transcript metadata still loading.
  if (isLoading) {
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6">
        <div className="h-4 w-40 animate-shimmer" />
        <div className="h-8 w-2/3 animate-shimmer" />
        <div className="h-12 w-1/2 animate-shimmer" />
        <div className="h-96 w-full animate-shimmer" />
      </div>
    );
  }

  // Transcript record not found.
  if (!data) {
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12">
        <div className="border border-rule bg-surface py-20 text-center">
          <p className="font-[family-name:var(--font-display)] text-lg text-ink-3 tracking-tight">
            Transcript not found
          </p>
        </div>
      </div>
    );
  }

  const t = data.transcript;
  const projectName = t.project_name ?? "transcript";

  // Content blob loaded, but it is not a SessionDetailPayload — the v2 viewer
  // needs structured turns. Render a graceful fallback instead of crashing.
  if (!contentLoading && content != null && !isSessionDetailPayload(content)) {
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12">
        <div className="border border-rule bg-surface py-16 text-center">
          <FileX2
            size={40}
            strokeWidth={1.5}
            className="mx-auto text-ink-4 mb-3"
            aria-hidden
          />
          <p className="text-ink font-medium">Unsupported transcript format</p>
          <p className="text-ink-3 text-sm mt-1 max-w-sm mx-auto">
            This transcript was published in a legacy format the viewer can no
            longer render. Re-publish it from the CLI to view it here.
          </p>
        </div>
      </div>
    );
  }

  // While content is loading, pass `detail={undefined}` — the v2 viewer renders
  // its own skeleton. Once loaded, hand it the structured payload.
  const detail =
    contentLoading || !isSessionDetailPayload(content)
      ? undefined
      : (content as SessionDetailPayload);

  // The v2 viewer brings its own hero, tabs, canvas and rails — it IS the page.
  return (
    <>
      {pendingReviews.length > 0 && (
        <PendingApprovalBar transcriptId={t.id} reviews={pendingReviews} />
      )}
      <SessionDetailV2
        sessionId={t.local_id || id}
        transcriptId={t.id}
        transcriptVisibility={t.visibility}
        transcriptTitle={t.title}
        transcriptDescription={t.description}
        transcriptOwnerId={data.owner?.id}
        sessionOrigin={t.session_origin}
        projectName={projectName}
        detail={detail}
        error={contentError ? String((contentError as Error).message ?? contentError) : null}
      />
    </>
  );
}
