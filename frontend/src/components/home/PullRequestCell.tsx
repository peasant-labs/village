"use client";

import Link from "next/link";
import type { VillagePullRequestRef, VillagePullRequestsSummary } from "@peasant-labs/schema";
import { OverflowList } from "@/lib/ft-ui";

/** How many pull request numbers a row shows before the rest fold behind `+N`. */
export const PULL_REQUESTS_SHOWN = 2;

const NOUN: [string, string] = ["pull request", "pull requests"];

/** The pull request page of one reference. */
export function pullRequestHref(ref: VillagePullRequestRef): string {
  return `/pulls/${encodeURIComponent(ref.owner)}/${encodeURIComponent(ref.name)}/${ref.number}`;
}

/**
 * The pull requests one transcript is attached to, as a list row states them:
 * `#42, #45 +2`, or `none`.
 *
 * A row carries the COUNT and at most the three newest references. When every
 * reference the count covers is in hand, fairtrade's overflow list folds the
 * extra ones behind `+N` and reveals them in place. When the count is larger
 * than what the row carries, the rest cannot be revealed from here, so `+N` is
 * a link to the transcript page, which lists every one. Either way `N` is the
 * true number of pull requests not shown.
 *
 * Each number is a link to its pull request page, and is named with its
 * repository, since the same number can belong to two repositories.
 */
export default function PullRequestCell({
  transcriptID,
  summary,
}: {
  transcriptID: string;
  summary: VillagePullRequestsSummary | undefined;
}) {
  const count = summary?.count ?? 0;
  const recent = summary?.recent ?? [];
  if (count === 0 || recent.length === 0) {
    return <span className="cmg-none">none</span>;
  }

  const links = recent.map((ref) => (
    <Link
      key={`${ref.owner}/${ref.name}#${ref.number}`}
      href={pullRequestHref(ref)}
      className="cmg-pr-link tnum"
      aria-label={`${ref.owner}/${ref.name} #${ref.number}`}
      title={`${ref.owner}/${ref.name} #${ref.number}`}
    >
      #{ref.number}
    </Link>
  ));

  if (count <= recent.length) {
    return (
      <OverflowList
        items={links}
        limit={PULL_REQUESTS_SHOWN}
        label={NOUN[1]}
        noun={NOUN}
        data-testid="home-pull-requests"
      />
    );
  }

  const shown = links.slice(0, PULL_REQUESTS_SHOWN);
  const more = count - shown.length;
  return (
    <span className="inline-flex flex-wrap items-baseline gap-[var(--sp-2)]" data-testid="home-pull-requests">
      <OverflowList items={shown} limit={PULL_REQUESTS_SHOWN} label={NOUN[1]} noun={NOUN} />
      <Link
        href={`/transcripts/${transcriptID}`}
        className="ovl-more tnum"
        aria-label={`${more} more ${more === 1 ? NOUN[0] : NOUN[1]} on the transcript page`}
      >
        +{more}
      </Link>
    </span>
  );
}
