"use client";

// Re-wired after the user chose to restore this feature rather than retire it.
// Mounted in SessionDetailV2 (see that file), alongside the
// package's other host-owned actions. DS-polished on restoration: lowercase chrome
// throughout (attestation-type option labels are system copy, not user content -- same
// convention RoleRoster's role labels and Manage.jsx's Select options already follow),
// composed from the DS Button + Select (via @/lib/ft-ui, never the package directly).
//
// The open state is a FLOATING popover anchored to the trigger (`.tip-anchor` / `.pop-card`,
// fairtrade src/index.css -- tokens-only, square, hairline), not an in-flow panel: the
// original in-place version reflowed the whole SessionDetail viewer downward and left an
// empty band to its left when open. This reuses the exact same anchored-popover chrome +
// controlled dismissal (outside-click, Escape-returns-focus) TurnLabelPopover already
// established for the per-turn label picker -- not a hand-rolled position, and not the DS's
// stock `Popover` component either, for the identical reason TurnLabelPopover isn't: this
// picker is *controlled* (must close programmatically after an async submit, and reset its
// transient error/pending state on dismiss), which the props-driven `Popover` doesn't expose.

import { useCallback, useEffect, useId, useRef, useState } from "react";
import { ShieldCheck } from "lucide-react";
import { Button, Select } from "@/lib/ft-ui";
import { useMyOrgs } from "@/lib/queries/orgs";
import { useCreateAttestation } from "@/lib/queries/attestations";

const attestationTypes = [
  { value: "used_in_training", label: "used in training" },
  { value: "referenced", label: "referenced" },
  { value: "evaluated", label: "evaluated" },
  { value: "deployed", label: "deployed" },
];

export default function AttestButton({ transcriptId }: { transcriptId: string }) {
  const { data: orgs } = useMyOrgs();
  const createAttestation = useCreateAttestation();
  const [open, setOpen] = useState(false);
  const [selectedOrg, setSelectedOrg] = useState("");
  const [selectedType, setSelectedType] = useState("");
  const popId = useId();
  const anchorRef = useRef<HTMLSpanElement>(null);

  const closePopover = useCallback(() => {
    setOpen(false);
    setSelectedOrg("");
    setSelectedType("");
  }, []);

  // Outside-click + Escape (with focus-return) dismissal -- the same effect the design
  // system's Popover ships (fairtrade src/ui/Tooltip.jsx) and TurnLabelPopover mirrors.
  // The trigger is the DS <Button> (a plain, non-forwardRef function component per
  // fairtrade src/ui/Button.jsx), so focus-return queries the anchor's own <button>
  // rather than holding a ref directly on <Button>.
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
        anchorRef.current?.querySelector<HTMLButtonElement>(":scope > button")?.focus();
      }
    };
    document.addEventListener("mousedown", onDocDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDocDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [closePopover, open]);

  // Only show visible orgs
  const visibleOrgs = orgs?.filter((o) => o.visible) ?? [];
  if (visibleOrgs.length === 0) return null;

  const handleSubmit = () => {
    if (!selectedOrg || !selectedType) return;
    createAttestation.mutate(
      { transcriptId, org_login: selectedOrg, attestation_type: selectedType },
      { onSuccess: closePopover }
    );
  };

  return (
    <span className="tip-anchor" ref={anchorRef}>
      <Button
        variant="secondary"
        size="sm"
        icon={ShieldCheck}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-controls={open ? popId : undefined}
        onClick={() => (open ? closePopover() : setOpen(true))}
      >
        attest
      </Button>

      {open && (
        <div
          className="pop-card"
          role="dialog"
          aria-label="new attestation"
          id={popId}
          // Right-align under the trigger (the trigger sits at the page's right edge; a
          // left-anchored popover would overflow the viewport), matching the same
          // right:0 override TurnLabelPopover uses for its own right-edge trigger.
          style={{ left: "auto", right: 0 }}
        >
          <div className="flex items-center gap-2 px-5 py-3 border-b border-rule">
            <ShieldCheck size={14} strokeWidth={1.75} className="text-ink-2" />
            {/* mono, lowercase eyebrow -- matches the DS's own eyebrow convention
                (fairtrade src/ui/SignIn.jsx .si-eyebrow: font-mono, lowercase, ink-3),
                not village's local .v2-eyebrow (uppercase, off-DS for this composition). */}
            <span className="font-mono text-[11px] tracking-wide text-ink-3">new attestation</span>
          </div>

          <div className="px-5 py-4 flex flex-col gap-4">
            <Select
              label="organization"
              value={selectedOrg}
              onChange={(e: React.ChangeEvent<HTMLSelectElement>) => setSelectedOrg(e.target.value)}
            >
              <option value="">select org…</option>
              {visibleOrgs.map((org) => (
                <option key={org.org_login} value={org.org_login}>
                  @{org.org_login}
                </option>
              ))}
            </Select>

            <Select
              label="usage type"
              value={selectedType}
              onChange={(e: React.ChangeEvent<HTMLSelectElement>) => setSelectedType(e.target.value)}
            >
              <option value="">select type…</option>
              {attestationTypes.map((t) => (
                <option key={t.value} value={t.value}>
                  {t.label}
                </option>
              ))}
            </Select>

            <div className="flex items-center gap-2 pt-1">
              <Button
                variant="primary"
                size="sm"
                loading={createAttestation.isPending}
                onClick={handleSubmit}
                disabled={!selectedOrg || !selectedType || createAttestation.isPending}
              >
                {createAttestation.isPending ? "attesting…" : "submit attestation"}
              </Button>
              <Button variant="ghost" size="sm" onClick={closePopover}>
                cancel
              </Button>
            </div>

            {createAttestation.isError && (
              <p className="text-[13px] text-danger px-3 py-2 border border-danger/30 bg-danger-soft">
                {createAttestation.error.message}
              </p>
            )}
          </div>
        </div>
      )}
    </span>
  );
}
