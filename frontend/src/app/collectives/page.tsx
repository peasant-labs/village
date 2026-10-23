"use client";

import { useMemo, useState } from "react";
import Link from "next/link";
import { Users } from "lucide-react";
import { useVisibleGroups } from "@/lib/queries/groups";
import { useMyCollectiveContributions } from "@/lib/queries/collectives";
import { useAuth } from "@/providers/AuthProvider";
import { collectiveStanding, contributedCollectiveIds } from "@/lib/collectiveBadges";
import { whoCanPublishShort } from "@/lib/collectivePolicyCopy";
import { loginHref } from "@/lib/signIn";
import { CollectiveSwatch, OnEmpty, OnHead, OnPage } from "@/components/online/Page";
import NewCollectiveDialog from "@/components/collectives/NewCollectiveDialog";
import "../online-collectives.css";

const LEDE = "Your teams, labs and classes. Each one decides who publishes and who reads.";

/**
 * The collectives the signed-in person belongs to.
 *
 * It reads the visible-collectives route and keeps the rows that carry a role:
 * those are exactly the memberships (the server answers the same set on
 * `GET /collectives`). The rows without a role, collectives the person may see
 * but has not joined, are listed on `/discover`, which reads the same answer,
 * so the two pages split one list and nothing a person could reach before is
 * lost between them.
 */
export default function CollectivesPage() {
  const { isLoading: authLoading, isLoggedIn, user } = useAuth();
  const visible = useVisibleGroups();
  const { data: contributions } = useMyCollectiveContributions(isLoggedIn);
  const [creating, setCreating] = useState(false);

  const rows = useMemo(() => {
    const contributed = contributedCollectiveIds(contributions);
    return (visible.data ?? [])
      .map((group) => ({ group, standing: collectiveStanding(group, contributed) }))
      .filter(({ standing }) => standing.memberRole !== null);
  }, [visible.data, contributions]);

  if (!authLoading && !isLoggedIn) {
    return (
      <OnPage testId="collectives-page">
        <OnHead title="collectives" lede={LEDE} />
        <OnEmpty
          testId="collectives-signed-out"
          icon={<Users />}
          title="sign in to see your collectives"
          body="A collective is a group that reads the transcripts its members publish to it."
          actions={
            <Link className="btn btn-primary" href={loginHref("/collectives")}>
              sign in
            </Link>
          }
        />
      </OnPage>
    );
  }

  const loading = authLoading || visible.isLoading;
  const failed = !loading && visible.isError;
  const empty = !loading && !failed && rows.length === 0;

  const newCollective = (
    <button type="button" className="btn btn-primary" onClick={() => setCreating(true)}>
      new collective
    </button>
  );

  return (
    <OnPage testId="collectives-page">
      <OnHead
        title="collectives"
        lede={LEDE}
        actions={
          !loading && !empty ? (
            <>
              <Link className="btn btn-secondary" href="/discover">
                discover
              </Link>
              {newCollective}
            </>
          ) : undefined
        }
      />

      {loading ? (
        <ul className="on-skel-list" aria-busy="true" aria-label="loading your collectives">
          <li className="on-skel" />
          <li className="on-skel is-row" />
          <li className="on-skel is-row" />
          <li className="on-skel is-row" />
        </ul>
      ) : failed ? (
        <div className="on-notice" data-tone="danger" role="alert">
          <p>could not read your collectives: {visible.error?.message}</p>
          <button
            type="button"
            className="btn btn-secondary"
            disabled={visible.isFetching}
            onClick={() => void visible.refetch()}
          >
            try again
          </button>
        </div>
      ) : empty ? (
        <OnEmpty
          testId="collectives-empty"
          icon={<Users />}
          title="no collectives yet"
          actions={
            <>
              {newCollective}
              <Link className="btn btn-secondary" href="/discover">
                discover collectives
              </Link>
            </>
          }
        />
      ) : (
        <div className="on-tbl" tabIndex={0} role="region" aria-label="collectives you belong to">
          <table>
            <caption className="sr-only">collectives you belong to</caption>
            <thead>
              <tr>
                <th scope="col">collective</th>
                <th scope="col">your role</th>
                <th scope="col">who can publish</th>
                <th scope="col" className="is-n">
                  members
                </th>
                <th scope="col" className="is-n">
                  transcripts
                </th>
              </tr>
            </thead>
            <tbody>
              {rows.map(({ group, standing }) => (
                <tr key={group.id} data-collective-id={group.id}>
                  <td>
                    <span className="on-name">
                      <CollectiveSwatch id={group.id} />
                      <Link className="is-title" href={`/collectives/${group.id}`}>
                        {group.name}
                      </Link>
                    </span>
                  </td>
                  <td data-cell="standing">
                    <span className="on-standing">
                      <span>{standing.memberRole}</span>
                      {standing.hasContributed ? <span className="on-tag">contributed</span> : null}
                    </span>
                  </td>
                  <td className="is-policy">
                    {whoCanPublishShort(group.acceptance_mode, group.linked_github_org, standing.memberRole === "owner")}
                  </td>
                  <td className="is-n">{group.member_count.toLocaleString("en-US")}</td>
                  <td className="is-n">{group.transcript_count.toLocaleString("en-US")}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {creating && user ? <NewCollectiveDialog onClose={() => setCreating(false)} /> : null}
    </OnPage>
  );
}
