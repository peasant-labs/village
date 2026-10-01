"use client";

import Link from "next/link";
import { useId } from "react";
import RetryButton from "@/components/RetryButton";
import type { ContributedCollective, Group } from "@/lib/types";

/** How many of the viewer's collectives the rail names before `view all`. */
export const RAIL_COLLECTIVES_SHOWN = 5;

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
 * The home page's rail: the collectives the viewer belongs to, and the
 * collectives still reviewing something the viewer published to them.
 *
 * Both reads are the viewer's own and independent of the transcript list, so a
 * failure here is said here, with its own retry, and never takes the list.
 * The waiting section is only drawn when something is waiting.
 */
export default function HomeRail({
  collectives,
  contributions,
}: {
  collectives: Read<Group[]>;
  contributions: Read<ContributedCollective[]>;
}) {
  const collectivesTitle = useId();
  const waitingTitle = useId();
  const mine = collectives.data ?? [];
  const waiting = (contributions.data ?? []).filter((c) => c.pending_count > 0);

  return (
    <aside className="cmg-rail" aria-label="your collectives and what is waiting" data-testid="home-rail">
      <section className="cmg-rail-box" aria-labelledby={collectivesTitle} data-testid="home-rail-collectives">
        <div className="cmg-rail-head">
          <h2 className="cmg-rail-title" id={collectivesTitle}>
            your collectives
          </h2>
          <Link href="/groups" className="btn btn-ghost btn-sm">
            view all
          </Link>
        </div>
        {collectives.isError && collectives.data == null ? (
          <div className="flex flex-col items-start gap-[var(--sp-2)]">
            <p role="alert" className="cmg-note">
              your collectives could not be loaded.
            </p>
            <RetryButton
              label={collectives.isFetching ? "retrying" : "retry"}
              busy={collectives.isFetching}
              onRetry={() => collectives.refetch()}
              testId="home-rail-collectives-retry"
            />
          </div>
        ) : collectives.data == null ? (
          <div className="h-16 animate-shimmer" aria-hidden="true" />
        ) : mine.length === 0 ? (
          <p className="cmg-note">
            you are not in a collective yet. a collective reads the transcripts its members
            publish to it.
          </p>
        ) : (
          <ul className="cmg-rail-list">
            {mine.slice(0, RAIL_COLLECTIVES_SHOWN).map((c) => (
              <li key={c.id} data-testid="home-rail-collective">
                <Link href={`/groups/${c.id}`} className="cmg-rail-link">
                  {c.name}
                </Link>
                <span className="cmg-rail-meta">
                  {c.role}
                  {c.member_count != null && (
                    <>
                      {" · "}
                      <span className="tnum">{plural(c.member_count, "member", "members")}</span>
                    </>
                  )}
                </span>
              </li>
            ))}
          </ul>
        )}
      </section>

      {contributions.isError && contributions.data == null ? (
        <section className="cmg-rail-box" data-testid="home-rail-waiting-failed">
          <p role="alert" className="cmg-note">
            village could not check whether anything you published is waiting for approval.
          </p>
          <RetryButton
            label={contributions.isFetching ? "retrying" : "retry"}
            busy={contributions.isFetching}
            onRetry={() => contributions.refetch()}
            testId="home-rail-waiting-retry"
          />
        </section>
      ) : waiting.length > 0 ? (
        <section className="cmg-rail-box" aria-labelledby={waitingTitle} data-testid="home-rail-waiting">
          <h2 className="cmg-rail-title" id={waitingTitle}>
            waiting for approval
          </h2>
          <ul className="cmg-rail-list">
            {waiting.map((c) => (
              <li key={c.id} data-testid="home-rail-waiting-collective">
                <Link href={`/groups/${c.id}`} className="cmg-rail-link">
                  {c.name}
                </Link>
                <span className="cmg-rail-meta">
                  <span className="tnum">{plural(c.pending_count, "transcript", "transcripts")}</span>{" "}
                  waiting. the owner of {c.name} approves each transcript before members can read
                  it.
                </span>
              </li>
            ))}
          </ul>
        </section>
      ) : null}
    </aside>
  );
}
