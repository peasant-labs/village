import { redirectLegacyCollective, type LegacySearchParams } from "@/lib/nav/legacyCollectives";

export default async function LegacyCollectiveReview({ params, searchParams }: {
  params: Promise<{ id: string }>;
  searchParams: Promise<LegacySearchParams>;
}) {
  redirectLegacyCollective(`/collectives/${encodeURIComponent((await params).id)}/review`, await searchParams);
}
