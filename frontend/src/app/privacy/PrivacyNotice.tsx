import { OnCrumb, OnHead, OnPage } from "@/components/online/Page";
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
 * The privacy notice: who operates peasant online, and what a person grants
 * when they publish under each license choice. Static content; no data fetch.
 *
 * The chrome around the notice names the product. The notice's own sentences
 * (in `notice.ts`) are the operator's legal wording and are changed only on the
 * operator's instruction.
 */
export function PrivacyNotice() {
  return (
    <OnPage tight testId="privacy-notice">
      {/* The trail's root is the product: a signed-in reader's home, and the
          product's own site for anyone else. */}
      <OnCrumb items={[{ label: "peasant", href: "/" }, { label: "privacy notice" }]} />

      <OnHead
        title="privacy notice"
        lede={
          <span className="on-cap tabular-nums">
            last updated <time dateTime={NOTICE_UPDATED}>{NOTICE_UPDATED}</time>
          </span>
        }
      />

      <div role="region" aria-labelledby="privacy-operator" className="flex flex-col gap-3">
        <h2 id="privacy-operator" className={SECTION_HEADING}>
          who operates peasant online
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
    </OnPage>
  );
}
