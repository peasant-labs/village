"use client";

/**
 * The one notice for rows that arrived without a project identity.
 *
 * `project_hash` is a required identity column, so a transcript reaching a
 * client without one is a backend contract violation rather than an ordinary
 * empty state. It is reported and left out of the project grouping; it is never
 * dropped from the page, and never folded into an invented project. Every
 * well-formed project still renders.
 *
 * Shared because the same violation was announced two different ways: one page
 * gave it `role="alert"` and the other did not, so assistive technology heard
 * about it on one surface and not the other. It is announced everywhere now.
 */
export interface MalformedProjectNoticeProps {
  /** How many rows arrived with no project identity. */
  count: number;
  /** Names the surface for tests that assert WHICH page reported it. */
  testId?: string;
  className?: string;
}

export default function MalformedProjectNotice({
  count,
  testId,
  className = "",
}: MalformedProjectNoticeProps) {
  if (count <= 0) return null;
  return (
    <div
      role="alert"
      data-testid={testId}
      className={`border border-danger/40 bg-danger-soft px-4 py-3 text-sm text-danger ${className}`.trim()}
    >
      <p className="font-medium">
        {count} transcript{count !== 1 ? "s" : ""} could not be grouped by project
      </p>
      <p className="mt-1 text-[13px]">
        Each is missing the project identity the server is expected to always
        provide. They are omitted from the project list below; the rest of this
        page is unaffected.
      </p>
    </div>
  );
}
