import Link from "next/link";
import { ChevronRight } from "lucide-react";
import {
  CONFIRMATION,
  LICENSE_ORDER,
  LICENSE_TERMS,
  NOTICE_UPDATED,
  OPERATOR,
  OPERATOR_PARAGRAPHS,
} from "./notice";

// Reading prose: the body font at the 16px floor, 1.5 line-height, and the
// design system's prose measure (fairtrade's base rule caps every <p> at
// --measure-prose, so no width utility is repeated here). Headings are chrome:
// display/mono face, lowercase. Regions are div[role=region], not <section>:
// fairtrade's base layer centers a bare <section> for the demo's page layout
// (max-width + margin auto), which would pull these blocks off the heading's
// left edge.
const PROSE = "text-base leading-[var(--lh-body)] text-ink";
const SECTION_HEADING =
  "font-[family-name:var(--font-display)] text-lg lowercase tracking-tight text-ink";
const CHOICE_HEADING = "font-mono text-base lowercase text-ink";

/**
 * The privacy notice: who operates Village, and what a person grants when they
 * publish under each license choice. Static content; no data fetch.
 */
export function PrivacyNotice() {
  return (
    <div
      className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-8 animate-fade-up"
      data-testid="privacy-notice"
    >
      <nav aria-label="Breadcrumb" className="flex items-center gap-1 text-xs">
        <Link
          href="/"
          className="text-ink-3 hover:text-ink transition-colors focus-mono cursor-pointer"
        >
          Village
        </Link>
        <ChevronRight className="size-3 shrink-0 text-ink-4" />
        <span className="font-medium text-ink">privacy notice</span>
      </nav>

      <div className="flex flex-col gap-1">
        <h1 className="font-[family-name:var(--font-display)] text-2xl tracking-tight text-ink">
          privacy notice
        </h1>
        <p className="text-sm text-ink-3">
          Who operates Village, and what a public contribution licenses.
        </p>
        <p className="font-mono text-xs text-ink-4 tabular-nums">
          last updated <time dateTime={NOTICE_UPDATED}>{NOTICE_UPDATED}</time>
        </p>
      </div>

      <div role="region" aria-labelledby="privacy-operator" className="flex flex-col gap-3">
        <h2 id="privacy-operator" className={SECTION_HEADING}>
          who operates village
        </h2>
        <p className={PROSE}>
          Village is currently operated by {OPERATOR.name}, an individual based in{" "}
          {OPERATOR.region}. Contact:{" "}
          <a
            href={`mailto:${OPERATOR.contact}`}
            className="underline decoration-ink-4 underline-offset-2 hover:decoration-ink focus-mono"
          >
            {OPERATOR.contact}
          </a>
          .
        </p>
        {OPERATOR_PARAGRAPHS.map((text) => (
          <p key={text} className={PROSE}>
            {text}
          </p>
        ))}
      </div>

      <div role="region" aria-labelledby="privacy-contributions" className="flex flex-col gap-3">
        <h2 id="privacy-contributions" className={SECTION_HEADING}>
          public contributions
        </h2>
        <p className={PROSE}>{CONFIRMATION}</p>

        {LICENSE_ORDER.map((choice) => {
          const terms = LICENSE_TERMS[choice];
          const headingId = `privacy-license-${choice.toLowerCase()}`;
          return (
            <div role="region"
              key={choice}
              aria-labelledby={headingId}
              data-license-choice={choice}
              className="flex flex-col gap-2 border-l border-rule pl-4 mt-2"
            >
              <h3 id={headingId} className={CHOICE_HEADING}>
                {terms.name}
              </h3>
              {terms.paragraphs.map((text) => (
                <p key={text} className={PROSE}>
                  {text}
                </p>
              ))}
              {terms.deedUrl && (
                <a
                  href={terms.deedUrl}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="font-mono text-sm text-ink-3 hover:text-ink underline decoration-ink-4 underline-offset-2 focus-mono w-fit"
                >
                  read the license text
                </a>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
