"use client";

import Link from "next/link";
import { CircleCheck, ClipboardCheck } from "lucide-react";
import { OnEmpty } from "@/components/online/Page";
import { relativeWhen } from "@/lib/adapters/collective";
import { resolveAttribution } from "@/lib/format";
import type { PendingShare } from "@/lib/review/types";
import { AgentCell } from "./CollectiveTranscriptsTable";

/**
 * What waits for the owner's approval in a curated collective, read-only: the
 * same queue the review page decides, read under the same key, so a decision
 * there refreshes this list. Deciding happens on the review page, which this
 * panel links to; each title opens the transcript, where the owner can also
 * approve or reject it.
 */
export default function CollectiveWaiting({
  collectiveId,
  pending,
  viewerId,
}: {
  collectiveId: string;
  pending: {
    data: PendingShare[] | undefined;
    isLoading: boolean;
    isError: boolean;
    isFetching: boolean;
    error: Error | null;
    refetch: () => unknown;
  };
  viewerId: string | undefined;
}) {
  const reviewHref = `/collectives/${collectiveId}/review`;

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
        <table className="on-ctable">
          <caption>waiting for approval {rows.length}</caption>
          <thead>
            <tr>
              <th scope="col">transcript</th>
              <th scope="col">contributor</th>
              <th scope="col">agent</th>
              <th scope="col">status</th>
              <th scope="col" className="is-n">
                when
              </th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => {
              const who = resolveAttribution(
                { id: row.owner_id, github_username: row.owner_username, is_discoverable: row.owner_is_discoverable },
                viewerId,
                true,
              );
              return (
                <tr key={row.transcript_id}>
                  <td className="on-ctable-title">
                    <Link className="is-title" href={`/transcripts/${row.transcript_id}`}>
                      {row.title || "untitled"}
                    </Link>
                    {(row.project_name || row.branch) && (
                      <span className="is-sub">{[row.project_name, row.branch].filter(Boolean).join(", ")}</span>
                    )}
                  </td>
                  <td className="on-handle-cell">{who.anonymous ? "anon" : `@${who.label}`}</td>
                  <td>
                    <span className="on-agent">
                      <AgentCell provider={row.model_provider} />
                    </span>
                  </td>
                  <td>
                    <span className="on-tag" data-tone="wait">
                      waiting
                    </span>
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
