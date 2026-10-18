import { redirectLegacyCollective, type LegacySearchParams } from "@/lib/nav/legacyCollectives";

export default async function LegacyCollective({ params, searchParams }: {
  params: Promise<{ id: string }>;
  searchParams: Promise<LegacySearchParams>;
}) {
  redirectLegacyCollective(`/collectives/${encodeURIComponent((await params).id)}`, await searchParams);
}
