"use client";

import Link from "next/link";
import type { ReactNode } from "react";
import { Eye } from "lucide-react";
import { ProviderName } from "@/lib/ft-ui";
import { isHarness } from "@/lib/harness";
import ChildSessionDisclosure from "@/components/transcript/ChildSessionDisclosure";
import type { TranscriptRowFact } from "@/components/transcript/TranscriptList";
import PullRequestCell from "@/components/home/PullRequestCell";
import { formatRelativeTime } from "@/lib/format";
import {
  HOME_COLUMN_LABELS,
  HOME_COLUMNS,
  NO_READERS,
  agentLabel,
  readersOf,
  type HomeColumn,
  type HomeGroup,
} from "@/lib/homeView";
import type { TranscriptListItem, TranscriptRow } from "@/lib/types";
import { transcriptHref } from "@/lib/transcriptContext";

/**
 * The facts a session another session started states when its chip is opened.
 * No provider: the home list names what a session was about and where, not
 * which tool recorded it.
 */
export const HOME_STARTED_SESSION_FACTS: readonly TranscriptRowFact[] = ["branch", "date", "turns"];

const NUMERIC: ReadonlySet<HomeColumn> = new Set(["turns", "when"]);

function plural(n: number, one: string, many: string): string {
  return `${n.toLocaleString("en-US")} ${n === 1 ? one : many}`;
}

