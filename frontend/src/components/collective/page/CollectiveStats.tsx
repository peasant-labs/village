"use client";

import type { CollectiveGithub } from "./useCollectiveGithub";
import { formatInteger } from "@/lib/ft-ui";

/** The org line's words: the org, and how many of its repositories are linked when that is known. */
export function orgLineText(org: CollectiveGithub["org"]): string {
  if (!org) return "nothing linked";
  return typeof org.total === "number" ? `${org.login}: ${org.linked ?? 0} of ${org.total} repos linked` : org.login;
}

/**
 * The collective in numbers (transcripts, members, tokens, pull requests
 * linked). Repository controls live on the repositories tab.
 */
export default function CollectiveStats({
  name,
  stats,
}: {
  name: string;
  stats: { value: number | string; label: string }[];
  github: CollectiveGithub;
  /** Opens the repo picker; left out, no manage button is drawn. */
  onManage?: () => void;
}) {
  return (
    <div className="on-section on-cstats">
      <dl className="on-stats" aria-label={`${name} in numbers`}>
        {stats.map((stat) => (
          <div key={stat.label} className="on-stat">
            <dt>{stat.label}</dt>
            <dd>{typeof stat.value === "number" ? formatInteger(stat.value) : stat.value}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}
