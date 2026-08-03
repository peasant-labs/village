"use client";

import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { createPortal } from "react-dom";
import { Globe, Lock, X } from "lucide-react";
import { cn } from "@/lib/utils";
import { useUpdateTranscript } from "@/lib/queries/transcripts";
import { ApiError } from "@/lib/api";

type Visibility = "private" | "public" | "shared";

interface TranscriptEditDialogProps {
  open: boolean;
  onClose: () => void;
  transcriptId: string;
  initialTitle: string | null;
  initialVisibility: Visibility;
  initialDescription?: string | null;
}

const subscribeToBrowser = () => () => {};

export default function TranscriptEditDialog(props: TranscriptEditDialogProps) {
  // Mount the form only while the dialog is open, so its useState initializers
  // re-capture the initial values whenever the dialog reopens (for a
  // potentially different transcript) without needing an effect-driven reset.
  if (!props.open) return null;
  return <DialogBody {...props} />;
}

function DialogBody({
  onClose,
  transcriptId,
  initialTitle,
  initialVisibility,
}: TranscriptEditDialogProps) {
  const [title, setTitle] = useState(initialTitle ?? "");
  const [visibility, setVisibility] = useState<Visibility>(initialVisibility);
  const mounted = useSyncExternalStore(
    subscribeToBrowser,
    () => true,
    () => false,
  );
  const firstFieldRef = useRef<HTMLInputElement>(null);
  const update = useUpdateTranscript();

  useEffect(() => {
    requestAnimationFrame(() => firstFieldRef.current?.select());
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  function submit(e: React.FormEvent) {
    e.preventDefault();
    update.mutate(
      {
        id: transcriptId,
        title,
        // 'shared' is a server-managed state — only forward 'public'/'private'.
        ...(visibility === "shared" ? {} : { visibility }),
      },
      { onSuccess: () => onClose() },
    );
  }

  if (!mounted) return null;

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-canvas/70 backdrop-blur-sm"
      role="dialog"
      aria-modal
      aria-label="Edit transcript"
      onClick={onClose}
    >
      <form
        onClick={(e) => e.stopPropagation()}
        onSubmit={submit}
        className="w-[min(520px,100%)] bg-surface-elev border border-rule-strong shadow-2xl flex flex-col"
      >
        <header className="flex items-center justify-between gap-2 px-4 py-2.5 border-b border-rule">
          <h2 className="font-display text-[14px] font-semibold text-ink">Edit transcript</h2>
          <button
            type="button"
            onClick={onClose}
            className="p-1 text-ink-3 hover:text-ink focus-mono cursor-pointer"
            aria-label="Close"
          >
            <X size={14} strokeWidth={1.75} />
          </button>
        </header>

        <div className="flex flex-col gap-3 p-4">
          <div>
            <label className="block v2-eyebrow mb-1.5">Title</label>
            <input
              ref={firstFieldRef}
              type="text"
              value={title}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => setTitle(e.target.value)}
              placeholder="Untitled"
              className={cn(
                "w-full px-2 py-1.5 text-[13px]",
                "bg-canvas border border-rule text-ink focus-mono",
              )}
            />
          </div>

          <div>
            <label className="block v2-eyebrow mb-1.5">Visibility</label>
            <div className="flex items-stretch border border-rule">
              <VisibilityButton
                active={visibility === "private"}
                onClick={() => setVisibility("private")}
                icon={<Lock size={12} strokeWidth={1.75} />}
                label="Private"
                desc="Only you can view"
              />
              <VisibilityButton
                active={visibility === "public"}
                onClick={() => setVisibility("public")}
                icon={<Globe size={12} strokeWidth={1.75} />}
                label="Public"
                desc="Anyone with the link"
              />
            </div>
            {initialVisibility === "shared" && visibility === "shared" && (
              <p className="mt-1.5 text-[11.5px] text-ink-3">
                Shared with one or more collectives. Choose Private or Public to
                override.
              </p>
            )}
          </div>

          {update.isError && (
            <p className="text-[12px] text-danger" role="alert">
              {update.error instanceof ApiError && update.error.status === 422 && update.error.message
                ? update.error.message
                : "Could not save changes. Try again."}
            </p>
          )}
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
            Cancel
          </button>
          <button
            type="submit"
            disabled={update.isPending}
            className={cn(
              "inline-flex items-center gap-1.5 px-2.5 h-8 text-[13px] font-medium",
              "bg-mark text-mark-fg border border-mark",
              "hover:opacity-90 focus-mono transition-opacity cursor-pointer",
              "disabled:opacity-60 disabled:cursor-not-allowed",
            )}
          >
            {update.isPending ? "Saving…" : "Save"}
          </button>
        </footer>
      </form>
    </div>,
    document.body,
  );
}

function VisibilityButton({
  active,
  onClick,
  icon,
  label,
  desc,
}: {
  active: boolean;
  onClick: () => void;
  icon: React.ReactNode;
  label: string;
  desc: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={cn(
        "flex-1 flex flex-col items-start gap-0.5 p-2.5 text-left",
        "focus-mono transition-colors cursor-pointer",
        active
          ? "bg-mark text-mark-fg"
          : "bg-surface text-ink hover:bg-surface-hover",
      )}
    >
      <span className="inline-flex items-center gap-1.5 text-[12.5px] font-medium">
        {icon}
        {label}
      </span>
      <span className={cn("text-[11px]", active ? "opacity-80" : "text-ink-3")}>
        {desc}
      </span>
    </button>
  );
}
