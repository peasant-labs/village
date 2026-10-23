"use client";

import { useId, useState } from "react";
import Link from "next/link";
import { ExternalLink, GitPullRequest, LogIn } from "lucide-react";
import { OnEmpty, OnHead, OnPage } from "@/components/online/Page";
import PullStateTag from "@/components/pulls/PullStateTag";
import RetryButton from "@/components/RetryButton";
import { formatRelativeTime } from "@/lib/format";
import { loginHref } from "@/lib/signIn";
import { pullPath, useMyPromptRequests } from "@/lib/queries/pulls";
import { useAuth } from "@/providers/AuthProvider";
import type { VillagePromptRequest } from "@peasant-labs/schema";
import DocsLink from "@/components/online/DocsLink";
import { DOCS_PULL_REQUESTS_URL } from "@/lib/appLinks";
import "@/app/online-pulls.css";

/**
 * `/pulls`: the signed-in person's pull requests that are waiting for their
 * transcripts.
 *
 * The list is `GET /users/me/prompt-requests` as served: the pull requests
 * this person asked to attach to, by commenting `/peasant attach` or by
 * opening them with automatic linking on, while none of their published
 * sessions matches yet. Publishing the session behind one of a pull request's
 * commits attaches it and takes it off this list. The page only orders the
 * rows newest first and groups them by the day they were asked for; which pull
 * requests belong here is the server's answer.
 */

const GITHUB_HOST = "https://github.com";
const DAY_MS = 24 * 60 * 60 * 1000;

export default function PullRequestsPage() {
  const { user, isLoading, isLoggedIn } = useAuth();

  if (isLoading) {
    return (
      <OnPage testId="pulls-pending">
        <OnHead title="pull requests" />
        <Skeleton />
      </OnPage>
    );
  }

  if (!isLoggedIn || user == null) {
    return (
      <OnPage testId="pulls-signed-out">
        <OnHead title="pull requests" />
        <OnEmpty
          icon={<LogIn aria-hidden="true" />}
          title="sign in to see your pull requests"
          body="the pull requests waiting for your transcripts are listed here once you sign in."
          actions={
            <Link href={loginHref("/pulls")} className="btn btn-primary" data-testid="pulls-sign-in">
              sign in
            </Link>
          }
        />
      </OnPage>
    );
  }

  // One mount per account, so one person's list is never shown to the next.
  return <WaitingPullRequests key={user.id} handle={user.github_username} />;
}

function Skeleton() {
  return (
    <div className="on-pr-skeleton" aria-busy="true" data-testid="pulls-skeleton">
      <div className="animate-shimmer" />
      <div className="animate-shimmer" />
    </div>
  );
}

/** One day's pull requests, under the label the day reads as. */
interface DayGroup {
  key: string;
  label: string;
  requests: VillagePromptRequest[];
}

function startOfLocalDay(ms: number): number {
  const day = new Date(ms);
  day.setHours(0, 0, 0, 0);
  return day.getTime();
}

/** "today", "yesterday", or the date itself, in the reader's own time zone. */
function dayLabel(dayStart: number, todayStart: number): string {
  // Rounded, so a day that a clock change made 23 or 25 hours long still counts as one.
  const daysAgo = Math.round((todayStart - dayStart) / DAY_MS);
  if (daysAgo <= 0) return "today";
  if (daysAgo === 1) return "yesterday";
  const day = new Date(dayStart);
  const sameYear = day.getFullYear() === new Date(todayStart).getFullYear();
  return day
    .toLocaleDateString("en-US", {
      month: "short",
      day: "numeric",
      ...(sameYear ? {} : { year: "numeric" }),
    })
    .toLowerCase();
}

/**
 * The requests newest first, grouped by the local day each was asked for. A
 * time that does not parse is kept, under its own group at the end, rather
 * than dropped or dated by a guess.
 */
function groupByDay(requests: VillagePromptRequest[], now: number): DayGroup[] {
  const todayStart = startOfLocalDay(now);
  const dated = requests
    .map((request) => ({ request, at: Date.parse(request.requested_at) }))
    .sort((a, b) => (Number.isNaN(a.at) ? 1 : Number.isNaN(b.at) ? -1 : b.at - a.at));
  const groups: DayGroup[] = [];
  for (const { request, at } of dated) {
    const key = Number.isNaN(at) ? "undated" : String(startOfLocalDay(at));
    let group = groups.find((candidate) => candidate.key === key);
    if (!group) {
      group = {
        key,
        label: Number.isNaN(at) ? "date not known" : dayLabel(Number(key), todayStart),
        requests: [],
      };
      groups.push(group);
    }
    group.requests.push(request);
  }
  return groups;
}

