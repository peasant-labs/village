"use client";

import { useEffect } from "react";
import { useAuth } from "@/providers/AuthProvider";
import { MARKETING_URL } from "@/lib/marketing";
import HomePage from "./HomePage";

/**
 * The root route decides WHOSE page `/` is.
 *
 * A signed-in visitor lands on their own home: their transcripts, and who can
 * read each one. A signed-out visitor is sent to the marketing site, which
 * introduces the product and links to `/login`. The proxy already sends a
 * visitor with no session cookie there before this page is served; this branch
 * covers a cookie that turned out to be stale once `GET /auth/me` answered.
 *
 * Neither branch renders until the session is known, so a signed-in person is
 * never shown a flash of the redirect.
 */
export default function RootPage() {
  const { isLoading, isLoggedIn } = useAuth();
  const signedOut = !isLoading && !isLoggedIn;

  useEffect(() => {
    if (signedOut) window.location.replace(MARKETING_URL);
  }, [signedOut]);

  if (isLoading) {
    return (
      <div className="on-page" data-testid="root-route-pending" aria-hidden="true">
        <div className="h-10 w-72 animate-shimmer" />
        <div className="h-48 animate-shimmer" />
      </div>
    );
  }

  if (signedOut) {
    return (
      <div className="on-page" data-testid="root-route-leaving">
        <p className="on-muted" role="status">
          taking you to <a className="on-link" href={MARKETING_URL}>{MARKETING_URL.replace(/^https?:\/\//, "")}</a>
        </p>
      </div>
    );
  }

  return <HomePage />;
}
