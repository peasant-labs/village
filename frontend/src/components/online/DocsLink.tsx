import type { ReactNode } from "react";
import { ExternalLink } from "lucide-react";

/**
 * A link to the page of the docs that explains something. The app does not
 * restate how-to; it sends the reader there, in a new tab, and says so.
 */
export default function DocsLink({
  href,
  children,
  className = "on-link",
  testId,
}: {
  href: string;
  children: ReactNode;
  className?: string;
  testId?: string;
}) {
  return (
    <a href={href} target="_blank" rel="noopener noreferrer" className={`${className} on-docs-link`} data-testid={testId}>
      {children}
      <ExternalLink aria-hidden="true" />
      <span className="sr-only">, opens in a new tab</span>
    </a>
  );
}
