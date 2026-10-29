import Link from "next/link";
import { ProviderName } from "@/lib/ft-ui";
import { isHarness } from "@/lib/harness";
import { formatRelativeTime } from "@/lib/format";
import { agentLabel } from "@/lib/homeView";
import type { TranscriptListItem } from "@/lib/types";
import { transcriptHref } from "@/lib/transcriptContext";

/** How many of the newest transcripts the strip above the table shows. */
export const RECENT_SHOWN = 3;

/**
 * The newest few transcripts as cards above the table, so the session a person
 * just published is one click away however the table below is grouped.
 */
// Retained as a deprecation candidate; Home now lists each transcript once.
export default function HomeRecent({ rows, now }: { rows: TranscriptListItem[]; now: number }) {
  if (rows.length === 0) return null;
  return (
    <section className="on-recent" aria-labelledby="home-recent-label" data-testid="home-recent">
      <h2 className="on-recent-label" id="home-recent-label">
        recent
      </h2>
      <ul className="on-rcards">
        {rows.slice(0, RECENT_SHOWN).map((item) => {
          const t = item.transcript;
          return (
            <li key={t.id}>
              <Link href={transcriptHref(t.id, { kind: "home" })} className="on-rcard">
                <span className="on-rcard-title">{t.title || "untitled"}</span>
                <span className="on-rcard-meta">
                  <span className="on-agent">
                    {t.project_display_name != null && <span className="on-rcard-project">{t.project_display_name},</span>}
                    {isHarness(t.model_provider) ? <ProviderName harness={t.model_provider} /> : <span>{agentLabel(item)}</span>}
                  </span>
                  <time className="on-n" dateTime={t.published_at}>
                    {formatRelativeTime(t.published_at, now)}
                  </time>
                </span>
              </Link>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
