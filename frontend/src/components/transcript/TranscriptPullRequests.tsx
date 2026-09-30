"use client";

import { useEffect, useId, useRef, useState } from "react";
import Link from "next/link";
import type { VillageTranscriptPullRequest } from "@peasant-labs/schema";

/** How many pull requests show before `show all N`. */
export const PULL_REQUESTS_SHOWN = 3;

function pullRequestHref(pr: VillageTranscriptPullRequest): string {
  return `/pulls/${encodeURIComponent(pr.owner)}/${encodeURIComponent(pr.name)}/${pr.number}`;
}

interface TranscriptPullRequestsProps {
  pullRequests: VillageTranscriptPullRequest[];
  /** The read failed: say so and offer a retry instead of showing nothing. */
  failed?: boolean;
  onRetry?: () => void;
}

/**
 * The pull requests this transcript is bound to, under the header's meta row,
 * as the fairtrade in-use demo lays them out: a titled list with the first few
 * rows and a `show all N` control for the rest. A transcript can be bound to
 * many pull requests, so the list stays short until the reader asks for all of
 * it. Each row opens village's page for that pull request.
 */
export default function TranscriptPullRequests({ pullRequests, failed = false, onRetry }: TranscriptPullRequestsProps) {
  const [all, setAll] = useState(false);
  const titleId = useId();
  const listRef = useRef<HTMLUListElement>(null);
  const total = pullRequests.length;
  // `show all` removes itself, so focus moves to the first row it revealed
  // rather than falling back to the page.
  useEffect(() => {
    if (!all) return;
    listRef.current?.children[PULL_REQUESTS_SHOWN]?.querySelector<HTMLElement>("a")?.focus();
  }, [all]);
  const shown = all ? pullRequests : pullRequests.slice(0, PULL_REQUESTS_SHOWN);

  return (
    <section className="cmg-transcript-prs" aria-labelledby={titleId} data-testid="transcript-pull-requests">
      <p className="cmg-section-title" id={titleId}>
        pull requests {!failed && <span className="tnum cmg-count">{total}</span>}
      </p>
      {failed ? (
        <p className="cmg-none" role="alert">
          the pull requests could not load.{" "}
          {onRetry && (
            <button type="button" className="btn btn-ghost btn-sm" onClick={onRetry}>
              retry
            </button>
          )}
        </p>
      ) : (
        <>
          <ul className="cmg-pr-list" ref={listRef}>
            {shown.map((pr) => (
              <li key={`${pr.owner}/${pr.name}#${pr.number}`} data-testid="transcript-pull-request">
                <Link className="cmg-pr-link mono" href={pullRequestHref(pr)}>
                  {pr.owner}/{pr.name} <span className="tnum">#{pr.number}</span>
                </Link>
                <span className="cmg-none">
                  {pr.state}
                  {pr.title ? ` · ${pr.title}` : ""}
                </span>
              </li>
            ))}
          </ul>
          {!all && total > PULL_REQUESTS_SHOWN && (
            <button type="button" className="btn btn-ghost btn-sm" onClick={() => setAll(true)}>
              show all <span className="tnum">{total}</span>
            </button>
          )}
        </>
      )}
    </section>
  );
}
