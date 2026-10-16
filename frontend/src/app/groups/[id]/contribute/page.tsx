import { redirectLegacyCollective, type LegacySearchParams } from "@/lib/nav/legacyCollectives";

export default async function LegacyCollectiveContribute({ params, searchParams }: {
  params: Promise<{ id: string }>;
  searchParams: Promise<LegacySearchParams>;
}) {
  redirectLegacyCollective(`/collectives/${encodeURIComponent((await params).id)}/contribute`, await searchParams);
}
