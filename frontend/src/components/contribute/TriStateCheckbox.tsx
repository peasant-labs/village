"use client";

import { useEffect, useRef } from "react";

export type TriState = "none" | "some" | "all";

/**
 * A parent row's checkbox carries three states: none, some, or all of its
 * selectable descendants. "some" is drawn as the filled square with a dash
 * (the native indeterminate face styled in `globals.css`), so partial
 * selection reads as a distinct SHAPE and never rides on colour alone.
 */
export default function TriStateCheckbox({
  state,
  disabled = false,
  onChange,
  label,
  inputRef,
}: {
  state: TriState;
  disabled?: boolean;
  onChange: () => void;
  label: string;
  inputRef?: (element: HTMLInputElement | null) => void;
}) {
  const ref = useRef<HTMLInputElement | null>(null);
  const mixed = state === "some";
  useEffect(() => {
    if (ref.current) ref.current.indeterminate = mixed;
  }, [mixed]);
  return (
    <label className="check contribute-check">
      <input
        ref={(element) => {
          ref.current = element;
          inputRef?.(element);
        }}
        type="checkbox"
        className="check-box"
        data-state={state}
        data-indeterminate={mixed || undefined}
        aria-checked={mixed ? "mixed" : state === "all"}
        checked={state === "all"}
        disabled={disabled}
        onChange={onChange}
        aria-label={label}
      />
      {mixed && <span className="contribute-check__mixed" aria-hidden="true" />}
    </label>
  );
}
