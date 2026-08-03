"use client";

import { useState } from "react";
import { LogOut, Archive } from "lucide-react";
import { ConsentDialog } from "@/lib/ft-ui";

interface LeaveCollectiveDialogProps {
  open: boolean;
  onClose: () => void;
  onConfirm: (retract: boolean) => void | Promise<void>;
  collectiveName: string;
  policy: "user_choice" | "mandatory";
  shareCount: number;
  isSubmitting?: boolean;
}

export default function LeaveCollectiveDialog({
  open,
  ...props
}: LeaveCollectiveDialogProps) {
  if (!open) return null;
  return <OpenLeaveCollectiveDialog key={props.policy} {...props} />;
}

function OpenLeaveCollectiveDialog({
  onClose,
  onConfirm,
  collectiveName,
  policy,
  shareCount,
  isSubmitting = false,
}: Omit<LeaveCollectiveDialogProps, "open">) {
  const [retract, setRetract] = useState(policy === "mandatory");

  const mandatory = policy === "mandatory";

  return (
    <ConsentDialog
      open
      labelId="cns-leave"
      tone="danger"
      title={
        <>
          leave <span className="cns-name">{collectiveName}</span>?
        </>
      }
      intro={
        <p>
          you&apos;ll lose member access to{" "}
          <span className="cns-name">{collectiveName}</span>. you can rejoin later if the
          collective is open.
        </p>
      }
      axes={[
        {
          icon: LogOut,
          tone: "restricted",
          key: "access",
          value: "member access is removed",
          scope: "rejoin later if the collective stays open",
        },
        {
          icon: Archive,
          key: "retention",
          value: mandatory
            ? "mandatory: auto-retracted on leave"
            : "each leaving member decides",
          scope: mandatory ? "set by the collective" : "your call below",
        },
      ]}
      summaryCaption="what crosses the boundary"
      consentLabel="i understand i will lose member access"
      confirmLabel={retract && shareCount > 0 ? "leave & retract" : "leave collective"}
      confirmIcon={LogOut}
      busy={isSubmitting}
      onCancel={onClose}
      onConfirm={() => void onConfirm(retract)}
    >
      {shareCount > 0 && (
        <>
          <div className="cns-list">
            <p className="cns-list-cap">your contributions</p>
            <ul>
              <li>
                <span className="cns-list-name">
                  you&apos;ve contributed{" "}
                  <span className="cns-mono" style={{ color: "var(--ink)" }}>
                    {shareCount}
                  </span>{" "}
                  transcript{shareCount !== 1 ? "s" : ""} to this collective
                </span>
              </li>
            </ul>
          </div>
          {!mandatory && (
            <label className="cns-consent">
              <input
                type="checkbox"
                className="cns-consent-box"
                checked={retract}
                disabled={isSubmitting}
                onChange={(e: React.ChangeEvent<HTMLInputElement>) => setRetract(e.target.checked)}
              />
              <span className="cns-consent-label">
                also retract my transcripts. they stay in your library, just unshared from{" "}
                <span className="cns-name">{collectiveName}</span>
              </span>
            </label>
          )}
        </>
      )}
    </ConsentDialog>
  );
}
