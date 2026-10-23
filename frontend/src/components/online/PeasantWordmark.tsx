import Link from "next/link";

/**
 * The product's name with its stalk mark: one product, "peasant", whether a
 * person is on their own computer or online. The mark is decoration; the word
 * is the name.
 */
export function PeasantStalk() {
  return (
    <svg
      viewBox="0 0 18 22"
      aria-hidden="true"
      focusable="false"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="square"
    >
      <path d="M9 21V3" />
      <path d="M9 7 5 4" />
      <path d="M9 7l4-3" />
      <path d="M9 11 5 8" />
      <path d="M9 11l4-3" />
      <path d="M9 15l-4-3" />
      <path d="M9 15l4-3" />
    </svg>
  );
}

export default function PeasantWordmark({ href, label }: { href?: string; label?: string }) {
  const body = (
    <>
      <PeasantStalk />
      peasant
    </>
  );
  if (!href) {
    return <span className="on-brand">{body}</span>;
  }
  return (
    <Link href={href} className="on-brand" aria-label={label}>
      {body}
    </Link>
  );
}
