"use client";

import Link from "next/link";
import RetryButton from "@/components/RetryButton";
import type { ContributedCollective } from "@/lib/types";

interface Read<T> {
  data: T | undefined;
  isError: boolean;
  isFetching: boolean;
  refetch: () => unknown;
}

function plural(n: number, one: string, many: string): string {
  return `${n.toLocaleString("en-US")} ${n === 1 ? one : many}`;
}

/**
 * The collectives still reviewing something the viewer published to them.
 *
 * A curated collective's members read a transcript only once its owner
 * approves it, so the list row's readers cell does not name that collective
 * yet; this line says it is on its way. Its read is independent of the list, so
 * a failure here is said here, with its own retry, and never takes the list.
 * Nothing waiting draws nothing.
 */
export default function HomeWaiting({ contributions }: { contributions: Read<ContributedCollective[]> }) {
  const waiting = (contributions.data ?? []).filter((c) => c.pending_count > 0);

  if (contributions.isError && contributions.data == null) {
    return (
      <div className="on-notice" data-testid="home-waiting-failed">
        <p role="alert">peasant could not check whether anything you published is waiting for approval.</p>
        <RetryButton
          label={contributions.isFetching ? "retrying" : "retry"}
          busy={contributions.isFetching}
          onRetry={() => contributions.refetch()}
          testId="home-waiting-retry"
        />
      </div>
    );
  }
  if (waiting.length === 0) return null;

  return (
    <section className="on-notice on-waiting" aria-labelledby="home-waiting-title" data-testid="home-waiting">
      <h2 className="on-label" id="home-waiting-title">
        waiting for approval
      </h2>
      <ul>
        {waiting.map((c) => (
          <li key={c.id} data-testid="home-waiting-collective">
            <Link href={`/collectives/${c.id}`} className="on-link">
              {c.name}
            </Link>
            : <span className="tabular-nums">{plural(c.pending_count, "transcript", "transcripts")}</span> waiting. its
            owner approves each transcript before members can read it.
          </li>
        ))}
      </ul>
    </section>
  );
}
