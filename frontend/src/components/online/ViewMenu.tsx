"use client";

import { useCallback, useEffect, useId, useRef, useState, type ReactNode } from "react";
import { Check } from "lucide-react";

/**
 * A toolbar button that opens a small panel of view choices: which projects to
 * show, how to group, which columns. It changes only what this browser shows,
 * never what the server holds.
 *
 * The panel is a disclosure, not an ARIA menu: its rows are real checkboxes or
 * radios, so a screen reader announces each one's state, and Tab moves through
 * them in order. Escape or a click outside closes it and returns focus to the
 * button.
 */
export function ViewMenu({
  icon,
  label,
  value,
  title,
  align = "start",
  kind = "group",
  children,
  testId,
}: {
  icon?: ReactNode;
  /** The button's word, e.g. "projects". */
  label: string;
  /** The current choice shown on the button, e.g. "all". */
  value?: string;
  /** The panel's own heading. */
  title: string;
  align?: "start" | "end";
  /** `radiogroup` when exactly one row is chosen (the rows are {@link RadioRow}). */
  kind?: "group" | "radiogroup";
  children: (close: () => void) => ReactNode;
  testId?: string;
}) {
  const [open, setOpen] = useState(false);
  const boxRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const panelId = useId();
  const titleId = useId();

  useEffect(() => {
    if (!open) return;
    const onDown = (event: PointerEvent) => {
      if (boxRef.current && !boxRef.current.contains(event.target as Node)) setOpen(false);
    };
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    document.addEventListener("pointerdown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("pointerdown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const close = useCallback(() => setOpen(false), []);

  // Closing from inside the panel (a choice, or Escape) unmounts the row that
  // had focus, so focus goes back to the button. A click elsewhere keeps the
  // focus where the person put it.
  const wasOpen = useRef(false);
  useEffect(() => {
    if (wasOpen.current && !open) {
      const active = document.activeElement;
      if (active == null || active === document.body || boxRef.current?.contains(active)) {
        buttonRef.current?.focus();
      }
    }
    wasOpen.current = open;
  }, [open]);

  return (
    <div className={align === "end" ? "on-ctl is-end" : "on-ctl"} ref={boxRef} data-testid={testId}>
      <button
        ref={buttonRef}
        type="button"
        className="btn btn-secondary"
        aria-expanded={open}
        aria-controls={panelId}
        onClick={() => setOpen((was) => !was)}
      >
        {icon}
        {value === undefined ? (
          label
        ) : (
          <span>
            {label}: <b>{value}</b>
          </span>
        )}
      </button>
      {open && (
        <div className="on-menu" id={panelId} role={kind} aria-labelledby={titleId}>
          <span className="on-mlabel" id={titleId}>
            {title}
          </span>
          {children(close)}
        </div>
      )}
    </div>
  );
}

/** A square mark for a check row: empty, ticked, or partly ticked. */
export function CheckMark({ state }: { state: "off" | "on" | "some" }) {
  return (
    <span className="on-cb" data-state={state} aria-hidden="true">
      {state === "on" && <Check strokeWidth={3} />}
    </span>
  );
}

/** One row of a panel that can be ticked on or off. */
export function CheckRow({
  checked,
  onToggle,
  children,
  count,
}: {
  checked: boolean;
  onToggle: () => void;
  children: ReactNode;
  count?: number;
}) {
  return (
    <button type="button" className="on-mi" role="checkbox" aria-checked={checked} onClick={onToggle}>
      <CheckMark state={checked ? "on" : "off"} />
      <span className="on-mi-label">{children}</span>
      {count !== undefined && <span className="on-k">{count.toLocaleString("en-US")}</span>}
    </button>
  );
}

/** One row of a panel where exactly one row is chosen. */
export function RadioRow({
  checked,
  onPick,
  children,
}: {
  checked: boolean;
  onPick: () => void;
  children: ReactNode;
}) {
  return (
    <button type="button" className="on-mi" role="radio" aria-checked={checked} onClick={onPick}>
      <span className="on-radio" data-state={checked ? "on" : "off"} aria-hidden="true" />
      <span className="on-mi-label">{children}</span>
    </button>
  );
}
