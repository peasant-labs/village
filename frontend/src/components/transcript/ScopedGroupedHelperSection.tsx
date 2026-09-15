"use client";

import { useGroupedTranscripts } from "@/lib/queries/helperGroups";
import { ScopedGroupedHelperRows } from "./ScopedHelperGroups";

/**
 * The saved-helper-thread section of a discovery-style surface.
 *
 * Discovery's result grid is Fairtrade's published `Explore` composite, which
 * has no helper slot; this mounts the server's grouped helper rows beneath that
 * grid instead, from the SAME route's opt-in grouped response. The flat grid
 * above is untouched, so nothing a visitor could reach is removed.
 *
 * A page whose grouped response carries no helper groups renders nothing at
 * all, so a commons without saved helpers looks exactly as it did.
 *
 * The section renders its items through ONE path. An owner row, an owner row
 * with saved helpers and a helper-only context container are each drawn once,
 * in server order; a second renderer for the same context would put two
 * independent controls on one group.
 */
export default function ScopedGroupedHelperSection({
  params,
}: {
  /** The active route filters. Their parsed values scope the grouped read; the
   *  member scopes themselves stay server-minted and opaque. */
  params: Record<string, string>;
}) {
  const grouped = useGroupedTranscripts(params);
  const items = grouped.data?.items ?? [];
  const hasDisclosures = items.some(
    (item) => item.kind === "context_container" || (item.helperGroups ?? []).length > 0,
  );
  if (grouped.isError || !hasDisclosures) return null;

  return (
    <div className="mt-4 flex flex-col gap-3" data-testid="grouped-helper-section">
      <p className="font-mono text-xs text-ink-3">saved helper threads</p>
      <div className="border border-rule bg-surface">
        <ScopedGroupedHelperRows items={items} onRefreshOrigin={grouped.refreshOrigin} />
      </div>
    </div>
  );
}
