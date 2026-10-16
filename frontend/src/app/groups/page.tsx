import { redirectLegacyCollective, type LegacySearchParams } from "@/lib/nav/legacyCollectives";

export default async function LegacyCollectives({ searchParams }: {
  searchParams: Promise<LegacySearchParams>;
}) {
  redirectLegacyCollective("/collectives", await searchParams);
}
