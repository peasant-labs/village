"use client";

import { useId, useState } from "react";
import TranscriptList from "./TranscriptList";
import SessionGroupDisclosure from "./SessionGroupDisclosure";
import { childSessionGroupLabel } from "@/lib/childSessions";
import type { TranscriptListItem } from "@/lib/types";

interface ChildSessionDisclosureProps {
  /** The id of the row this chip hangs under. Rendered as a `data-` attribute
   *  so what a chip belongs to is observable, rather than inferred from the
   *  order elements happen to appear in. */
  parentTranscriptID: string;
  /** The rows the row above started, already in server order. */
  childSessions: TranscriptListItem[];
  /** Render Edit/Delete actions for rows the current viewer owns. */
  showOwnerActions?: boolean;
  /** Hide the owner pill, on a list where every row is the same person's. */
  hideOwner?: boolean;
}

/**
 * The expandable chip a session list shows under a row that started other
 * sessions.
 *
 * A harness can start a session from inside another session. Each started
 * session is published as its own transcript, so without this a list shows the
 * session a person ran and every session it spawned as equal neighbours. Here
 * the spawned rows are one count directly beneath the row that started them,
 * which is what makes them readable: the chip needs no words to say whose
 * children these are, because it hangs off that row.
 *
 * It shares `SessionGroupDisclosure` with the agent-session group, so the two
 * collapsed controls in this app cannot drift apart.
 *
 * The rows are already in hand: they arrived in the same response as the row
 * above and were folded out of it, so expanding costs no request and can never
 * disagree with the count on the control. Nothing is hidden -- every row it
 * lists links to its transcript page as usual.
 */
export default function ChildSessionDisclosure({
  parentTranscriptID,
  childSessions,
  showOwnerActions = false,
  hideOwner = false,
}: ChildSessionDisclosureProps) {
  const [expanded, setExpanded] = useState(false);
  // One id per mounted chip: a list carries one chip per parent row, and each
  // control must name its own rows.
  const rowsID = `child-session-disclosure-rows-${useId()}`;

  if (childSessions.length === 0) return null;

  return (
    // Indented, so the chip reads as belonging to the row above it rather than
    // as another row of the list.
    <div className="pl-5" data-parent-transcript-id={parentTranscriptID}>
      <SessionGroupDisclosure
        label={childSessionGroupLabel(childSessions.length)}
        // No leading `+`. The chip hangs off its own parent's row, where the
        // count reads as part of that row rather than as an item being offered.
        collapsedLabel={childSessionGroupLabel(childSessions.length)}
        expanded={expanded}
        onToggle={() => setExpanded((open) => !open)}
        rowsID={rowsID}
        testID="child-session-disclosure"
        bare
      >
        <div
          id={rowsID}
          data-testid="child-session-disclosure-rows"
          className="border-t border-rule"
        >
          <TranscriptList
            items={childSessions}
            showOwnerActions={showOwnerActions}
            hideOwner={hideOwner}
            bare
          />
        </div>
      </SessionGroupDisclosure>
    </div>
  );
}
