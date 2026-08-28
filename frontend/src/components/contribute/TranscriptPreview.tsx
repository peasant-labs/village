"use client";

import { useTranscript, useTranscriptContent } from "@/lib/queries/transcripts";
import { SessionDetailV2 } from "@/components/session-detail/v2/SessionDetailV2";
import { isSessionDetailPayload } from "@/lib/sessionDetailPayload";
import type { SessionDetailPayload } from "@/types/messages";
import { FileX2 } from "lucide-react";

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
  const { data, isLoading } = useTranscript(transcriptId ?? "");
  const {
    data: content,
    isLoading: contentLoading,
    error: contentError,
  } = useTranscriptContent(transcriptId ?? "");

  if (transcriptId == null) {
    return (
      <div
        className="h-full flex items-center justify-center border border-rule bg-surface"
        data-testid="transcript-preview-empty"
      >
        <p className="text-[13px] text-ink-3">select a session to preview it</p>
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
      <div className="h-full flex items-center justify-center border border-rule bg-surface">
        <p className="text-[13px] text-ink-3">transcript not found</p>
      </div>
    );
  }

  if (!contentLoading && content != null && !isSessionDetailPayload(content)) {
    return (
      <div className="h-full flex flex-col items-center justify-center gap-2 border border-rule bg-surface text-center px-6">
        <FileX2 size={28} strokeWidth={1.5} className="text-ink-4" aria-hidden />
        <p className="text-sm text-ink">unsupported transcript format</p>
        <p className="text-[12px] text-ink-3 max-w-xs">
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
      transcriptOwnerId={data.owner?.id}
      sessionOrigin={t.session_origin}
      projectName={t.project_display_name}
      projectHash={t.project_hash}
      ownerUsername={data.owner?.github_username}
      detail={detail}
      error={contentError ? String((contentError as Error).message ?? contentError) : null}
    />
  );
}
