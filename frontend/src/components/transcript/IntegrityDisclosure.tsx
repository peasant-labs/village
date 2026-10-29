import Link from "next/link";

/** The one copy of the integrity disclosure, shown wherever a transcript becomes readable by others. */
export const INTEGRITY_DISCLOSURE =
  "once anyone besides you can read this transcript, including a collective's owners reviewing your submission, an automated check reads it for signs it was edited after it was recorded. no person reads it for this. the check stores nothing and logs no content; village keeps only the result and when the check ran. see the";

export const INTEGRITY_DISCLOSURE_LINK_LABEL = "privacy notice";
export const INTEGRITY_NOTICE_HREF = "/privacy#privacy-integrity";

export default function IntegrityDisclosure() {
  return (
    <p className="pub-hint">
      {INTEGRITY_DISCLOSURE}{" "}
      <Link href={INTEGRITY_NOTICE_HREF} className="underline focus-mono">
        {INTEGRITY_DISCLOSURE_LINK_LABEL}
      </Link>
      .
    </p>
  );
}
