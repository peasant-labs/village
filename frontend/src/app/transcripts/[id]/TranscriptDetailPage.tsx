"use client";

import { use, useEffect, useMemo, useState } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useTranscript, useTranscriptContent } from "@/lib/queries/transcripts";
import { useGroup, useGroups } from "@/lib/queries/groups";
import { CONTEXT_PARAM, inferTranscriptContext, parseTranscriptContext, withTranscriptContext } from "@/lib/transcriptContext";
import { SessionDetailV2 } from "@/components/session-detail/v2/SessionDetailV2";
import PendingApprovalBar, {
  type PendingReview,
} from "@/components/transcript/PendingApprovalBar";
import { isSessionDetailPayload } from "@/lib/sessionDetailPayload";
import type { SessionDetailPayload } from "@/types/messages";
import Link from "next/link";
import { FileX2, SearchX } from "lucide-react";
import { hasAuthTokenCookie, isApiErrorStatus } from "@/lib/api";
import { useAuth } from "@/providers/AuthProvider";
import { useDeclareSignedOutReadable } from "@/components/shell/signedOutDeclaration";
import { loginHref } from "@/lib/signIn";
import RequestFailureState from "@/components/RequestFailureState";
import { OnEmpty, OnHead, OnPage } from "@/components/online/Page";

export default function TranscriptDetailPage({
  params,
  openSignedOut = false,
}: {
  params: Promise<{ id: string }>;
  /** The server read this transcript as public, so a visitor without a session may stay. */
  openSignedOut?: boolean;
}) {
  const { id } = use(params);
  useDeclareSignedOutReadable(openSignedOut);
  const metadata = useTranscript(id);
  const { data, isLoading, error: metadataError } = metadata;
  const [retryingMetadata, setRetryingMetadata] = useState(false);
  const { data: contentRead, isLoading: contentLoading, error: contentError } =
    useTranscriptContent(id, { knownHarness: data?.transcript.model_provider, enabled: !!data });
  const content = contentRead?.content;
  const { isLoading: sessionLoading, isLoggedIn } = useAuth();
  // The person's own collectives need a session; nobody signed out has any.
  // While the session read is in flight, a browser holding a session cookie
  // asks at once, as before, so a signed-in page waits on nothing new.
  const { data: myGroups, isError: myGroupsFailed } = useGroups({
    enabled: isLoggedIn || (sessionLoading && hasAuthTokenCookie()),
  });
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();

  // Where the transcript was opened from. A link from a list carries it; one
  // with none is given the one collective of the person's that holds the
  // transcript, or home, written into the address so the sidebar follows and
  // Back and Forward keep it.
  const context = parseTranscriptContext(searchParams.get(CONTEXT_PARAM));
  const inferred = useMemo(() => {
    if (context || !data || !isLoggedIn) return null;
    if (!myGroups && !myGroupsFailed) return null;
    return inferTranscriptContext(data.enriched_shares ?? [], new Set((myGroups ?? []).map((g) => g.id)));
  }, [context, data, isLoggedIn, myGroups, myGroupsFailed]);
  useEffect(() => {
    if (!inferred) return;
    router.replace(`${pathname}?${withTranscriptContext(searchParams.toString(), inferred)}`, { scroll: false });
  }, [inferred, pathname, router, searchParams]);
  const shown = context ?? inferred;

  // The collective's name for the trail: from the person's own collectives,
  // from the transcript's shares, or from the collective itself when the
  // person reached it without belonging to it.
  const collectiveId = shown?.kind === "collective" ? shown.id : "";
  const knownName =
    myGroups?.find((g) => g.id === collectiveId)?.name ??
    data?.enriched_shares?.find((s) => s.group_id === collectiveId)?.group_name;
  const { data: collective } = useGroup(collectiveId && !knownName && data ? collectiveId : "");
  const collectiveName = knownName ?? collective?.group.name ?? null;

  const pendingReviews = useMemo<PendingReview[]>(() => {
    if (!data || !myGroups) return [];
    const ownedGroupIds = new Set(
      myGroups.filter((g) => g.role === "owner").map((g) => g.id)
    );
    return (data.enriched_shares ?? [])
      .filter((s) => s.status === "pending" && ownedGroupIds.has(s.group_id))
      .map((s) => ({ groupId: s.group_id, groupName: s.group_name }));
  }, [data, myGroups]);

  if (!data && ((metadataError && !isApiErrorStatus(metadataError, 404)) || retryingMetadata)) {
    return (
      <OnPage>
        <RequestFailureState
          headingLevel={1}
          title="this transcript could not load"
          message="the transcript could not be read. retry to load it before continuing."
          retryLabel="retry transcript"
          retryDisabled={metadata.isFetching || retryingMetadata}
          onRetry={() => {
            setRetryingMetadata(true);
            void metadata.refetch().finally(() => setRetryingMetadata(false));
          }}
        />
      </OnPage>
    );
  }

  // Transcript metadata still loading.
  // The title's place shimmers until the read names it.
  if (isLoading) {
    return (
      <OnPage>
        <OnHead title={null} loadingLabel="loading the transcript" />
        <div className="flex flex-col gap-6" aria-hidden="true">
          <div className="h-12 w-1/2 animate-shimmer" />
          <div className="h-96 w-full animate-shimmer" />
        </div>
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
          headingLevel={1}
          body={signedOut ? "if it was shared with you, sign in to read it." : undefined}
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
          headingLevel={1}
          body="this transcript was published in a format the viewer can no longer render. publish it again from peasant to read it here."
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
      <SessionDetailV2
        key={t.id}
        sessionId={t.local_id || id}
        transcriptId={t.id}
        transcriptVisibility={t.visibility}
        transcriptTitle={t.title}
        transcriptDescription={t.description}
        owner={data.owner}
        context={shown}
        collectiveName={collectiveName}
        projectName={projectName}
        projectHash={t.project_hash}
        detail={detail}
        relationshipNavigation={data.relationshipNavigation}
        transcriptContentHash={t.content_hash}
        integrity={data.integrity}
        renderedContentHash={contentRead?.contentHash ?? null}
        error={contentError ? String((contentError as Error).message ?? contentError) : null}
        beforeHeader={pendingReviews.length > 0 ? <PendingApprovalBar transcriptId={t.id} reviews={pendingReviews} /> : undefined}
      />
    </>
  );
}
