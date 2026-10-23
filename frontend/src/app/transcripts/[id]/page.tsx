"use client";

import { use, useMemo } from "react";
import { useTranscript, useTranscriptContent } from "@/lib/queries/transcripts";
import { useGroups } from "@/lib/queries/groups";
import { SessionDetailV2 } from "@/components/session-detail/v2/SessionDetailV2";
import PendingApprovalBar, {
  type PendingReview,
} from "@/components/transcript/PendingApprovalBar";
import { isSessionDetailPayload } from "@/lib/sessionDetailPayload";
import type { SessionDetailPayload } from "@/types/messages";
import Link from "next/link";
import { Clock, FileX2, SearchX } from "lucide-react";
import { isApiErrorStatus } from "@/lib/api";
import { useAuth } from "@/providers/AuthProvider";
import { loginHref } from "@/lib/signIn";
import { OnEmpty, OnPage } from "@/components/online/Page";

export default function TranscriptDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const { data, isLoading, error } = useTranscript(id);
  const { data: content, isLoading: contentLoading, error: contentError } =
    useTranscriptContent(id, { knownHarness: data?.transcript.model_provider, enabled: !!data });
  const { data: myGroups } = useGroups();
  const { isLoading: sessionLoading, isLoggedIn } = useAuth();

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

  // The server refused this read. A rate-limited access check says so: rendering
  // "not found" for it would pretend the reader has no access, which is the one
  // thing the throttle's answer exists to avoid.
  if (isApiErrorStatus(error, 429)) {
    return (
      <OnPage>
        <OnEmpty
          icon={<Clock aria-hidden="true" />}
          title="too many access checks just now"
          body="This transcript's repository is asked whether you may read it, and that has been asked too often in the last few minutes. Try again shortly."
        />
      </OnPage>
    );
  }

  // Transcript record not found. A transcript the reader may not open answers
  // the same way, so somebody who is not signed in is offered the one thing
  // that could change the answer, and returns here afterwards.
  if (!data) {
    const signedOut = !sessionLoading && !isLoggedIn;
    return (
      <OnPage>
        <OnEmpty
          icon={<SearchX aria-hidden="true" />}
          title="transcript not found"
          body={signedOut ? "If it was shared with you, sign in to read it." : undefined}
          testId="transcript-not-found"
          actions={
            signedOut ? (
              <Link href={loginHref(`/transcripts/${id}`)} className="btn btn-primary">
                sign in
              </Link>
            ) : undefined
          }
        />
      </OnPage>
    );
  }

  const t = data.transcript;
  // The one server-resolved project name every surface renders — never the
  // raw `project_name` column.
  const projectName = t.project_display_name;

  // Content blob loaded, but it is not a SessionDetailPayload — the v2 viewer
  // needs structured turns. Render a graceful fallback instead of crashing.
  if (!contentLoading && content != null && !isSessionDetailPayload(content)) {
    return (
      <OnPage>
        <OnEmpty
          icon={<FileX2 aria-hidden="true" />}
          title="unsupported transcript format"
          body="This transcript was published in a format the viewer can no longer render. Publish it again from peasant to read it here."
        />
      </OnPage>
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
        key={t.id}
        sessionId={t.local_id || id}
        transcriptId={t.id}
        transcriptVisibility={t.visibility}
        transcriptTitle={t.title}
        transcriptDescription={t.description}
        transcriptOwnerId={data.owner?.id}
        sessionOrigin={t.session_origin}
        projectName={projectName}
        projectHash={t.project_hash}
        ownerUsername={data.owner?.github_username}
        detail={detail}
        relationshipNavigation={data.relationshipNavigation}
        transcriptContentHash={t.content_hash}
        error={contentError ? String((contentError as Error).message ?? contentError) : null}
      />
    </>
  );
}
