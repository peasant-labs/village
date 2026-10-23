"use client";

import Link from "next/link";
import { Users } from "lucide-react";
import { OnCrumb, OnEmpty, OnPage } from "@/components/online/Page";

/** The collective pages' shape while the collective is read: a crumb, a title, a row of chips and a body. */
export function CollectiveSkeleton({ crumb }: { crumb: { label: string; href?: string }[] }) {
  return (
    <OnPage tight testId="collective-loading">
      <OnCrumb items={crumb} />
      <div className="on-cskel" aria-busy="true" aria-label="loading the collective">
        <div className="h-10 w-72 max-w-full bg-surface-hover animate-shimmer" />
        <div className="on-row">
          {[1, 2, 3].map((i) => (
            <div key={i} className="h-11 w-48 max-w-full bg-surface-hover animate-shimmer" />
          ))}
        </div>
        <div className="h-64 w-full bg-surface-hover animate-shimmer" />
      </div>
    </OnPage>
  );
}

/**
 * A read that failed for a reason other than "not found": what broke, in the
 * server's words, and a way to ask again.
 */
export function CollectiveReadError({
  crumb,
  message,
  retrying,
  onRetry,
}: {
  crumb: { label: string; href?: string }[];
  message: string;
  retrying: boolean;
  onRetry: () => void;
}) {
  return (
    <OnPage tight testId="collective-error">
      <OnCrumb items={crumb} />
      <div className="on-notice" data-tone="danger" role="alert">
        <p>could not read the collective: {message}</p>
        <button type="button" className="btn btn-secondary" disabled={retrying} onClick={onRetry}>
          try again
        </button>
      </div>
    </OnPage>
  );
}

/** No collective answers at this address (or the viewer may not know it exists). */
export function CollectiveNotFound() {
  return (
    <OnPage tight testId="collective-not-found">
      <OnCrumb items={[{ label: "collectives", href: "/collectives" }, { label: "not found" }]} />
      <OnEmpty
        icon={<Users />}
        title="collective not found"
        body="it may have been deleted, or the link may be wrong."
        actions={
          <Link href="/collectives" className="btn btn-secondary">
            back to collectives
          </Link>
        }
      />
    </OnPage>
  );
}
