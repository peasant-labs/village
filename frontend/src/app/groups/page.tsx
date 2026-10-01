"use client";

import { useMemo, useState } from "react";
import { Users } from "lucide-react";
import { useRouter } from "next/navigation";
import { useVisibleGroups, useCreateGroup, useSearchCollectives } from "@/lib/queries/groups";
import { createGroupRequest } from "@/lib/queries/groupRequests";
import { useMyCollectiveContributions } from "@/lib/queries/collectives";
import { useMyOrgs } from "@/lib/queries/orgs";
import { useAuth } from "@/providers/AuthProvider";
import { collectiveStanding, contributedCollectiveIds } from "@/lib/collectiveBadges";
import { collectiveListRow, searchResultRows } from "@/lib/adapters/collective";
import { CollectivesView } from "@peasant-labs/fairtrade/commons";

export default function GroupsPage() {
  const router = useRouter();
  const { isLoggedIn } = useAuth();
  // Every collective the caller may SEE, not only the ones they belong to: a
  // person browsing collectives is asking which ones exist for them, and the
  // membership-only list answered a different question. Rows the caller does
  // not belong to carry a null role.
  const visible = useVisibleGroups();
  const groups = visible.data;
  const { data: contributions } = useMyCollectiveContributions(isLoggedIn);
  const createGroup = useCreateGroup();
  // The search the person submitted. An empty search shows the list again.
  const [query, setQuery] = useState("");
  const search = useSearchCollectives(query);

  const { data: myOrgs } = useMyOrgs();
  const visibleOrgs = (myOrgs ?? []).filter((o) => o.visible);

  const collectives = useMemo(() => {
    const contributed = contributedCollectiveIds(contributions);
    const now = new Date();
    return (groups ?? []).map((g) => collectiveListRow(g, collectiveStanding(g, contributed), now));
  }, [groups, contributions]);

  const searching = query.trim().length > 0;
  const rows = searching ? searchResultRows(search.data?.collectives ?? [], collectives) : collectives;

  if (!isLoggedIn) {
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">
        <div className="border border-rule bg-surface px-5 py-12 flex flex-col items-center gap-3 text-center">
          <Users size={28} className="text-ink-4" aria-hidden="true" />
          <p className="text-sm font-medium text-ink">sign in to join or create collectives</p>
          <p className="text-[13px] text-ink-3 max-w-sm">
            a collective is a group that can read the transcripts its members publish to it.
          </p>
        </div>
      </div>
    );
  }

  const handleCreateCollective = ({ name, purpose, mode, access, org }: { name: string; purpose: string; mode: string; access: string; org: string }) => {
    createGroup.mutate(createGroupRequest({ name, purpose, mode, access, org }), {
      onSuccess: (created) => router.push(`/groups/${created.id}`),
    });
  };

  return (
    <div className="cmg-root max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">
      {/* CollectivesView owns the heading, the deck, the table and the search. */}
      <CollectivesView
        data={{
          collectives: rows,
          linkedOrgs: visibleOrgs.map((o) => o.org_login),
          caption: searching ? "search results" : "collectives",
          createBusy: createGroup.isPending,
        }}
        actions={{
          onCreateCollective: handleCreateCollective,
          onOpenCollective: (id: string) => router.push(`/groups/${id}`),
          onSearchCollectives: (next: string) => setQuery(next.trim()),
        }}
      />
      {visible.isError ? (
        <div role="alert" className="text-[var(--fs-body)] text-danger">
          <p>could not read collectives: {visible.error.message}</p>
          <button type="button" className="btn btn-secondary btn-sm" disabled={visible.isFetching} onClick={() => void visible.refetch()}>try again</button>
        </div>
      ) : null}
      {createGroup.isError ? (
        <p role="alert" className="text-[var(--fs-body)] text-danger">
          could not create the collective: {createGroup.error.message}
        </p>
      ) : null}
      {searching && search.isError ? (
        <p role="alert" className="text-[13px] text-danger">
          the search failed: {search.error.message}
        </p>
      ) : null}
    </div>
  );
}
