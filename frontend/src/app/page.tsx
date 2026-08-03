"use client";

import { useMemo, useState } from "react";
import { SearchX } from "lucide-react";
import { useRouter } from "next/navigation";
import { useTranscripts } from "@/lib/queries/transcripts";
import { useSearchCollectives } from "@/lib/queries/groups";
import { usePopularTags } from "@/lib/queries/tags";
import { adaptExplore } from "@/lib/adapters/explore";
import { Explore } from "@peasant-labs/fairtrade/commons";

type ExploreFilters = {
  query: string;
  provider: string;
  topics: string[];
  order: string;
  page: number;
};

const DEFAULT_FILTERS: ExploreFilters = {
  query: "",
  provider: "all",
  topics: [],
  order: "recent",
  page: 1,
};

export default function ExplorePage() {
  const router = useRouter();
  const [filters, setFilters] = useState<ExploreFilters>(DEFAULT_FILTERS);

  const params = useMemo(() => {
    const next: Record<string, string> = {
      sort: filters.order,
      page: String(filters.page),
      limit: String(24),
    };
    if (filters.query.trim()) next.q = filters.query.trim();
    if (filters.provider && filters.provider !== "all") next.provider = filters.provider;
    if (filters.topics.length > 0) next.tags = filters.topics.join(",");
    return next;
  }, [filters]);

  const { data, isLoading, error } = useTranscripts(params);
  const { data: collData } = useSearchCollectives(filters.query);
  const { data: popularTags } = usePopularTags(15);

  const payload = useMemo(() => {
    if (!data) return null;
    return adaptExplore(
      data,
      collData ?? { collectives: [] },
      popularTags ?? []
    );
  }, [collData, data, popularTags]);

  if (error) {
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">
        <div className="border border-rule bg-surface px-5 py-12 flex flex-col items-center gap-3 text-center">
          <SearchX size={28} className="text-ink-4" />
          <p className="text-sm font-medium text-ink">Failed to load transcripts</p>
          <p className="text-[13px] text-ink-3 max-w-sm">
            {error instanceof Error ? error.message : "The commons browse surface could not load."}
          </p>
        </div>
      </div>
    );
  }

  if (isLoading || !payload) {
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">
        <div className="flex flex-col gap-1">
          <div className="h-8 w-72 animate-shimmer" />
          <div className="h-4 w-96 animate-shimmer" />
        </div>
        <div className="grid grid-cols-1 lg:grid-cols-[16rem_minmax(0,1fr)] gap-6">
          <div className="h-[420px] animate-shimmer" />
          <div className="flex flex-col gap-4">
            <div className="h-24 animate-shimmer" />
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div className="h-48 animate-shimmer" />
              <div className="h-48 animate-shimmer" />
            </div>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12">
      <Explore
        data={payload}
        onFiltersChange={setFilters}
        transcriptHref={(transcript) => `/transcripts/${transcript.id}`}
        profileHref={(owner) => `/users/${owner.githubUsername}`}
        collectiveHref={(collective) => `/groups/${collective.id}`}
        onOpenTranscript={(transcript) => router.push(`/transcripts/${transcript.id}`)}
        onOpenProfile={(owner) => router.push(`/users/${owner.githubUsername}`)}
        onOpenCollective={(collective) => router.push(`/groups/${collective.id}`)}
      />
    </div>
  );
}
