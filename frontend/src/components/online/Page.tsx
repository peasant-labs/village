import Link from "next/link";
import type { ReactNode } from "react";
import { collectiveTone } from "@/lib/collectiveTone";

/**
 * The frame every online page body sits in: one column, generous air above and
 * below, and an even rhythm between its parts.
 */
export function OnPage({
  children,
  tight = false,
  testId,
  label,
}: {
  children: ReactNode;
  /** Pages with a breadcrumb start a little higher. */
  tight?: boolean;
  testId?: string;
  label?: string;
}) {
  return (
    <div className={tight ? "on-page is-tight" : "on-page"} data-testid={testId} aria-label={label}>
      {children}
    </div>
  );
}

/** The page's title on the left, its actions on the right, one line under both. */
export function OnHead({
  title,
  lede,
  actions,
  titleId,
}: {
  title: ReactNode;
  lede?: ReactNode;
  actions?: ReactNode;
  titleId?: string;
}) {
  return (
    <header className="on-head">
      <h1 className="on-title" id={titleId}>
        {title}
      </h1>
      {actions ? <div className="on-actions">{actions}</div> : null}
      {lede ? <p className="on-lede">{lede}</p> : null}
    </header>
  );
}

/** The trail above a page title: each step a link, the last one where you are. */
export function OnCrumb({ items }: { items: { label: ReactNode; href?: string }[] }) {
  return (
    <nav className="on-crumb" aria-label="breadcrumb">
      {items.map((item, index) => {
        const last = index === items.length - 1;
        return (
          <span key={index} className="on-row" style={{ gap: "var(--sp-2)" }}>
            {item.href && !last ? (
              <Link href={item.href}>{item.label}</Link>
            ) : (
              <span aria-current={last ? "page" : undefined}>{item.label}</span>
            )}
            {!last && <span aria-hidden="true">/</span>}
          </span>
        );
      })}
    </nav>
  );
}

/** One empty state for every surface: an icon tile, a title, at most one line, and actions. */
export function OnEmpty({
  icon,
  title,
  body,
  children,
  actions,
  testId,
  headingLevel = 2,
}: {
  icon: ReactNode;
  title: ReactNode;
  body?: ReactNode;
  /** Anything between the line and the actions, such as command lines. */
  children?: ReactNode;
  actions?: ReactNode;
  testId?: string;
  headingLevel?: 2 | 3;
}) {
  const Heading = headingLevel === 2 ? "h2" : "h3";
  return (
    <div className="on-empty" data-testid={testId}>
      <span className="on-ico-tile" aria-hidden="true">
        {icon}
      </span>
      <Heading>{title}</Heading>
      {body ? <p>{body}</p> : null}
      {children}
      {actions ? <div className="on-empty-actions">{actions}</div> : null}
    </div>
  );
}

/** A collective's colour square. Decorative: its name is always beside it. */
export function CollectiveSwatch({ id }: { id: string }) {
  return <span className="on-swatch" data-tone={collectiveTone(id)} aria-hidden="true" />;
}
