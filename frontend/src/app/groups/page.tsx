"use client";

import { useMemo } from "react";
import { Users } from "lucide-react";
import { useRouter } from "next/navigation";
import { useGroups, useCreateGroup } from "@/lib/queries/groups";
import { useMyOrgs } from "@/lib/queries/orgs";
import { useAuth } from "@/providers/AuthProvider";
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
  const { data: groups } = useGroups();
  const createGroup = useCreateGroup();

  const { data: myOrgs } = useMyOrgs();
  const visibleOrgs = (myOrgs ?? []).filter((o) => o.visible);

  const collectives = useMemo(
    () =>
      (groups ?? []).map((g) => ({
        id: g.id,
        name: g.name,
        desc: g.description,
        role: g.role,
        since: formatMemberSince(g.member_since),
        mode: g.acceptance_mode,
        // member_count/transcript_count are not yet returned by GET /groups
        // (see the Group type comment) -- undefined here omits them from the
        // card footer rather than showing a fabricated or wrong count.
        members: g.member_count,
        transcripts: g.transcript_count,
      })),
    [groups]
  );

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
      {/* CollectivesView (below) already renders its own "village > collectives" breadcrumb + the
          "collectives" heading/deck (matching the fairtrade demo) -- an app-chrome breadcrumb +
          title block here duplicated both (UAT finding: heading + description "REPEATED TWICE"). */}

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
