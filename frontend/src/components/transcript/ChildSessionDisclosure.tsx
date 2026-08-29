"use client";

import { useId, useState } from "react";
import TranscriptList from "./TranscriptList";
import SessionGroupDisclosure from "./SessionGroupDisclosure";
import { childSessionGroupSelectionLabel } from "@/lib/childSessions";
import type { TranscriptRowFact, TranscriptRowSelection } from "./TranscriptList";
import type { TranscriptRow } from "@/lib/types";

interface ChildSessionDisclosureProps {
  /** The id of the row this chip hangs under. Rendered as a `data-` attribute
   *  so what a chip belongs to is observable, rather than inferred from the
   *  order elements happen to appear in. */
  parentTranscriptID: string;
  /** The rows the row above started, already in server order. */
  childSessions: TranscriptRow[];
  /** Render Edit/Delete actions for rows the current viewer owns. */
  showOwnerActions?: boolean;
  /** Hide the owner pill, on a list where every row is the same person's. */
  hideOwner?: boolean;
  /** The facts each revealed row states, so a child reads exactly like the row
   *  it hangs under rather than like a row from some other list. */
  facts?: readonly TranscriptRowFact[];
  /** Selection for the revealed rows. A session started by another session is
   *  picked out exactly like any other row, so a "select everything" action
   *  cannot silently miss it. */
  selection?: TranscriptRowSelection;
  /** Carried through so a revealed row attributes its author exactly as the row
   *  it hangs under does. */
  viewerIsPrivileged?: boolean;
  /** Carried through so a revealed handle leads where the parent's does. */
  linkOwner?: boolean;
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
  facts,
  selection,
  viewerIsPrivileged = false,
  linkOwner = false,
}: ChildSessionDisclosureProps) {
  const [expanded, setExpanded] = useState(false);
  // How many of the rows behind this control the viewer has picked out.
  //
  // A selection the viewer cannot see is the one way this fold could do real
  // harm. "Select everything" reaches the rows inside a control, as it must --
  // a select-all that quietly skipped them would act on less than it says --
  // but a control starts CLOSED, so without this a person could tick
  // everything, untick every box on screen, and still have rows selected for a
  // destructive action they never laid eyes on. Stating the count on the closed
  // control means a selection is never invisible.
  const selectedCount =
    selection === undefined
      ? 0
      : childSessions.filter((child) => selection.selectedIDs.has(child.transcript.id)).length;
  // One id per mounted chip: a list carries one chip per parent row, and each
  // control must name its own rows.
  const rowsID = `child-session-disclosure-rows-${useId()}`;

  if (childSessions.length === 0) return null;

  const label = childSessionGroupSelectionLabel(childSessions.length, selectedCount);

  return (
    // Indented, so the chip reads as belonging to the row above it rather than
    // as another row of the list.
    <div className="pl-5" data-parent-transcript-id={parentTranscriptID}>
      <SessionGroupDisclosure
        label={label}
        // No leading `+`. The chip hangs off its own parent's row, where the
        // count reads as part of that row rather than as an item being offered.
        collapsedLabel={label}
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
            facts={facts}
            selection={selection}
            viewerIsPrivileged={viewerIsPrivileged}
            linkOwner={linkOwner}
            bare
          />
        </div>
      </SessionGroupDisclosure>
    </div>
  );
}