function WaitingPullRequests({ handle }: { handle: string }) {
  const requests = useMyPromptRequests(true, handle);
  // One clock for every row, read once per mount.
  const [now] = useState(() => Date.now());

  const head = <OnHead title="pull requests" />;
  const retry = (
    <RetryButton
      label={requests.isFetching ? "retrying" : "retry"}
      busy={requests.isFetching}
      onRetry={() => requests.refetch()}
      testId="pulls-retry"
    />
  );

  // A failed read is never shown as an empty list: "nothing is waiting" and
  // "we could not find out" are different answers.
  if (requests.data == null && requests.isError) {
    return (
      <OnPage testId="pulls-page">
        {head}
        <div className="on-notice" data-tone="danger" data-testid="pulls-failure">
          <div role="alert" className="on-pr-fail">
            <p className="on-pr-fail-title">your pull requests could not be loaded</p>
            <p>
              The request for the pull requests waiting on you failed, so this is not an empty list.
              Retrying asks again.
            </p>
          </div>
          {retry}
        </div>
      </OnPage>
    );
  }

  if (requests.data == null) {
    return (
      <OnPage testId="pulls-page">
        {head}
        <Skeleton />
      </OnPage>
    );
  }

  if (requests.data.length === 0) {
    return (
      <OnPage testId="pulls-page">
        {head}
        <OnEmpty
          testId="pulls-empty"
          icon={<GitPullRequest aria-hidden="true" />}
          title="nothing is waiting on you"
          actions={<DocsLink href={DOCS_PULL_REQUESTS_URL}>how pull requests get transcripts</DocsLink>}
        />
      </OnPage>
    );
  }

  const groups = groupByDay(requests.data, now);
  return (
    <OnPage testId="pulls-page">
      {head}
      {requests.isError && (
        // A refresh that failed keeps the rows already here, and says so.
        <div className="on-notice" data-tone="danger" data-testid="pulls-stale">
          <p role="alert">This list could not be refreshed, so it shows what was loaded before.</p>
          {retry}
        </div>
      )}
      <div className="on-pr-feed">
        {groups.map((group) => (
          <DaySection key={group.key} group={group} now={now} />
        ))}
      </div>
    </OnPage>
  );
}

function DaySection({ group, now }: { group: DayGroup; now: number }) {
  const labelId = useId();
  return (
    <section className="on-pr-day" aria-labelledby={labelId} data-testid="pulls-day">
      <h2 id={labelId}>{group.label}</h2>
      <ul className="on-pr-list">
        {group.requests.map((request) => (
          <li key={`${request.owner}/${request.name}#${request.number}`}>
            <RequestCard request={request} now={now} />
          </li>
        ))}
      </ul>
    </section>
  );
}

/**
 * One pull request waiting on you. The wire carries its repository, number,
 * state and when it was asked for, but no title, so the card is named by its
 * repository and number, and links to its prompts page.
 */
function RequestCard({ request, now }: { request: VillagePromptRequest; now: number }) {
  const titleId = useId();
  const repo = `${request.owner}/${request.name}`;
  const asked = formatRelativeTime(request.requested_at, now);
  // A pull request from a fork names the fork its commits come from.
  const fork =
    request.head_remote && request.head_remote.toLowerCase() !== request.remote.toLowerCase()
      ? request.head_remote
      : null;
  const githubHref = `${GITHUB_HOST}/${encodeURIComponent(request.owner)}/${encodeURIComponent(request.name)}/pull/${request.number}`;
  return (
    <article className="on-prcard" aria-labelledby={titleId} data-testid="pulls-request">
      <header>
        <div className="on-pr-cardhead">
          <h3 id={titleId} className="on-pr-title">
            <GitPullRequest aria-hidden="true" />
            <Link className="on-pr-open" href={pullPath(request.owner, request.name, request.number)}>
              {repo} <span className="on-pr-num">#{request.number}</span>
            </Link>
          </h3>
          <PullStateTag state={request.state} />
        </div>
        <div className="on-pr-cardfoot">
          <p className="on-prcard-meta">
            {asked && <span>asked {asked}</span>}{" "}
            {fork && <span className="on-pr-repo-name">from {fork}</span>}
          </p>
          <a className="on-pr-gh" href={githubHref} target="_blank" rel="noreferrer">
            view on github
            <ExternalLink aria-hidden="true" />
          </a>
        </div>
      </header>
    </article>
  );
}
