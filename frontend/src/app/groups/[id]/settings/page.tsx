import { redirectLegacyCollective, type LegacySearchParams } from "@/lib/nav/legacyCollectives";

export default async function LegacyCollectiveSettings({ params, searchParams }: {
  params: Promise<{ id: string }>;
  searchParams: Promise<LegacySearchParams>;
}) {
  redirectLegacyCollective(`/collectives/${encodeURIComponent((await params).id)}/settings`, await searchParams);
}
