"use client";

import { useCallback, useMemo, useState } from "react";
import type { VillageSessionListItem } from "@peasant-labs/schema";
import type { ScopedHelperSelection } from "@/components/transcript/ScopedHelperGroups";

/**
 * The ONE identity-level selection a collective surface owns.
 *
 * A collective route draws the same transcript twice: as an ordinary tree row
 * and, when the server grouped it, as a member inside a helper disclosure. Both
 * surfaces therefore read and write the SAME set of transcript ids — one
 * identity is one selection, counted once, and its checkboxes on either surface
 * state the same thing. Ticking it anywhere selects it everywhere; clearing it
 * anywhere clears it everywhere. A helper disclosure additionally keeps the
 * exact display item it was ticked from, because a contribution batch or a
 * review decision is built from the route arm (`contributable`/`pending`) the
 * member endpoint served, never from a re-looked-up row a later page could
 * reinterpret.
 *
 * Nothing here infers a sibling, the owner, or a whole group from one pick, and
 * there is no group-level control.
 */
export function helperItemID(item: VillageSessionListItem): string | null {
  return item.transcript?.session.id ?? null;
}

export interface CollectiveIdentitySelection {
  /** Every selected transcript identity once, whichever surface picked it. */
  selectedIds: ReadonlySet<string>;
  /** The disclosure row behind each identity picked inside a helper group. */
  helperItems: ReadonlyMap<string, VillageSessionListItem>;
  /** Adopt the identity set a flat-tree node or select-all change produced. */
  applyTreeSelection: (next: ReadonlySet<string>) => void;
  /** The contract both helper disclosures render. */
  contract: ScopedHelperSelection;
  /** Drop ids a completed action answered, so they cannot be resent. */
  forget: (ids: Iterable<string>) => void;
}

export function useCollectiveIdentitySelection(
  isDisabled?: (item: VillageSessionListItem) => boolean,
): CollectiveIdentitySelection {
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [helperItems, setHelperItems] = useState<Map<string, VillageSessionListItem>>(new Map());

  const onToggle = useCallback((item: VillageSessionListItem, nextSelected: boolean) => {
    const id = helperItemID(item);
    if (id == null) return;
    setHelperItems((prev) => {
      const next = new Map(prev);
      if (nextSelected) next.set(id, item);
      else next.delete(id);
      return next;
    });
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (nextSelected) next.add(id);
      else next.delete(id);
      return next;
    });
  }, []);

  const applyTreeSelection = useCallback((next: ReadonlySet<string>) => {
    setSelectedIds(new Set(next));
    setHelperItems((prev) => {
      // A helper disclosure keeps its route-arm row only while its identity
      // stays selected, so clearing it on the tree cannot leave a helper tick
      // behind to be counted or sent.
      let changed = false;
      const kept = new Map<string, VillageSessionListItem>();
      for (const [id, item] of prev) {
        if (next.has(id)) kept.set(id, item);
        else changed = true;
      }
      return changed ? kept : prev;
    });
  }, []);

  const forget = useCallback((ids: Iterable<string>) => {
    const drop = new Set(ids);
    setHelperItems((prev) => {
      let changed = false;
      const next = new Map(prev);
      for (const id of drop) {
        if (next.delete(id)) changed = true;
      }
      return changed ? next : prev;
    });
    setSelectedIds((prev) => {
      let changed = false;
      const next = new Set(prev);
      for (const id of drop) {
        if (next.delete(id)) changed = true;
      }
      return changed ? next : prev;
    });
  }, []);

  const contract = useMemo<ScopedHelperSelection>(
    () => ({ selectedIds, onToggle, isDisabled }),
    [selectedIds, onToggle, isDisabled],
  );

  return { selectedIds, helperItems, applyTreeSelection, contract, forget };
}
