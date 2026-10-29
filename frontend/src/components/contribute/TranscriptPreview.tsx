"use client";

import { useState } from "react";
import RequestFailureState from "@/components/RequestFailureState";
import { isApiErrorStatus } from "@/lib/api";
import { useTranscript, useTranscriptContent } from "@/lib/queries/transcripts";
import { SessionDetailV2 } from "@/components/session-detail/v2/SessionDetailV2";
import { isSessionDetailPayload } from "@/lib/sessionDetailPayload";
import type { SessionDetailPayload } from "@/types/messages";
import { FileText, FileX2 } from "lucide-react";

interface TranscriptPreviewProps {
  /** The clicked session's transcript id, or `null` before any row has been
   *  clicked. `null` (never an empty string) is the one "nothing selected"
   *  value the empty state gates on. */
  transcriptId: string | null;
}

/**
 * The contribute page's right-hand preview column. Fetches the same two
 * queries the full `/transcripts/{id}` route does (`useTranscript` +
 * `useTranscriptContent`), narrows the content blob with the SAME guard that
 * route uses ({@link isSessionDetailPayload}), and renders it through
 * `SessionDetailV2`'s `variant="preview"` — turns + tool calls, read-only,
 * every owner action and the trajectory graph hidden.
 */
export default function TranscriptPreview({ transcriptId }: TranscriptPreviewProps) {
  const metadata = useTranscript(transcriptId ?? "");
  const { data, isLoading } = metadata;
  const [retryingId, setRetryingId] = useState<string | null>(null);
  const retrying = transcriptId != null && retryingId === transcriptId;
  const {
    data: content,
    isLoading: contentLoading,
    error: contentError,
  } = useTranscriptContent(transcriptId ?? "", { knownHarness: data?.transcript.model_provider, enabled: !!data });

  if (transcriptId == null) {
    return (
      <div
        className="on-preview-state"
        data-testid="transcript-preview-empty"
      >
        <FileText aria-hidden="true" />
        <h2>preview a transcript</h2>
        <p>select a session to preview it</p>
        <p className="on-muted">previewing a session does not select it. use its checkbox to add it to the selection.</p>
      </div>
    );
  }

  if (!isApiErrorStatus(metadata.error, 404) && (metadata.isError || retrying)) {
    return (
      <div className="on-preview-state">
        <RequestFailureState
          title="the transcript preview could not load"
          message={metadata.error instanceof Error ? metadata.error.message : "the transcript details could not be read. retry to load the preview."}
          retryLabel="retry transcript preview"
          retryDisabled={metadata.isFetching || retrying}
          onRetry={() => {
            const id = transcriptId;
            setRetryingId(id);
            void metadata.refetch().finally(() => setRetryingId((current) => current === id ? null : current));
          }}
        />
      </div>
    );
  }

  if (isLoading) {
    return (
      <div className="h-full flex flex-col gap-3 p-6">
        <div className="h-4 w-1/3 animate-shimmer" />
        <div className="h-8 w-2/3 animate-shimmer" />
        <div className="h-64 w-full animate-shimmer" />
      </div>
    );
  }

  if (!data) {
    return (
      <div className="on-preview-state">
        <p className="text-[length:var(--fs-body)] text-ink-3">transcript not found</p>
      </div>
    );
  }

  if (!contentLoading && content != null && !isSessionDetailPayload(content)) {
    return (
      <div className="h-full flex flex-col items-center justify-center gap-2 border border-rule bg-surface text-center px-6">
        <FileX2 size={28} strokeWidth={1.5} className="text-ink-4" aria-hidden />
        <p className="text-sm text-ink">unsupported transcript format</p>
        <p className="text-[length:var(--fs-body)] text-ink-3 max-w-xs">
          this transcript was published in a legacy format the preview cannot render.
        </p>
      </div>
    );
  }

  const t = data.transcript;
  const detail =
    contentLoading || !isSessionDetailPayload(content) ? undefined : (content as SessionDetailPayload);

  return (
    <SessionDetailV2
      variant="preview"
      sessionId={t.local_id || transcriptId}
      transcriptId={t.id}
      transcriptVisibility={t.visibility}
      transcriptTitle={t.title}
      transcriptDescription={t.description}
      owner={data.owner}
      projectName={t.project_display_name}
      projectHash={t.project_hash}
      detail={detail}
      error={contentError ? String((contentError as Error).message ?? contentError) : null}
    />
  );
}
