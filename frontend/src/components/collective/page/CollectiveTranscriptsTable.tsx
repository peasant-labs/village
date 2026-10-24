"use client";

import Link from "next/link";
import { ProviderName } from "@/lib/ft-ui";
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
  onShowMore,
}: {
  /** The collective listing them: each title opens its transcript from here. */
  collectiveId: string;
  rows: CollectiveTranscriptRow[];
  /** Where a pull request label leads. */
  pullRequestHref: (label: string) => string | undefined;
  total: number;
  /** Shows the next page; left out, no button is drawn. */
  onShowMore?: () => void;
}) {
  return (
    <div className="on-section">
      <div className="on-tbl">
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
            {rows.map((row) => (
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
      {total > rows.length && (
        <div className="on-pager">
          <span>
            showing <span className="on-n">{rows.length}</span> of <span className="on-n">{total}</span>
          </span>
          {onShowMore && (
            <button type="button" className="btn btn-secondary" onClick={onShowMore}>
              show more
            </button>
          )}
        </div>
      )}
    </div>
  );
}
