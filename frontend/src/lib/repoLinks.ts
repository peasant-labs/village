/**
 * Saving the repo picker: the picker reports what changed as `{ add, remove }`
 * and the collective's routes link and unlink one repository per call
 * (`POST /groups/{id}/repositories`, `DELETE /groups/{id}/repositories/{owner}/{name}`).
 * The calls run one after another, links first, and the first failure stops the
 * rest, so the message can say exactly what was saved and what was not.
 */

export interface RepoLinkStep {
  action: "link" | "unlink";
  /** The repository as `owner/name`. */
  repo: string;
}

export interface RepoLinkOutcome {
  /** The steps that were saved, in order. */
  saved: RepoLinkStep[];
  /** The step that failed and why, or null when every step was saved. */
  failed: { step: RepoLinkStep; message: string } | null;
  /** The steps not tried because an earlier one failed. */
  notTried: RepoLinkStep[];
}

/** The picker's diff as the ordered steps the save performs. */
export function repoLinkSteps(diff: { add: readonly string[]; remove: readonly string[] }): RepoLinkStep[] {
  return [
    ...diff.add.map((repo) => ({ action: "link" as const, repo })),
    ...diff.remove.map((repo) => ({ action: "unlink" as const, repo })),
  ];
}

/** Split `owner/name` at its first slash. */
export function splitRepo(repo: string): { owner: string; name: string } {
  const slash = repo.indexOf("/");
  if (slash <= 0 || slash === repo.length - 1) {
    throw new Error(`${repo} is not an owner/name repository.`);
  }
  return { owner: repo.slice(0, slash), name: repo.slice(slash + 1) };
}

function failureText(error: unknown): string {
  if (error instanceof Error && error.message) return error.message;
  if (typeof error === "string" && error) return error;
  return "the request failed.";
}

/** Run the steps one after another; stop at the first failure. */
export async function applyRepoLinks(
  steps: readonly RepoLinkStep[],
  write: (step: RepoLinkStep) => Promise<unknown>,
): Promise<RepoLinkOutcome> {
  const saved: RepoLinkStep[] = [];
  for (let index = 0; index < steps.length; index += 1) {
    const step = steps[index];
    try {
      await write(step);
      saved.push(step);
    } catch (error) {
      return { saved, failed: { step, message: failureText(error) }, notTried: steps.slice(index + 1) };
    }
  }
  return { saved, failed: null, notTried: [] };
}

function describe(steps: readonly RepoLinkStep[], tense: "done" | "todo"): string {
  const words = tense === "done" ? { link: "linked", unlink: "unlinked" } : { link: "link", unlink: "unlink" };
  const linked = steps.filter((step) => step.action === "link").map((step) => step.repo);
  const unlinked = steps.filter((step) => step.action === "unlink").map((step) => step.repo);
  return [
    linked.length ? `${words.link} ${linked.join(", ")}` : null,
    unlinked.length ? `${words.unlink} ${unlinked.join(", ")}` : null,
  ]
    .filter(Boolean)
    .join("; ");
}

/**
 * What the save tells the owner: every repository it linked or unlinked, and on
 * a failure the one it stopped at, why, and what it did not try.
 */
export function repoLinkMessage(outcome: RepoLinkOutcome): { ok: boolean; title: string; detail: string } {
  if (!outcome.failed) {
    return { ok: true, title: "repositories saved", detail: `${describe(outcome.saved, "done")}.` };
  }
  const { step, message } = outcome.failed;
  const parts = [
    outcome.saved.length ? `saved: ${describe(outcome.saved, "done")}.` : "nothing was saved.",
    `could not ${step.action} ${step.repo}: ${/[.!?]$/.test(message) ? message : `${message}.`}`,
    outcome.notTried.length ? `not tried: ${describe(outcome.notTried, "todo")}.` : null,
  ];
  return { ok: false, title: "not every change was saved", detail: parts.filter(Boolean).join(" ") };
}
