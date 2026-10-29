"use client";

import Link from "next/link";
import { Pagination, ProviderName, formatInteger } from "@/lib/ft-ui";
import RetryButton from "@/components/RetryButton";
import { isHarness } from "@/lib/harness";
import type { CollectiveTranscriptRow } from "@/lib/adapters/collective";
import { transcriptHref } from "@/lib/transcriptContext";

/** The agent that recorded a session: its brand mark and name, or the raw value when it has no mark. */
export function AgentCell({ provider }: { provider: string | null | undefined }) {
  if (isHarness(provider)) return <ProviderName harness={provider} />;
  return <span className="on-none">{provider || "unknown"}</span>;
}

/**
 * A collective's transcripts: the title (and the pull requests it is attached
 * to, each a link to its prompts page), who contributed it, the agent, turns,
 * and when. There is no status column: the server lists only approved
 * transcripts, so every row would say the same thing; the ones still waiting
 * are the waiting tab's.
 */
export default function CollectiveTranscriptsTable({
  collectiveId,
  rows,
  pullRequestHref,
  total,
  page,
  pageSize,
  onPageChange,
  loading = false,
  error,
  retrying = false,
  onRetry,
}: {
  /** The collective listing them: each title opens its transcript from here. */
  collectiveId: string;
  rows: CollectiveTranscriptRow[];
  /** Where a pull request label leads. */
  pullRequestHref: (label: string) => string | undefined;
  total: number;
  /** The current page, starting at one. */
  page: number;
  pageSize: number;
  onPageChange: (page: number) => void;
  loading?: boolean;
  error?: string;
  retrying?: boolean;
  onRetry?: () => void;
}) {
  const first = rows.length ? (page - 1) * pageSize + 1 : 0;
  const last = rows.length ? first + rows.length - 1 : 0;
  return (
    <div className="on-section">
      <div className="on-tbl" aria-busy={loading}>
        <table className="on-ctable">
          <caption>transcripts {total}</caption>
          <thead>
            <tr>
              <th scope="col">transcript</th>
              <th scope="col" className="is-wide">
                contributor
              </th>
              <th scope="col" className="is-wide">
                agent
              </th>
              <th scope="col" className="is-n is-wide">
                turns
              </th>
              <th scope="col" className="is-n">
                when
              </th>
            </tr>
          </thead>
          <tbody>
            {error ? (
              <tr>
                <td colSpan={5}>
                  <div role="alert">could not load this page: {error}</div>
                  {onRetry && <RetryButton label={retrying ? "retrying" : "retry"} busy={retrying} onRetry={onRetry} />}
                </td>
              </tr>
            ) : loading ? (
              <tr><td colSpan={5}>loading transcripts</td></tr>
            ) : rows.length === 0 ? (
              <tr><td colSpan={5}>no transcripts on this page</td></tr>
            ) : rows.map((row) => (
              <tr key={row.id}>
                <td className="on-ctable-title">
                  <Link className="is-title" href={transcriptHref(row.id, { kind: "collective", id: collectiveId })}>
                    {row.title}
                  </Link>
                  {(row.pullRequests?.length ?? 0) > 0 && (
                    <span className="is-sub on-prs">
                      {row.pullRequests!.map((label) => {
                        const href = pullRequestHref(label);
                        return href ? (
                          <Link key={label} href={href} className="on-pr-link">
                            {label}
                          </Link>
                        ) : (
                          <span key={label}>{label}</span>
                        );
                      })}
                      {row.repo && <span className="on-pr-repo">{row.repo}</span>}
                    </span>
                  )}
                  {/* On a phone the contributor, agent and turns columns give
                      way, and the row states them under its title instead. */}
                  <span className="is-sub on-ctable-facts" data-testid="collective-row-facts">
                    <span>{row.author}</span>
                    <span className="on-agent">
                      <AgentCell provider={row.harness} />
                    </span>
                    {row.turns != null && (
                      <span className="tabular-nums">
                        {row.turns} {row.turns === 1 ? "turn" : "turns"}
                      </span>
                    )}
                  </span>
                </td>
                <td className="on-handle-cell is-wide">{row.author}</td>
                <td className="is-wide">
                  <span className="on-agent">
                    <AgentCell provider={row.harness} />
                  </span>
                </td>
                <td className="is-n is-wide">{row.turns ?? ""}</td>
                <td className="is-n on-when">{row.when ?? ""}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="on-pager">
        <span role="status" aria-live="polite">
          {error ? `page ${formatInteger(page)} could not be loaded`
            : loading ? `loading page ${formatInteger(page)}`
              : `showing ${formatInteger(first)} to ${formatInteger(last)} of ${formatInteger(total)}`}
        </span>
        {total > pageSize && (
          <Pagination
            label="transcript pages"
            page={page}
            totalItems={total}
            pageSize={pageSize}
            siblingCount={0}
            onChange={onPageChange}
          />
        )}
      </div>
    </div>
  );
}
