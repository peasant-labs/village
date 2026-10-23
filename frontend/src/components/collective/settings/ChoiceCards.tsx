"use client";

import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { CircleAlert, CircleCheck, LoaderCircle } from "lucide-react";
import type { PolicyCard } from "@/lib/adapters/collective";

type Status = "idle" | "pending" | "settled" | "failed";

const STATUS_WORD: Record<Exclude<Status, "idle">, string> = {
  pending: "saving",
  settled: "saved",
  failed: "not saved",
};

function failureText(error: unknown): string {
  if (error instanceof Error && error.message) return error.message;
  if (typeof error === "string" && error) return error;
  return "the change could not be saved.";
}

/**
 * One setting chosen from a few cards, saved the moment a card is pressed:
 * the same promise SettingRow keeps (saving, then saved; on a refusal the
 * card the collective still holds is pressed again and the server's words are
 * shown under the cards).
 *
 * The cards are toggle buttons rather than radios on purpose: arrowing through
 * a radio group checks each radio it passes, and here every check is a write.
 */
export default function ChoiceCards({
  label,
  value,
  choices,
  icons,
  onCommit,
}: {
  /** The setting's name, its heading and the group's name. */
  label: string;
  /** The value the collective holds. */
  value: string;
  choices: PolicyCard[];
  icons: Record<string, ReactNode>;
  /** The one write; a rejection puts the held value back. */
  onCommit: (next: string) => Promise<unknown>;
}) {
  const headingId = useId();
  const errorId = useId();
  const [shown, setShown] = useState(value);
  const [status, setStatus] = useState<Status>("idle");
  const [error, setError] = useState<string | null>(null);
  const mounted = useRef(true);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  // The server's value wins whenever it changes (a refetch, another tab).
  const [held, setHeld] = useState(value);
  if (held !== value) {
    setHeld(value);
    setShown(value);
  }

  const choose = async (next: string) => {
    if (next === shown || status === "pending") return;
    const before = shown;
    setShown(next);
    setStatus("pending");
    setError(null);
    try {
      await onCommit(next);
      if (mounted.current) setStatus("settled");
    } catch (failure) {
      if (!mounted.current) return;
      setShown(before);
      setStatus("failed");
      setError(failureText(failure));
    }
  };

  const Icon = status === "pending" ? LoaderCircle : status === "settled" ? CircleCheck : CircleAlert;

  return (
    <section className="on-choices" aria-labelledby={headingId}>
      <div className="on-choices-head">
        <h2 className="on-h2" id={headingId}>
          {label}
        </h2>
        <span className="on-choice-status" data-status={status} aria-live="polite">
          {status !== "idle" && (
            <>
              <Icon aria-hidden="true" />
              <span>{STATUS_WORD[status]}</span>
            </>
          )}
        </span>
      </div>
      <div
        className="on-opts"
        role="group"
        aria-labelledby={headingId}
        aria-describedby={error ? errorId : undefined}
        aria-busy={status === "pending" || undefined}
      >
        {choices.map((choice) => {
          const pressed = choice.value === shown;
          return (
            <button
              key={choice.value}
              type="button"
              className="on-opt"
              aria-pressed={pressed}
              aria-busy={(pressed && status === "pending") || undefined}
              disabled={status === "pending" && !pressed}
              data-value={choice.value}
              onClick={() => void choose(choice.value)}
            >
              <span className="on-ico-tile" aria-hidden="true">
                {icons[choice.value]}
              </span>
              <b>{choice.label}</b>
              <p>{choice.help}</p>
              {choice.note && <span className="on-for">{choice.note}</span>}
            </button>
          );
        })}
      </div>
      {status === "failed" && error && (
        <p className="on-error" id={errorId} role="alert">
          {error}
        </p>
      )}
    </section>
  );
}
