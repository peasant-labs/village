import Link from "next/link";

/** The one copy of the integrity disclosure, shown wherever a transcript becomes readable by others. */
export const INTEGRITY_DISCLOSURE =
  "once this transcript is shared, an automated check reads it for signs of editing. the check does not store any content; village only keeps metadata about the check result and timestamp (see the";

export const INTEGRITY_DISCLOSURE_LINK_LABEL = "privacy notice";
export const INTEGRITY_DISCLOSURE_END = ").";
export const INTEGRITY_NOTICE_HREF = "/privacy#privacy-integrity";

export default function IntegrityDisclosure() {
  return (
    <p className="pub-hint">
      {INTEGRITY_DISCLOSURE}{" "}
      <Link href={INTEGRITY_NOTICE_HREF} className="underline focus-mono">
        {INTEGRITY_DISCLOSURE_LINK_LABEL}
      </Link>
      {INTEGRITY_DISCLOSURE_END}
    </p>
  );
}
