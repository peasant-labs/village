"use client";

import { useEffect, useState, useSyncExternalStore } from "react";
import { createPortal } from "react-dom";
import { Check, Copy, X } from "lucide-react";
import { cn } from "@/lib/utils";

interface PublishImportDialogProps {
  open: boolean;
  onClose: () => void;
}

interface ImportStep {
  title: string;
  command: string;
  description: React.ReactNode;
}

const STEPS: ImportStep[] = [
  {
    title: "Install the Peasant CLI",
    command: "go install github.com/peasant-labs/peasant/cmd/peasant@latest",
    description:
      "The CLI scans your local agent transcript stores (Claude Code, OpenCode, Codex, etc.) and pushes them to the village.",
  },
  {
    title: "Sign in to the village",
    command: "peasant login",
    description:
      "Opens your browser, completes the GitHub OAuth flow, and stores credentials at ~/.config/peasant/credentials.json.",
  },
  {
    title: "Run the setup wizard",
    command: "peasant kickstart",
    description:
      "Discovers agent transcripts on your machine, configures providers, and sets your default redaction level.",
  },
  {
    title: "Push transcripts",
    command: "peasant village push",
    description:
      "Pushes unpublished transcripts with automatic redaction. Add --dry-run to preview, or --visibility public to override the default.",
  },
];

const subscribeToBrowser = () => () => {};

export default function PublishImportDialog({
  open,
  onClose,
}: PublishImportDialogProps) {
  const mounted = useSyncExternalStore(
    subscribeToBrowser,
    () => true,
    () => false,
  );

  useEffect(() => {
    if (!open) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open || !mounted) return null;

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-canvas/70 backdrop-blur-sm"
      role="dialog"
      aria-modal
      aria-label="How to import transcripts"
      onClick={onClose}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        className="w-[min(640px,100%)] max-h-[85vh] overflow-y-auto bg-surface-elev border border-rule-strong shadow-2xl flex flex-col"
      >
        <header className="flex items-center justify-between gap-2 px-4 py-2.5 border-b border-rule">
          <h2 className="font-display text-[14px] font-semibold text-ink">
            How to import transcripts
          </h2>
          <button
            type="button"
            onClick={onClose}
            className="p-1 text-ink-3 hover:text-ink focus-mono cursor-pointer"
            aria-label="Close"
          >
            <X size={14} strokeWidth={1.75} />
          </button>
        </header>

        <div className="flex flex-col gap-5 p-5">
          <p className="text-[13px] text-ink-3 leading-relaxed">
            Transcripts are imported into the village via the Peasant CLI — not
            through the web UI. The CLI reads your local agent transcript
            stores, redacts sensitive content, and pushes the result here.
          </p>

          <div className="flex flex-col gap-5">
            {STEPS.map((step, idx) => (
              <Step key={step.title} index={idx + 1} step={step} />
            ))}
          </div>
        </div>

        <footer className="flex items-center justify-end gap-2 px-4 py-2.5 border-t border-rule">
          <button
            type="button"
            onClick={onClose}
            className={cn(
              "inline-flex items-center gap-1.5 px-2.5 h-8 text-[13px] font-medium",
              "border border-rule bg-surface text-ink",
              "hover:bg-surface-hover focus-mono transition-colors cursor-pointer",
            )}
          >
            Close
          </button>
        </footer>
      </div>
    </div>,
    document.body,
  );
}

function Step({ index, step }: { index: number; step: ImportStep }) {
  return (
    <div className="flex gap-4">
      <span
        className="flex size-7 shrink-0 items-center justify-center bg-mark text-mark-fg text-[13px] font-semibold tabular-nums"
        aria-hidden="true"
      >
        {index}
      </span>
      <div className="min-w-0 flex-1 space-y-2">
        <h3 className="text-sm font-medium text-ink">{step.title}</h3>
        <CommandBlock command={step.command} />
        <p className="text-[13px] text-ink-3 leading-relaxed">
          {step.description}
        </p>
      </div>
    </div>
  );
}

function CommandBlock({ command }: { command: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="flex items-stretch border border-rule bg-code-bg">
      <pre className="flex-1 min-w-0 overflow-x-auto px-3 py-2.5 text-[13px] font-mono text-ink">
        <code>
          <span className="select-none text-ink-4">$ </span>
          {command}
        </code>
      </pre>
      <div className="flex items-center border-l border-rule px-2">
        <button
          type="button"
          onClick={() => {
            navigator.clipboard.writeText(command);
            setCopied(true);
            setTimeout(() => setCopied(false), 2000);
          }}
          aria-label={copied ? "Copied" : "Copy to clipboard"}
          className={cn(
            "inline-flex items-center gap-1.5 px-2 py-1 text-xs font-medium",
            "border border-transparent transition-colors focus-mono cursor-pointer",
            copied
              ? "text-success border-success/30 bg-success-soft"
              : "text-ink-3 hover:text-ink hover:bg-surface-hover",
          )}
        >
          {copied ? (
            <Check className="size-3.5" />
          ) : (
            <Copy className="size-3.5" />
          )}
          {copied ? "Copied" : "Copy"}
        </button>
      </div>
    </div>
  );
}
