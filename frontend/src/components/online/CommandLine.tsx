"use client";

import { useEffect, useId, useState } from "react";

/**
 * One terminal command with a copy button beside it. The button says what
 * happened: "copied", or "copy failed" when the browser refused the clipboard,
 * so a press is never silent.
 */
export default function CommandLine({ command }: { command: string }) {
  const [state, setState] = useState<"idle" | "copied" | "failed">("idle");
  const commandId = useId();

  useEffect(() => {
    if (state === "idle") return;
    const timer = setTimeout(() => setState("idle"), 2000);
    return () => clearTimeout(timer);
  }, [state]);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(command);
      setState("copied");
    } catch {
      setState("failed");
    }
  };

  return (
    <div className="on-cmdline">
      <code id={commandId}>{command}</code>
      {/* The button says what it does; the command it copies is its description. */}
      <button type="button" className="btn btn-secondary" onClick={() => void copy()} aria-describedby={commandId}>
        <span aria-live="polite">{state === "copied" ? "copied" : state === "failed" ? "copy failed" : "copy"}</span>
      </button>
    </div>
  );
}
