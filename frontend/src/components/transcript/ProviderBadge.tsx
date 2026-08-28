import { ProviderTag, Tag } from "@/lib/ft-ui";
import { isHarness } from "@/lib/harness";

/**
 * The canonical harness identity badge for a transcript row.
 *
 * A recognised harness renders fairtrade's `ProviderTag` (which leads with the
 * `BrandMark` the design system requires for provider names); anything else
 * degrades to a neutral `Tag` carrying the raw provider string, and an empty
 * provider reads "unknown" rather than rendering an empty pill.
 *
 * Every surface that shows a provider next to a transcript uses THIS module -
 * the same helper was previously copied verbatim into individual pages, which
 * is how the two copies drifted apart in casing and fallback wording.
 */
export default function ProviderBadge({
  provider,
  className,
}: {
  provider: string;
  className?: string;
}) {
  return isHarness(provider) ? (
    <ProviderTag harness={provider} className={className} />
  ) : (
    <Tag className={className}>{provider || "unknown"}</Tag>
  );
}
