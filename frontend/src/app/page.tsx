"use client";

import { useAuth } from "@/providers/AuthProvider";
import HomePage from "./HomePage";
import { OnPage } from "@/components/online/Page";

/**
 * The root route: the signed-in person's own home, their transcripts and who
 * can read each one.
 *
 * Nobody without a session is served it. The proxy sends a visitor with no
 * session cookie to the marketing site's log-in popup before the page is
 * served, and the app's frame does the same for a cookie that turned out to be
 * stale once `GET /auth/me` answered.
 *
 * Nothing renders until the session is known, so a signed-in person is never
 * shown a flash of anything else.
 */
export default function RootPage() {
  const { isLoading, isLoggedIn } = useAuth();

  if (isLoading) {
    return (
      <OnPage testId="root-route-pending">
        <div className="h-10 w-72 animate-shimmer" />
        <div className="h-48 animate-shimmer" />
      </OnPage>
    );
  }

  if (!isLoggedIn) return null;

  return <HomePage />;
}
