"use client";

import { useState } from "react";
import { BrandMark, RepoPicker } from "@/lib/ft-ui";
import type { CollectiveGithub } from "./useCollectiveGithub";

/** The GitHub connection belongs beside the repositories it controls. */
export default function CollectiveRepositories({ github, onResult }: { github: CollectiveGithub; onResult?: (result: { ok: boolean; title: string; detail: string }) => void }) {
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState<string>();
  const [error, setError] = useState<string>();
  const linked = new Set(github.linkedIds);
  const repositories = github.owners.flatMap((owner) => owner.repos).filter((repo) => linked.has(repo.id));

  if (!github.configured) return <p className="on-muted">github connection isn&apos;t set up on this server.</p>;

  return (
    <div className="on-section on-repositories">
      <div className="on-row" data-testid="collective-org-line">
        <BrandMark name="github" />
        <span className="on-keep-case on-orgline-text">{github.org ? (typeof github.org.total === "number" ? `${github.org.login}: ${github.org.linked ?? 0} of ${github.org.total} repos linked` : github.org.login) : "nothing linked"}</span>
        <span className="on-muted tnum">{github.linkedIds.length} repositories linked</span>
      </div>
      {github.canManage ? (
        <RepoPicker
          key={github.linkedIds.join("\n")}
          inline
          open
          onClose={() => {}}
          owners={github.owners}
          initialSelected={github.linkedIds}
          title="repositories"
          description="pull requests in linked repositories can show this collective's transcripts."
          saving={saving}
          error={error}
          onSave={async (diff) => {
            setSaving(true);
            setError(undefined);
            setMessage(undefined);
            try {
              const result = await github.saveLinks(diff);
              onResult?.(result);
              if (result.ok) setMessage(result.title);
              else setError(`${result.title}: ${result.detail}`);
            } catch (failure) {
              setError(failure instanceof Error ? failure.message : "could not save repository links; try again");
            } finally {
              setSaving(false);
            }
          }}
        />
      ) : repositories.length > 0 ? (
        <ul className="on-repository-list">
          {repositories.map((repo) => <li key={repo.id}><span className="on-keep-case">{repo.name}</span></li>)}
        </ul>
      ) : <p className="on-muted">no repositories linked yet.</p>}
      {message && <p role="status">{message}</p>}
    </div>
  );
}
