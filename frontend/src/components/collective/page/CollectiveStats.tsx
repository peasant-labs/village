"use client";

import { Github, SlidersHorizontal } from "lucide-react";
import type { CollectiveGithub } from "./useCollectiveGithub";
import { formatInteger } from "@/lib/ft-ui";

/** The org line's words: the org, and how many of its repositories are linked when that is known. */
export function orgLineText(org: CollectiveGithub["org"]): string {
  if (!org) return "nothing linked";
  return typeof org.total === "number" ? `${org.login}: ${org.linked ?? 0} of ${org.total} repos linked` : org.login;
}

/**
 * The collective in numbers (transcripts, members, tokens, pull requests
 * linked), then its GitHub org: how many repositories are linked, and for the
 * owner the picker that links them.
 */
export default function CollectiveStats({
  name,
  stats,
  github,
  onManage,
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

      <section className="on-section" aria-labelledby="collective-github-heading">
          <h3 className="on-h2" id="collective-github-heading">
            github org
          </h3>
          {github.configured ? (
            <div className="on-orgline" data-testid="collective-org-line">
              <Github aria-hidden="true" />
              <span className="on-orgline-text">{orgLineText(github.org)}</span>
              {onManage && (
                <button
                  type="button"
                  className="btn btn-secondary on-orgline-manage"
                  aria-label="manage repositories"
                  title="manage repositories"
                  onClick={onManage}
                >
                  <SlidersHorizontal aria-hidden="true" />
                  <span className="on-orgline-manage-word">manage repositories</span>
                </button>
              )}
            </div>
          ) : (
            <div className="on-notice" data-testid="collective-github-not-configured">
              <p>github connection isn&apos;t set up on this server.</p>
            </div>
          )}
        </section>
    </div>
  );
}
