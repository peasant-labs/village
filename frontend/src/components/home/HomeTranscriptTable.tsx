"use client";

import Link from "next/link";
import type { ReactNode } from "react";
import { DataTable, ProviderIcon } from "@/lib/ft-ui";
import { isHarness } from "@/lib/harness";
import ChildSessionDisclosure from "@/components/transcript/ChildSessionDisclosure";
import type { TranscriptRowFact } from "@/components/transcript/TranscriptList";
import PullRequestCell from "@/components/home/PullRequestCell";
import { formatRelativeTime } from "@/lib/format";
import type { TranscriptListItem, TranscriptRow } from "@/lib/types";

/**
 * The facts a session another session started states when its chip is opened.
 * No provider: the home list names what a session was about and where, not
 * which tool recorded it.
 */
export const HOME_STARTED_SESSION_FACTS: readonly TranscriptRowFact[] = [
  "branch",
  "date",
  "turns",
];

/**
 * Who can read one of the viewer's own transcripts, as the list row states it.
 *
 * A list row carries the collectives that have APPROVED it, so a submission a
 * curated collective has not reviewed yet is not named here; the rail's
 * waiting section says those exist. With no collective, the transcript's own
 * visibility states the public-link audience. No approved collective does not
 * imply author-only access: collective owners may still review submissions.
 */
function sharedWith(item: TranscriptListItem): ReactNode {
  const names = [...new Set((item.shares ?? []).map((share) => share.group_name))];
  const audience = item.transcript.visibility === "public"
    ? ["anyone with the link", ...names]
    : item.transcript.visibility === "shared" ? names : [];
  if (audience.length > 0) {
    return (
      <span className="cmg-shared">
        {audience.map((name) => <span key={name}>{name}</span>)}
      </span>
    );
  }
  return <span className="cmg-none">not shared with collective members</span>;
}

interface HomeTranscriptTableProps {
  /** The rows the table lists, in order. */
  rows: TranscriptListItem[];
  /** The sessions each listed row started, keyed by that row's id. */
  childSessions: Map<string, TranscriptListItem[]>;
  /** The signed-in person's handle, for the links to their own project pages. */
  viewerUsername: string;
  /** The saved helper groups the grouped read attached to a row, if any. */
  helperGroupSlot: (item: TranscriptRow) => ReactNode;
  /** One clock for every row's age, so two rows are never aged at two instants. */
  now: number;
}

/**
 * The signed-in person's transcripts as fairtrade's data table: title, who can
 * read it, the pull requests it is attached to, and when it was published.
 *
 * A session another session started is not a row of its own when the session
 * that started it is listed: it is inside that row's chip, as on every other
 * list of a person's sessions. The saved helper groups hang off their row the
 * same way.
 */
export default function HomeTranscriptTable({
  rows,
  childSessions,
  viewerUsername,
  helperGroupSlot,
  now,
}: HomeTranscriptTableProps) {
  const columns = [
    {
      key: "title",
      label: "title",
      render: (_: unknown, item: TranscriptListItem) => {
        const t = item.transcript;
        const started = childSessions.get(t.id);
        const branch = t.git_branch?.trim() ?? "";
        // A project is routed on its hash. A row that arrived without one is a
        // server contract violation: it is still listed, and its project is
        // named but not linked, never folded into a made-up project.
        //
        // The line breaks between the project and the branch, never inside a
        // name: "fix/flaky-" over "ingest" reads as two things.
        const project = t.project_hash ? (
          <Link
            href={`/users/${encodeURIComponent(viewerUsername)}/projects/${t.project_hash}`}
            className="whitespace-nowrap hover:text-ink focus-mono"
          >
            {t.project_display_name}
          </Link>
        ) : (
          <span className="whitespace-nowrap">{t.project_display_name}</span>
        );
        return (
          <div className="cmg-cell-stack" data-testid="home-transcript-row" data-transcript-id={t.id}>
            <span className="iu-session">
              {isHarness(t.model_provider) && <ProviderIcon harness={t.model_provider} accent label />}
              <span className="iu-session-text">
                <Link href={`/transcripts/${t.id}`} className="iu-session-title focus-mono">
                  {t.title || "untitled"}
                </Link>
                <span className="iu-session-sub">
                  {project}
                  {branch !== "" && (
                    <>
                      {" · "}
                      <span className="whitespace-nowrap">{branch}</span>
                    </>
                  )}
                </span>
              </span>
            </span>
            {helperGroupSlot(item)}
            {started !== undefined && started.length > 0 && (
              <ChildSessionDisclosure
                parentTranscriptID={t.id}
                childSessions={started}
                hideOwner
                facts={HOME_STARTED_SESSION_FACTS}
                helperGroupSlot={helperGroupSlot}
              />
            )}
          </div>
        );
      },
    },
    {
      key: "sharedWith",
      label: "shared with",
      width: "13rem",
      render: (_: unknown, item: TranscriptListItem) => sharedWith(item),
    },
    {
      key: "pullRequests",
      label: "pull requests",
      width: "10rem",
      render: (_: unknown, item: TranscriptListItem) => (
        <PullRequestCell transcriptID={item.transcript.id} summary={item.pull_requests} />
      ),
    },
    {
      key: "when",
      label: "when",
      width: "7rem",
      render: (_: unknown, item: TranscriptListItem) => (
        <time dateTime={item.transcript.published_at} className="iu-session-sub tnum">
          {formatRelativeTime(item.transcript.published_at, now)}
        </time>
      ),
    },
  ];

  return (
    <DataTable
      caption="transcripts you published"
      columns={columns}
      rows={rows}
      rowKey={(item: TranscriptListItem) => item.transcript.id}
    />
  );
}
