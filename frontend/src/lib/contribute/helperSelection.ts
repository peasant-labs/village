"use client";

import { useCallback, useMemo, useState } from "react";
import type { VillageSessionListItem } from "@peasant-labs/schema";
import type { ScopedHelperSelection } from "@/components/transcript/ScopedHelperGroups";

/**
 * The explicit per-helper selection one collective surface owns.
 *
 * The host reports the exact display item a member toggled, so this state keeps
 * the ROW beside its id: a contribution batch or a review decision is built
 * from the route arm the member endpoint served, never from a re-looked-up id
 * that a later page could reinterpret. Nothing here infers a sibling, a parent
 * or a whole group from one pick, and there is no group-level control.
 */
export function helperItemID(item: VillageSessionListItem): string | null {
  return item.transcript?.session.id ?? null;
}

export interface ExplicitHelperSelection {
  /** The selected rows, keyed by the transcript id a mutation names. */
  selected: ReadonlyMap<string, VillageSessionListItem>;
  selectedIds: ReadonlySet<string>;
  /** The contract the helper-group host renders and reports through. */
  contract: ScopedHelperSelection;
  /** Drop ids a completed action answered, so they cannot be resent. */
  forget: (ids: Iterable<string>) => void;
}

export function useExplicitHelperSelection(
  isDisabled?: (item: VillageSessionListItem) => boolean,
): ExplicitHelperSelection {
  const [selected, setSelected] = useState<Map<string, VillageSessionListItem>>(new Map());

  const onToggle = useCallback((item: VillageSessionListItem, nextSelected: boolean) => {
    const id = helperItemID(item);
    if (id == null) return;
    setSelected((prev) => {
      const next = new Map(prev);
      if (nextSelected) next.set(id, item);
      else next.delete(id);
      return next;
    });
  }, []);

  const selectedIds = useMemo(() => new Set(selected.keys()), [selected]);

  const forget = useCallback((ids: Iterable<string>) => {
    const drop = new Set(ids);
    setSelected((prev) => {
      let changed = false;
      const next = new Map(prev);
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

  return { selected, selectedIds, contract, forget };
}
