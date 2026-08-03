"use client";

import type { ReactNode } from "react";
import { Share2, Globe, FileText } from "lucide-react";
import { ConsentDialog } from "@/lib/ft-ui";

interface CollectiveTarget {
  id: string;
  name: string;
  memberCount?: number;
}

interface TranscriptTarget {
  id: string;
  title: string;
}

interface ConfirmContributeDialogProps {
  open: boolean;
  onClose: () => void;
  onConfirm: () => void | Promise<void>;
  transcripts: TranscriptTarget[];
  collectives: CollectiveTarget[];
  isSubmitting?: boolean;
}

const MAX_LISTED = 5;

export default function ConfirmContributeDialog({
  open,
  onClose,
  onConfirm,
  transcripts,
  collectives,
  isSubmitting = false,
}: ConfirmContributeDialogProps) {
  if (transcripts.length === 0) return null;

  const count = transcripts.length;
  const plural = count !== 1;
  const listed = transcripts.slice(0, MAX_LISTED);
  const remaining = transcripts.length - listed.length;
  const collectiveNameNodes = formatCollectiveListNodes(collectives.map((c) => c.name));

  return (
    <ConsentDialog
      open={open}
      labelId="cns-contribute"
      title={
        <>
          make {count} transcript{plural ? "s" : ""} visible?
        </>
      }
      intro={
        <p>
          {count} private transcript{plural ? "s" : ""} will become visible to members of{" "}
          {collectiveNameNodes}. their visibility changes from{" "}
          <span className="cns-mono">private</span> to{" "}
          <span className="cns-mono">shared</span>.
        </p>
      }
      axes={[
        {
          icon: Share2,
          tone: "reveal",
          key: "contribution",
          value: (
            <>
              flips <span className="cns-mono">private</span> →{" "}
              <span className="cns-mono">shared</span>
            </>
          ),
          scope: "the full turn-by-turn record is shared",
        },
        {
          icon: Globe,
          tone: "open",
          key: "data access",
          value: "visible to collective members",
          scope: "per this collective’s data-access policy",
        },
      ]}
      summaryCaption="what crosses the boundary"
      consentLabel="i understand and consent to sharing these transcripts"
      confirmLabel="contribute & make visible"
      confirmIcon={Share2}
      busy={isSubmitting}
      onCancel={onClose}
      onConfirm={() => void onConfirm()}
    >
      <div className="cns-list">
        <p className="cns-list-cap">
          transcript{plural ? "s" : ""} ({count})
        </p>
        <ul>
          {listed.map((t) => (
            <li key={t.id}>
              <span className="cns-list-name">{t.title || "Untitled"}</span>
              <FileText
                size={14}
                aria-hidden
                style={{ flex: "none", color: "var(--ink-3)" }}
              />
            </li>
          ))}
          {remaining > 0 && (
            <li>
              <span className="cns-list-more">…and {remaining} more</span>
            </li>
          )}
        </ul>
      </div>
    </ConsentDialog>
  );
}

function formatCollectiveListNodes(names: string[]): ReactNode {
  if (names.length === 0) return <span className="cns-name">the collective</span>;
  const mono = (name: string, key: string | number) => (
    <span key={key} className="cns-name">
      {name}
    </span>
  );
  if (names.length === 1) return mono(names[0], 0);
  if (names.length === 2)
    return (
      <>
        {mono(names[0], 0)} and {mono(names[1], 1)}
      </>
    );
  return (
    <>
      {names.slice(0, -1).map((n, i) => (
        <span key={i}>
          {mono(n, i)}
          {", "}
        </span>
      ))}
      and {mono(names[names.length - 1], names.length - 1)}
    </>
  );
}
