"use client";

import Link from "next/link";
import { AlertTriangle } from "lucide-react";
import { OnEmpty, OnPage } from "@/components/online/Page";
import { HOME_SECTION } from "@/lib/nav/sections";

/**
 * A page that failed while it rendered. It keeps the page's chrome and offers a
 * second try of the same page, in place of Next's default "application error"
 * screen. The error's own message is never shown: a server error reaches the
 * browser as an opaque digest, and a client one may carry internal detail.
 */
export default function RouteError({ reset }: { error: Error & { digest?: string }; reset: () => void }) {
  return (
    <OnPage testId="route-error">
      <title>this page could not load | peasant</title>
      <OnEmpty
        headingLevel={1}
        icon={<AlertTriangle aria-hidden="true" />}
        title="this page could not load"
        body="something went wrong while drawing it. try again, or go back to home."
        actions={
          <>
            <button type="button" className="btn btn-primary" onClick={() => reset()}>
              try again
            </button>
            <Link href={HOME_SECTION.href} className="btn btn-secondary">
              back to home
            </Link>
          </>
        }
      />
    </OnPage>
  );
}
