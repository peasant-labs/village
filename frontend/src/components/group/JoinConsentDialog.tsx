"use client";

import { Eye, Users, UserCheck } from "lucide-react";
import { ConsentDialog } from "@/lib/ft-ui";

interface JoinConsentDialogProps {
  open: boolean;
  onClose: () => void;
  onConfirm: () => void | Promise<void>;
  collectiveName: string;
  isSubmitting?: boolean;
}

export default function JoinConsentDialog({
  open,
  onClose,
  onConfirm,
  collectiveName,
  isSubmitting = false,
}: JoinConsentDialogProps) {
  return (
    <ConsentDialog
      open={open}
      labelId="cns-join-consent"
      title={
        <>
          join <span className="cns-name">{collectiveName}</span>?
        </>
      }
      intro={
        <>
          <p>
            you&apos;re currently <span className="cns-em">not discoverable</span>, so your
            handle is hidden across the commons.
          </p>
          <p>
            joining reveals your profile to the collective&apos;s{" "}
            <span className="cns-em">owners</span>. they need it to review your membership and
            contributions. other members still see you as anon.
          </p>
        </>
      }
      axes={[
        {
          icon: Eye,
          tone: "reveal",
          key: "identity",
          value: (
            <>
              your profile (<span className="cns-mono">handle</span>, name &amp; avatar) becomes
              visible
            </>
          ),
          scope: "to owners only, to review membership",
        },
        {
          icon: Users,
          key: "to other members",
          value: (
            <>
              you still appear as <span className="cns-mono">anon</span>
            </>
          ),
          scope: "no handle, name, or avatar shown",
        },
      ]}
      summaryCaption="what crosses the boundary"
      consentLabel="i understand and consent to revealing my profile to owners"
      confirmLabel="reveal profile & join"
      confirmIcon={UserCheck}
      busy={isSubmitting}
      onCancel={onClose}
      onConfirm={() => void onConfirm()}
    />
  );
}
