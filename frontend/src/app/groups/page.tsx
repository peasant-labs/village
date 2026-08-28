"use client";

import { useMemo } from "react";
import { Users } from "lucide-react";
import { useRouter } from "next/navigation";
import { useVisibleGroups, useCreateGroup } from "@/lib/queries/groups";
import { useMyCollectiveContributions } from "@/lib/queries/collectives";
import { useMyOrgs } from "@/lib/queries/orgs";
import { useAuth } from "@/providers/AuthProvider";
import { collectiveStanding, contributedCollectiveIds } from "@/lib/collectiveBadges";
import { CollectivesView } from "@peasant-labs/fairtrade/commons";

// "Member for 5mo" / "Joined today" — mirrors the fairtrade demo fixture's
// card-footer phrasing (CollectivesView's mock COLLECTIVES `since` strings),
// computed from the real `member_since` timestamp instead of a raw ISO string.
function formatMemberSince(iso: string | null): string | null {
  if (!iso) return null;
  const joined = new Date(iso);
  if (Number.isNaN(joined.getTime())) return null;
  const days = Math.floor((Date.now() - joined.getTime()) / 86_400_000);
  if (days < 1) return "Joined today";
  if (days < 31) return `Member for ${days}d`;
  const months = Math.floor(days / 30);
  if (months < 12) return `Member for ${months}mo`;
  const years = Math.floor(months / 12);
  return `Member for ${years}y`;
}

export default function GroupsPage() {
  const router = useRouter();
  const { isLoggedIn } = useAuth();
  // Every collective the caller may SEE, not only the ones they belong to: a
  // person browsing collectives is asking which ones exist for them, and the
  // membership-only list answered a different question. Rows the caller does
  // not belong to carry a null role.
  const { data: groups } = useVisibleGroups();
  const { data: contributions } = useMyCollectiveContributions(isLoggedIn);
  const createGroup = useCreateGroup();

  const { data: myOrgs } = useMyOrgs();
  const visibleOrgs = (myOrgs ?? []).filter((o) => o.visible);

  const collectives = useMemo(() => {
    const contributed = contributedCollectiveIds(contributions);
    return (groups ?? []).map((g) => {
      const standing = collectiveStanding(g, contributed);
      return {
        id: g.id,
        name: g.name,
        desc: g.description,
        // The card's one standing slot. It states the caller's role when they
        // belong to the collective, and says "contributed" when the collective
        // holds or is still reviewing something of theirs. A row that is
        // neither carries nothing: the caller can see this collective, and
        // there is nothing further to claim about them.
        role: [standing.memberRole, standing.hasContributed ? "contributed" : null]
          .filter(Boolean)
          .join(" \u00b7 "),
        // Only a member has a join date, so this is absent on the rows the
        // caller only sees.
        since: formatMemberSince(g.member_since),
        mode: g.acceptance_mode,
        members: g.member_count,
        transcripts: g.transcript_count,
      };
    });
  }, [groups, contributions]);

  if (!isLoggedIn) {
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">
        <nav aria-label="Breadcrumb" className="flex items-center gap-1 text-xs">
          <span className="text-ink font-medium">Collectives</span>
        </nav>
        <div className="border border-rule bg-surface px-5 py-12 flex flex-col items-center gap-3 text-center">
          <Users size={28} className="text-ink-4" />
          <p className="text-sm font-medium text-ink">
            Sign in to join or create collectives
          </p>
          <p className="text-[13px] text-ink-3 max-w-sm">
            Collectives are groups that govern shared data together.
          </p>
        </div>
      </div>
    );
  }

  const handleCreateCollective = ({ name, purpose, mode, access, org }: { name: string; purpose: string; mode: string; access: string; org: string }) => {
    createGroup.mutate(
      {
        name,
        description: purpose,
        acceptance_mode: mode,
        data_access: access,
        linked_github_org: org || null,
      }
    );
  };

  return (
    <div className="cmg-root max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">
      {/* CollectivesView owns the breadcrumb, heading, and deck for this surface. */}

      <CollectivesView
        data={{
          collectives,
          linkedOrgs: visibleOrgs.map((o) => o.org_login),
          title: "collectives",
          deck: "groups that govern shared data together.",
          crumb: "collectives",
          createLabel: "new collective",
          createBusy: createGroup.isPending,
        }}
        actions={{
          onCreateCollective: handleCreateCollective,
          onOpenCollective: (id: string) => router.push(`/groups/${id}`),
        }}
      />
    </div>
  );
}
