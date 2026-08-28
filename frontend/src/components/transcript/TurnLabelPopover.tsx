"use client";

import { useCallback, useEffect, useId, useRef, useState } from "react";
import { Tag } from "lucide-react";
import { type TurnLabel } from "@peasant-labs/fairtrade/ui";
import { Button, Select } from "@/lib/ft-ui";
import { ANNOTATION_TYPES, annotationTypeName } from "@/lib/annotations";

interface TurnLabelPopoverProps {
  /** The entry index this label targets (`turn.index`). */
  entryIndex: number;
  /**
   * Persist the chosen label. Mirrors the package's `ViewerCallbacks.onLabelSave`
   * — returns the saved `TurnLabel` (with server id) on success.
   */
  onSave: (label: TurnLabel) => void | Promise<TurnLabel>;
}

/**
 * Village-owned per-turn label control, mounted into the shared viewer via
 * `<SessionDetail renderTurnActions={...}>`. The package ships no labelling UI of
 * its own; this is the app-specific annotation surface kept OUT of the package.
 *
 * It keeps village's original single-panel form — a label-type select + a value
 * select + Save — and lets a signed-in viewer POST the choice through village's
 * `onSave` callback (wired to `useCreateTranscriptAnnotation`).
 *
 * The floating panel is a local composition over the design system's popover
 * chrome (`.tip-anchor` / `.pop-card`, tokens-only, square + hairline) rather
 * than the design system's `Popover` component: this picker is *controlled* — it
 * must close programmatically after an async save and reset on dismissal —
 * behaviour the props-driven `Popover` does not expose. It is a candidate to
 * fold back into the shared design system if a controlled popover primitive
 * ships there. Dismissal parity is preserved: Escape (returns focus to the
 * trigger) and an outside click both close the picker.
 */
export default function TurnLabelPopover({
  entryIndex,
  onSave,
}: TurnLabelPopoverProps) {
  const [open, setOpen] = useState(false);
  const [typeId, setTypeId] = useState(ANNOTATION_TYPES[0]?.typeId ?? "");
  const [value, setValue] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const popId = useId();
  const anchorRef = useRef<HTMLSpanElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);

  const closePopover = useCallback(() => {
    setOpen(false);
    setError(null);
    setSaving(false);
  }, []);

  const type = ANNOTATION_TYPES.find((t) => t.typeId === typeId);

  // Outside-click + Escape (with focus-return) dismissal — the same effect the
  // design system's Popover ships (fairtrade src/ui/Tooltip.jsx), so this local
  // controlled rebuild dismisses identically to the stock popover.
  useEffect(() => {
    if (!open) return;
    const onDocDown = (e: MouseEvent) => {
      if (anchorRef.current && !anchorRef.current.contains(e.target as Node)) {
        closePopover();
      }
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        closePopover();
        triggerRef.current?.focus();
      }
    };
    document.addEventListener("mousedown", onDocDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDocDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [closePopover, open]);

  async function submit() {
    if (!typeId || !value || saving) return;
    setSaving(true);
    setError(null);
    try {
      await onSave({
        entryIndex,
        typeId,
        typeName: annotationTypeName(typeId),
        value,
        id: "",
      });
      setValue("");
      closePopover();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save label.");
    } finally {
      setSaving(false);
    }
  }

  return (
    <span className="tip-anchor" ref={anchorRef}>
      <button
        ref={triggerRef}
        type="button"
        aria-label="Add label"
        title="Add label"
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-controls={open ? popId : undefined}
        onClick={() => (open ? closePopover() : setOpen(true))}
        className="inline-flex items-center justify-center p-1 text-ink-3 hover:text-ink hover:bg-surface-hover border border-transparent hover:border-rule focus-mono transition-colors"
      >
        <Tag size={13} strokeWidth={1.75} aria-hidden />
      </button>
      {open && (
        <div
          className="pop-card"
          role="dialog"
          aria-label="Add a label"
          id={popId}
          // Right-align under the trigger, at the prior popover width.
          style={{ left: "auto", right: 0, width: "16rem" }}
        >
          <div className="flex flex-col gap-2 p-2.5">
            <Select
              aria-label="Label type"
              value={typeId}
              onChange={(e: React.ChangeEvent<HTMLSelectElement>) => {
                setTypeId(e.target.value);
                setValue("");
                setError(null);
              }}
            >
              {ANNOTATION_TYPES.map((t) => (
                <option key={t.typeId} value={t.typeId}>
                  {t.typeName}
                </option>
              ))}
            </Select>

            <Select
              aria-label="Value"
              value={value}
              onChange={(e: React.ChangeEvent<HTMLSelectElement>) => {
                setValue(e.target.value);
                setError(null);
              }}
            >
              <option value="">Value</option>
              {(type?.values ?? []).map((v) => (
                <option key={v.value} value={v.value}>
                  {v.label}
                </option>
              ))}
            </Select>

            {error && (
              <span className="text-[12px] text-danger" role="alert">
                {error}
              </span>
            )}

            <Button
              variant="primary"
              size="sm"
              loading={saving}
              onClick={submit}
              disabled={!typeId || !value || saving}
              className="self-end"
            >
              Save label
            </Button>
          </div>
        </div>
      )}
    </span>
  );
}