interface HomeTableProps {
  groups: HomeGroup[];
  /** Whether the rows are grouped (a group row heads each group). */
  grouped: boolean;
  /** The groups that are open. Ungrouped rows always show. */
  isOpen: (groupId: string) => boolean;
  onToggle: (groupId: string) => void;
  columns: Record<HomeColumn, boolean>;
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
 * The signed-in person's transcripts: one row each, optionally grouped, with the
 * columns the person chose. A session another session started is not a row of
 * its own when the session that started it is listed: it is inside that row's
 * chip. Wide tables scroll inside their own box; on a phone the table keeps
 * the title and when, and each row states the rest under its title.
 */
export default function HomeTable({
  groups,
  grouped,
  isOpen,
  onToggle,
  columns,
  childSessions,
  viewerUsername,
  helperGroupSlot,
  now,
}: HomeTableProps) {
  const shown = HOME_COLUMNS.filter((key) => columns[key]);
  const span = shown.length + 1;

  const cell = (key: HomeColumn, item: TranscriptListItem): ReactNode => {
    const t = item.transcript;
    switch (key) {
      case "project": {
        const branch = t.git_branch?.trim() ?? "";
        // A project is routed on its hash. A row that arrived without one is a
        // server contract violation: it is still listed, and its project is
        // named but not linked, never folded into a made-up project.
        return (
          <td key={key} className="is-data is-wrap is-wide" data-col="project">
            {t.project_hash ? (
              <Link
                href={`/users/${encodeURIComponent(viewerUsername)}/projects/${t.project_hash}`}
                className="on-cell-link"
              >
                {t.project_display_name}
              </Link>
            ) : (
              <span>{t.project_display_name}</span>
            )}
            {branch !== "" && <span className="on-sub">{branch}</span>}
          </td>
        );
      }
      case "readers": {
        const readers = readersOf(item);
        return (
          <td key={key} className="is-data is-wrap is-wide" data-col="readers">
            {readers.length > 0 ? (
              <span className="on-readers">
                {readers.map((name) => (
                  <span key={name}>{name}</span>
                ))}
              </span>
            ) : (
              <span className="on-none">{NO_READERS}</span>
            )}
          </td>
        );
      }
      case "agent":
        return (
          <td key={key} className="is-data is-wide" data-col="agent">
            {isHarness(t.model_provider) ? (
              <ProviderName harness={t.model_provider} className="on-agent" />
            ) : (
              <span className="on-agent">{agentLabel(item)}</span>
            )}
          </td>
        );
      case "pullRequests":
        return (
          <td key={key} className="is-data is-wide" data-col="pullRequests">
            <PullRequestCell transcriptID={t.id} summary={item.pull_requests} />
          </td>
        );
      case "turns":
        return (
          <td key={key} className="is-data is-n is-wide" data-col="turns">
            {t.turn_count == null ? <span className="on-none">not counted</span> : t.turn_count.toLocaleString("en-US")}
          </td>
        );
      case "when":
        return (
          <td key={key} className="is-data is-n" data-col="when">
            <time dateTime={t.published_at}>{formatRelativeTime(t.published_at, now)}</time>
          </td>
        );
    }
  };

  // On a phone the table keeps the title and when, and each row states the
  // rest under its title: who can read it always, then the other columns the
  // person chose. Wider, this line is not drawn and the columns carry them.
  const facts = (item: TranscriptListItem): ReactNode => {
    const t = item.transcript;
    const readers = readersOf(item);
    return (
      <span className="on-row-facts" data-testid="home-row-facts">
        <span className="on-fact-readers" data-fact="readers">
          <Eye aria-hidden="true" />
          <span className="sr-only">{HOME_COLUMN_LABELS.readers}: </span>
          {readers.length > 0 ? readers.join(", ") : NO_READERS}
        </span>
        {columns.project && <span data-fact="project">{t.project_display_name}</span>}
        {columns.agent && (
          <span data-fact="agent">
            {isHarness(t.model_provider) ? (
              <ProviderName harness={t.model_provider} className="on-agent" />
            ) : (
              <span className="on-agent">{agentLabel(item)}</span>
            )}
          </span>
        )}
        {columns.pullRequests && (
          <span data-fact="pullRequests">
            <PullRequestCell transcriptID={t.id} summary={item.pull_requests} />
          </span>
        )}
        {columns.turns && t.turn_count != null && (
          <span data-fact="turns" className="tabular-nums">
            {plural(t.turn_count, "turn", "turns")}
          </span>
        )}
      </span>
    );
  };

  const row = (item: TranscriptListItem, inGroup: boolean) => {
    const t = item.transcript;
    const started = childSessions.get(t.id);
    return (
      <tr key={t.id} className={inGroup ? "is-row is-in" : "is-row"} data-testid="home-row">
        <td>
          <div className="on-cell-title" data-testid="home-transcript-row" data-transcript-id={t.id}>
            <Link href={transcriptHref(t.id, { kind: "home" })}>{t.title || "untitled"}</Link>
            {facts(item)}
            {helperGroupSlot(item)}
            {started !== undefined && started.length > 0 && (
              <ChildSessionDisclosure
                parentTranscriptID={t.id}
                childSessions={started}
                hideOwner
                facts={HOME_STARTED_SESSION_FACTS}
                helperGroupSlot={helperGroupSlot}
                context={{ kind: "home" }}
              />
            )}
          </div>
        </td>
        {shown.map((key) => cell(key, item))}
      </tr>
    );
  };

  return (
    <div className="on-table-box" data-testid="home-table">
      <table className="on-table">
        <caption>transcripts you published</caption>
        <colgroup>
          <col />
          {shown.map((key) => (
            <col key={key} className={key === "when" ? `on-col-${key}` : `on-col-${key} is-wide`} />
          ))}
        </colgroup>
        <thead>
          <tr>
            <th scope="col">transcript</th>
            {shown.map((key) => (
              <th
                key={key}
                scope="col"
                className={[NUMERIC.has(key) ? "is-n" : "", key === "when" ? "" : "is-wide"].filter(Boolean).join(" ") || undefined}
              >
                {HOME_COLUMN_LABELS[key]}
              </th>
            ))}
          </tr>
        </thead>
        {groups.map((group) => {
          const open = !grouped || isOpen(group.id);
          return (
            <tbody key={group.id} data-testid={grouped ? "home-group" : undefined}>
              {grouped && (
                <tr className="is-group">
                  <th colSpan={span} scope="rowgroup">
                    <button
                      type="button"
                      className="on-gtog"
                      aria-expanded={open}
                      onClick={() => onToggle(group.id)}
                    >
                      <span className="on-chev" data-open={open} aria-hidden="true" />
                      <span className="on-gtog-label">{group.label}</span>{" "}
                      <span className="on-k">{plural(group.rows.length, "transcript", "transcripts")}</span>
                    </button>
                  </th>
                </tr>
              )}
              {open && group.rows.map((item) => row(item, grouped))}
            </tbody>
          );
        })}
      </table>
    </div>
  );
}
