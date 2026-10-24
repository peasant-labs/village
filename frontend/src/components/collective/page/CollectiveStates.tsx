"use client";

import Link from "next/link";
import { useQueryClient } from "@tanstack/react-query";
import { Users } from "lucide-react";
import { OnCrumb, OnEmpty, OnHead, OnPage } from "@/components/online/Page";

/**
 * The collective's name while its page reads it, when the app already holds
 * it: the person's own collectives and the ones discover listed are read
 * before a collective is opened from them. Null on a cold link.
 */
export function useKnownCollectiveName(id: string): string | null {
  const qc = useQueryClient();
  for (const key of [["groups"], ["visible-groups"]]) {
    const list = qc.getQueryData<{ id: string; name: string }[]>(key);
    const found = list?.find((group) => group.id === id);
    if (found) return found.name;
  }
  return null;
}

/**
 * The collective pages' shape while the collective is read: a crumb, the
 * page's title, a row of chips and a body. The title is the one the page will
 * show (`title`), so the heading does not change when the read lands; when it
 * is not known yet its place shimmers.
 */
export function CollectiveSkeleton({
  crumb,
  title,
}: {
  crumb: { label: string; href?: string }[];
  title: string | null;
}) {
  return (
    <OnPage tight testId="collective-loading">
      <OnCrumb items={crumb} />
      <OnHead title={title} loadingLabel="loading the collective" />
      {/* A status, so its label is announced; the shapes are for the eye. */}
      <div className="on-cskel" role="status" aria-busy="true" aria-label="loading the collective">
        <div aria-hidden="true" className="on-row">
          {[1, 2, 3].map((i) => (
            <div key={i} className="h-11 w-48 max-w-full bg-surface-hover animate-shimmer" />
          ))}
        </div>
        <div aria-hidden="true" className="h-64 w-full bg-surface-hover animate-shimmer" />
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
  title,
  message,
  retrying,
  onRetry,
}: {
  crumb: { label: string; href?: string }[];
  /** The page's title, as the loaded page would show it; "collective" when the
   *  read that failed was the one that would have named it. */
  title: string | null;
  message: string;
  retrying: boolean;
  onRetry: () => void;
}) {
  return (
    <OnPage tight testId="collective-error">
      <OnCrumb items={crumb} />
      <OnHead title={title ?? "collective"} />
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
        headingLevel={1}
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
