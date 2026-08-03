"use client";

import { useState } from "react";
import { cn } from "@/lib/utils";
import { useGroups } from "@/lib/queries/groups";
import { useBulkShareTranscripts } from "@/lib/queries/transcripts";
import ConfirmContributeDialog from "@/components/transcript/ConfirmContributeDialog";

interface ContributePickerProps {
  open: boolean;
  onClose: () => void;
  transcriptId: string;
  transcriptTitle: string | null;
  transcriptVisibility: "private" | "public" | "shared";
}

/**
 * Contribute-to-collective flow. Lifted out of the (now deleted) local
 * `ActionMenu` so it can be mounted from the `SessionDetailV2` adapter and
 * triggered by the shared viewer's `onContribute` callback. Picks a collective,
 * confirms the visibility change for private transcripts, then runs the bulk
 * share mutation. Pure village app glue — no viewer code lives here.
 */
export default function ContributePicker({
  open,
  onClose,
  transcriptId,
  transcriptTitle,
  transcriptVisibility,
}: ContributePickerProps) {
  const { data: groups, isLoading } = useGroups();
  const share = useBulkShareTranscripts();
  const [selected, setSelected] = useState<string | null>(null);
  const [confirmation, setConfirmation] = useState<string | null>(null);
  const [confirmOpen, setConfirmOpen] = useState(false);

  function runShare() {
    if (!selected) return;
    share.mutate(
      { transcriptIds: [transcriptId], groupId: selected },
      {
        onSuccess: () => {
          const name =
            groups?.find((g) => g.id === selected)?.name ?? "collective";
          setConfirmOpen(false);
          setConfirmation(`Contributed to ${name}.`);
          setTimeout(() => {
            setConfirmation(null);
            setSelected(null);
            onClose();
          }, 1200);
        },
      },
    );
  }

  function submit() {
    if (!selected) return;
    if (transcriptVisibility === "private") {
      setConfirmOpen(true);
      return;
    }
    runShare();
  }

  const selectedGroup = groups?.find((g) => g.id === selected);

  if (!open) return null;

  return (
    <>
      <div
        className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-canvas/70 backdrop-blur-sm"
        role="dialog"
        aria-modal
        aria-label="Contribute to a collective"
        onClick={onClose}
      >
        <div
          onClick={(e) => e.stopPropagation()}
          className="w-[min(480px,100%)] bg-surface-elev border border-rule-strong shadow-2xl flex flex-col"
        >
          <header className="flex items-center justify-between gap-2 px-4 py-2.5 border-b border-rule">
            <h2 className="font-display text-[14px] font-semibold text-ink">
              Contribute to a collective
            </h2>
          </header>

          <div className="flex flex-col gap-2 p-4">
            {isLoading ? (
              <p className="text-[13px] text-ink-3">Loading collectives…</p>
            ) : !groups || groups.length === 0 ? (
              <p className="text-[13px] text-ink-3">
                You haven&apos;t joined any collectives yet.
              </p>
            ) : (
              <div className="border border-rule divide-y divide-rule max-h-72 overflow-y-auto">
                {groups.map((g) => (
                  <button
                    key={g.id}
                    type="button"
                    onClick={() => setSelected(g.id)}
                    className={cn(
                      "flex w-full items-center justify-between gap-2 px-3 py-2 text-left",
                      "focus-mono transition-colors cursor-pointer",
                      selected === g.id
                        ? "bg-mark text-mark-fg"
                        : "bg-surface text-ink hover:bg-surface-hover",
                    )}
                    aria-pressed={selected === g.id}
                  >
                    <span className="min-w-0 flex-1 truncate text-[13px] font-medium">
                      {g.name}
                    </span>
                    <span
                      className={cn(
                        "font-mono text-[10.5px]",
                        selected === g.id ? "opacity-80" : "text-ink-3",
                      )}
                    >
                      {g.role}
                    </span>
                  </button>
                ))}
              </div>
            )}

            {share.isError && (
              <p className="text-[12px] text-danger">
                Could not contribute. Try again.
              </p>
            )}
            {confirmation && (
              <p className="text-[12px] text-success">{confirmation}</p>
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
              type="button"
              onClick={submit}
              disabled={!selected || share.isPending || !!confirmation}
              className={cn(
                "inline-flex items-center gap-1.5 px-2.5 h-8 text-[13px] font-medium",
                "bg-mark text-mark-fg border border-mark",
                "hover:opacity-90 focus-mono transition-opacity cursor-pointer",
                "disabled:opacity-60 disabled:cursor-not-allowed",
              )}
            >
              {share.isPending ? "Contributing…" : "Contribute"}
            </button>
          </footer>
        </div>
      </div>
      <ConfirmContributeDialog
        open={confirmOpen}
        onClose={() => setConfirmOpen(false)}
        onConfirm={runShare}
        transcripts={[{ id: transcriptId, title: transcriptTitle ?? "Untitled" }]}
        collectives={
          selectedGroup
            ? [{ id: selectedGroup.id, name: selectedGroup.name }]
            : []
        }
        isSubmitting={share.isPending}
      />
    </>
  );
}
