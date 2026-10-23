"use client";

import { useRef, type KeyboardEvent, type ReactNode } from "react";

export interface Choice<T extends string> {
  value: T;
  label: string;
  help: string;
  icon: ReactNode;
}

const STEP: Record<string, number> = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 };

/**
 * One choice among a few, drawn as cards (`.on-opts` / `.on-opt`) and
 * announced as a radio group: one tab stop, arrow keys move and choose, Home
 * and End jump to the ends. Each card is named by its label and described by
 * its help line.
 */
export function ChoiceCards<T extends string>({
  label,
  labelId,
  value,
  onChange,
  choices,
  focusSelected = false,
}: {
  label: string;
  labelId: string;
  value: T;
  onChange: (next: T) => void;
  choices: readonly Choice<T>[];
  /** Marks the chosen card as the control a step focuses first. */
  focusSelected?: boolean;
}) {
  const refs = useRef<(HTMLButtonElement | null)[]>([]);
  const selected = Math.max(
    0,
    choices.findIndex((choice) => choice.value === value),
  );

  const onKeyDown = (event: KeyboardEvent<HTMLButtonElement>, from: number) => {
    let next: number;
    if (event.key in STEP) next = (from + STEP[event.key] + choices.length) % choices.length;
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = choices.length - 1;
    else return;
    event.preventDefault();
    onChange(choices[next].value);
    refs.current[next]?.focus();
  };

  return (
    <div className="on-field">
      <span className="on-label" id={labelId}>
        {label}
      </span>
      <div className="on-opts is-compact" role="radiogroup" aria-labelledby={labelId}>
        {choices.map((choice, index) => (
          <button
            key={choice.value}
            ref={(node) => {
              refs.current[index] = node;
            }}
            type="button"
            role="radio"
            aria-checked={choice.value === value}
            tabIndex={index === selected ? 0 : -1}
            data-autofocus={focusSelected && index === selected ? true : undefined}
            className="on-opt"
            onClick={() => onChange(choice.value)}
            onKeyDown={(event) => onKeyDown(event, index)}
            aria-labelledby={`${labelId}-${choice.value}`}
            aria-describedby={`${labelId}-${choice.value}-help`}
          >
            <span className="on-opt-ic" aria-hidden="true">
              {choice.icon}
            </span>
            <b id={`${labelId}-${choice.value}`}>{choice.label}</b>
            <span className="on-opt-help" id={`${labelId}-${choice.value}-help`}>
              {choice.help}
            </span>
          </button>
        ))}
      </div>
    </div>
  );
}
