import { render, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import CollectiveAnalytics from "@/components/group/CollectiveAnalytics";
import type { GroupTranscript } from "@/lib/types";
import {
  loadDisplayedTurnCountFixtures,
  type DisplayedTurnCountCase,
} from "@/test/displayedTurnCountFixtures";
import { profileOwner } from "@/test/publicOwnerFixture";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";

// The dashboard's turn figures come from the count the server chose for
// display. Each case is one transcript, so the median and the tail of the
// "typical vs. tail" turns row are both that transcript's count; a transcript
// with no displayed count contributes zero, never its stored count.

const fixtures = loadDisplayedTurnCountFixtures();

function groupTranscript(c: DisplayedTurnCountCase): GroupTranscript {
  return {
    pull_requests: { count: 0, recent: [] },
    ...makeTranscriptFixture({
      // Nothing else on the row may equal a turn count under test.
      token_count: 900,
      tool_call_count: null,
      turn_count: c.turnCount,
      conversation_turn_count: c.conversationTurnCount,
      displayed_turn_count: c.displayedTurnCount,
    }),
    license_id: null,
    outcome: null,
    source_format: null,
    subagents: null,
    owner: profileOwner({ id: "user-ada", github_username: "ada" }),
  };
}

// The "typical vs. tail" rows are drawn inside the design system's
// `<ProjectOverview>`, which takes no test id and gives a row no role, and the
// package exports no name for a row. The row's own label is therefore the only
// handle this app has; it is stated once, here, and is the design system's copy.
const TYPICAL_TURNS_ROW_LABEL = "turns";

/** The median and tail figures on the turns row, as numbers. */
function typicalTurns(root: HTMLElement): number[] {
  const label = within(root).getByText(TYPICAL_TURNS_ROW_LABEL, { selector: "span" });
  const values = label.nextElementSibling!;
  // The median carries a trailing separator in its own element; the figures
  // themselves may be digit-grouped.
  const [median, tail] = [...values.children].map((el) =>
    [...el.childNodes]
      .filter((node) => node.nodeType === Node.TEXT_NODE)
      .map((node) => node.textContent ?? "")
      .join("")
      .trim(),
  );
  return [median, tail].map((figure) => {
    expect(figure, "a turn figure is a whole number, digit-grouped or not").toMatch(/^\d[\d,]*$/);
    return Number(figure.replace(/,/g, ""));
  });
}

describe("the turn count the collective dashboard totals", () => {
  it.each(fixtures)("typical vs. tail: $name", (c) => {
    const { container } = render(<CollectiveAnalytics transcripts={[groupTranscript(c)]} />);
    const counted = c.shows ?? 0;
    expect(typicalTurns(container)).toEqual([counted, counted]);
  });
});
