"use client";

import Link from "next/link";
import RetryButton from "@/components/RetryButton";
import { useAuth } from "@/providers/AuthProvider";
import { useGroups } from "@/lib/queries/groups";
import { formatInteger } from "@/lib/ft-ui";
import type { ContributedCollective } from "@/lib/types";

interface Read<T> {
  data: T | undefined;
  isError: boolean;
  isFetching: boolean;
  refetch: () => unknown;
}

function plural(n: number, one: string, many: string): string {
  return `${formatInteger(n)} ${n === 1 ? one : many}`;
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
  const { user, isLoggedIn } = useAuth();
  // This is the membership read the sidebar and home totals already share.
  // A pending contribution alone never grants permission to decide it.
  const memberships = useGroups({ enabled: isLoggedIn });
  const owned = new Set(!memberships.isError
    ? (memberships.data ?? []).filter((c) => c.role === "owner").map((c) => c.id)
    : []);
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
        {waiting.map((c) => {
          const canReview = owned.has(c.id);
          const profile = user?.github_username;
          const actionHref = canReview
            ? `/collectives/${encodeURIComponent(c.id)}/review`
            : profile
              ? `/users/${encodeURIComponent(profile)}?collective=${encodeURIComponent(c.id)}#contributions`
              : `/collectives/${encodeURIComponent(c.id)}`;
          const actionLabel = canReview ? "review submissions" : profile ? "view submissions" : "view collective";
          return (
            <li key={c.id} data-testid="home-waiting-collective" className="flex flex-wrap items-center justify-between gap-4">
              <div>
                <Link href={`/collectives/${encodeURIComponent(c.id)}`} className="on-link">
                  {c.name}
                </Link>
                : <span className="tabular-nums">{plural(c.pending_count, "transcript", "transcripts")}</span> waiting for
                {canReview ? " your approval." : " the collective owner's approval."}
              </div>
              <Link href={actionHref} className="btn btn-secondary" aria-label={`${actionLabel} for ${c.name}`}>
                {actionLabel}
              </Link>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
