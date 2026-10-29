"use client";

import Link from "next/link";
import { useState } from "react";
import { Button } from "@/lib/ft-ui";
import { useBatchReview } from "@/lib/queries/groupReview";
import type { ReviewDecision } from "@/lib/review/types";
import { CircleCheck, ClipboardCheck } from "lucide-react";
import { OnEmpty } from "@/components/online/Page";
import { relativeWhen } from "@/lib/adapters/collective";
import { resolveAttribution } from "@/lib/format";
import type { PendingShare } from "@/lib/review/types";
import { AgentCell } from "./CollectiveTranscriptsTable";
import { transcriptHref } from "@/lib/transcriptContext";

/** The curated owner's queue, with direct decisions and a deeper review exit. */
export default function CollectiveWaiting({
  collectiveId,
  pending,
  canReview,
}: {
  collectiveId: string;
  canReview: boolean;
  pending: {
    data: PendingShare[] | undefined;
    isLoading: boolean;
    isError: boolean;
    isFetching: boolean;
    error: Error | null;
    refetch: () => unknown;
  };
}) {
  const reviewHref = `/collectives/${collectiveId}/review`;
  const review = useBatchReview(collectiveId);
  const [stale, setStale] = useState<ReadonlySet<string>>(new Set());
  const touchedID = review.variables?.transcript_ids[0];
  async function decide(transcriptID: string, status: ReviewDecision) {
    if (!canReview || review.isPending || stale.has(transcriptID)) return;
    try {
      const outcome = await review.mutateAsync({ transcript_ids: [transcriptID], status });
      setStale(new Set(outcome.already_decided));
    } catch {
      // Keep the failed row and put the mutation error beside its retry action.
    }
  }

  if (pending.isLoading) {
    return (
      <div className="on-section" aria-busy="true">
        <div className="h-10 w-full bg-surface-hover animate-shimmer" />
        <div className="h-10 w-full bg-surface-hover animate-shimmer" />
      </div>
    );
  }

  if (pending.isError) {
    return (
      <div className="on-notice" data-tone="danger" role="alert">
        <p>could not read what is waiting for approval: {pending.error?.message}</p>
        <button
          type="button"
          className="btn btn-secondary"
          disabled={pending.isFetching}
          onClick={() => void pending.refetch()}
        >
          try again
        </button>
      </div>
    );
  }

  const rows = pending.data ?? [];
  if (rows.length === 0) {
    return (
      <OnEmpty
        icon={<CircleCheck />}
        title="nothing is waiting for approval"
        body="when someone publishes a session here, it waits in this list until you approve it."
        headingLevel={3}
        testId="collective-waiting-empty"
      />
    );
  }

  const now = new Date();
  return (
    <div className="on-section">
      <div className="on-row on-waiting-head">
        <p className="on-muted">
          {rows.length === 1 ? "1 session waits" : `${rows.length} sessions wait`} for your approval before members can
          read {rows.length === 1 ? "it" : "them"}.
        </p>
        <Link href={reviewHref} className="btn btn-secondary">
          <ClipboardCheck size={16} aria-hidden="true" /> review transcripts
        </Link>
      </div>
      <div className="on-tbl">
        <table className="on-ctable on-waiting-table">
          <caption>waiting for approval {rows.length}</caption>
          <thead>
            <tr>
              <th scope="col">transcript</th>
              <th scope="col" className="is-wide">
                contributor
              </th>
              <th scope="col" className="is-wide">
                agent
              </th>
              <th scope="col" className="is-wide">
                review
              </th>
              <th scope="col" className="is-n">
                when
              </th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => {
              const who = resolveAttribution(row.owner);
              const isStale = stale.has(row.transcript_id);
              return (
                <tr key={row.transcript_id} data-testid={`waiting-row-${row.transcript_id}`}>
                  <td className="on-ctable-title">
                    <Link className="is-title" href={transcriptHref(row.transcript_id, { kind: "collective", id: collectiveId })}>
                      {row.title || "untitled"}
                    </Link>
                    {(row.project_name || row.branch) && (
                      <span className="is-sub">{[row.project_name, row.branch].filter(Boolean).join(", ")}</span>
                    )}
                    {/* On a phone the contributor, agent and status columns
                        give way, and the row states them under its title. */}
                    <span className="is-sub on-ctable-facts" data-testid="collective-row-facts">
                      <span>{who.anonymous ? "anon" : `@${who.label}`}</span>
                      <span className="on-agent">
                        <AgentCell provider={row.model_provider} />
                      </span>
                      <span className="on-tag" data-tone={isStale ? undefined : "wait"}>
                        {isStale ? "already decided" : "waiting"}
                      </span>
                    </span>
                  </td>
                  <td className="on-handle-cell is-wide">{who.anonymous ? "anon" : `@${who.label}`}</td>
                  <td className="is-wide">
                    <span className="on-agent">
                      <AgentCell provider={row.model_provider} />
                    </span>
                  </td>
                  <td className="is-wide on-waiting-decision">
                    <div className="on-waiting-actions">
                      <span className="on-tag on-waiting-status" data-tone={isStale ? undefined : "wait"}>{isStale ? "already decided" : "waiting"}</span>
                      {canReview && <>
                        <Button size="sm" variant="secondary" aria-label={`approve ${row.title || "untitled"}`}
                          disabled={review.isPending || pending.isFetching || isStale}
                          loading={review.isPending && touchedID === row.transcript_id && review.variables?.status === "approved"}
                          onClick={() => void decide(row.transcript_id, "approved")}>approve</Button>
                        <Button size="sm" variant="ghost" aria-label={`reject ${row.title || "untitled"}`}
                          disabled={review.isPending || pending.isFetching || isStale}
                          loading={review.isPending && touchedID === row.transcript_id && review.variables?.status === "rejected"}
                          onClick={() => void decide(row.transcript_id, "rejected")}>reject</Button>
                      </>}
                    </div>
                    {review.isError && touchedID === row.transcript_id && <p role="alert" className="on-waiting-error">
                      {review.error instanceof Error ? review.error.message : "the decision could not be applied. try again."}
                    </p>}
                  </td>
                  <td className="is-n on-when">{relativeWhen(row.shared_at, now) ?? ""}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}
