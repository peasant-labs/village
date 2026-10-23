import type { VillagePullRequestAttachmentState } from "@peasant-labs/schema";

/**
 * The state a pull request's attachment is in, as a tag on its repository line.
 *
 * The words are the server's own state names. Amber marks the states that wait
 * on the author, olive the one where the prompts are attached, and the colour
 * is never the only signal: the word is always there.
 */
const TONE: Record<VillagePullRequestAttachmentState, "wait" | "ok" | undefined> = {
  requested: "wait",
  waiting: "wait",
  preview: "wait",
  attached: "ok",
  detached: undefined,
};

export default function PullStateTag({
  state,
  testId,
}: {
  state: VillagePullRequestAttachmentState;
  testId?: string;
}) {
  return (
    <span className="on-tag" data-tone={TONE[state]} data-testid={testId}>
      {state}
    </span>
  );
}
