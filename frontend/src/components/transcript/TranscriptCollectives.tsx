"use client";

import { Boxes } from "lucide-react";
import { Chip } from "@/lib/ft-ui";
import { useTranscriptCollectives } from "@/lib/queries/collectives";

/**
 * The collectives holding this transcript that THIS viewer may see.
 *
 * The server applies the collective-visibility rule and the owner's
 * contributor opt-in itself, and answers an empty list rather than a refusal
 * when either withholds everything. So an empty answer renders as nothing at
 * all: no chips, no label, no "some memberships are hidden" note. Any such
 * note would confirm that memberships exist and are being withheld, which is
 * precisely what a person who did not opt in to being listed asked not to
 * happen. Emptiness and "not in any collective" are meant to be
 * indistinguishable here.
 */
export default function TranscriptCollectives({ transcriptId }: { transcriptId: string }) {
  const { data } = useTranscriptCollectives(transcriptId);
  const collectives = data ?? [];
  if (collectives.length === 0) return null;

  return (
    <span
      data-testid="transcript-collectives"
      className="inline-flex items-center gap-1.5"
    >
      {collectives.map((collective) => (
        <Chip
          key={collective.id}
          size="sm"
          icon={Boxes}
          data-testid="transcript-collective"
          data-collective-id={collective.id}
        >
          {collective.name}
        </Chip>
      ))}
    </span>
  );
}
