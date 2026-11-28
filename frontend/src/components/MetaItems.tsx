import { Fragment, type ReactNode } from "react";

type Item = ReactNode | false | null | undefined;

/**
 * A line of facts set apart by space, the way fairtrade sets a meta line,
 * with no glyph between them. A screen reader hears them separated by
 * commas. Each fact moves to the next line whole before it breaks inside.
 * Empty items are skipped, so a caller can list a fact conditionally.
 */
export default function MetaItems({ items, className = "" }: { items: readonly Item[]; className?: string }) {
  const shown = items.filter((item) => item != null && item !== false && item !== "");
  return (
    <span className={`flex flex-wrap items-baseline gap-x-4 gap-y-1 ${className}`}>
      {shown.map((item, index) => (
        <Fragment key={index}>
          {index > 0 && <span className="sr-only">, </span>}
          <span className="min-w-0 max-w-full [overflow-wrap:anywhere]">{item}</span>
        </Fragment>
      ))}
    </span>
  );
}
