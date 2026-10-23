"use client";

import { useId, useRef, type KeyboardEvent, type ReactNode } from "react";

export interface CollectiveTab<Id extends string> {
  id: Id;
  label: string;
  /** A count shown beside the label; left out while it is unknown. */
  count?: number;
  panel: ReactNode;
}

/**
 * The collective page's sections as a real tablist: one tab is selected and
 * in the tab order, the arrow keys move between tabs (Home and End jump), and
 * each panel is labelled by its tab.
 *
 * Every panel stays mounted and the unselected ones are `hidden`, so a panel
 * keeps what the reader did in it (an open disclosure, a filter) when they
 * come back, and nothing is fetched again.
 */
export default function CollectiveTabs<Id extends string>({
  tabs,
  selected,
  onSelect,
  label,
}: {
  tabs: CollectiveTab<Id>[];
  selected: Id;
  onSelect: (id: Id) => void;
  label: string;
}) {
  const base = useId();
  const refs = useRef(new Map<Id, HTMLButtonElement>());
  const tabId = (id: Id) => `${base}-tab-${id}`;
  const panelId = (id: Id) => `${base}-panel-${id}`;

  const move = (event: KeyboardEvent<HTMLButtonElement>, index: number) => {
    let next: number | null = null;
    if (event.key === "ArrowRight") next = (index + 1) % tabs.length;
    else if (event.key === "ArrowLeft") next = (index - 1 + tabs.length) % tabs.length;
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = tabs.length - 1;
    if (next === null) return;
    event.preventDefault();
    const target = tabs[next];
    onSelect(target.id);
    refs.current.get(target.id)?.focus();
  };

  return (
    <div className="on-ctabs">
      <div className="on-tabs" role="tablist" aria-label={label}>
        {tabs.map((tab, index) => {
          const isSelected = tab.id === selected;
          return (
            <button
              key={tab.id}
              ref={(node) => {
                if (node) refs.current.set(tab.id, node);
                else refs.current.delete(tab.id);
              }}
              type="button"
              role="tab"
              id={tabId(tab.id)}
              className="on-tab"
              aria-selected={isSelected}
              aria-controls={panelId(tab.id)}
              tabIndex={isSelected ? 0 : -1}
              onClick={() => onSelect(tab.id)}
              onKeyDown={(event) => move(event, index)}
            >
              {tab.label}
              {tab.count !== undefined && (
                <>
                  {" "}
                  <span className="on-k">{tab.count.toLocaleString("en-US")}</span>
                </>
              )}
            </button>
          );
        })}
      </div>
      {tabs.map((tab) => (
        <div
          key={tab.id}
          role="tabpanel"
          id={panelId(tab.id)}
          aria-labelledby={tabId(tab.id)}
          hidden={tab.id !== selected}
          className="on-tabpanel"
          data-testid={`collective-panel-${tab.id}`}
        >
          {tab.panel}
        </div>
      ))}
    </div>
  );
}
