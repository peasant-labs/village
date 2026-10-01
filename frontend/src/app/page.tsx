"use client";

import { useAuth } from "@/providers/AuthProvider";
import HomePage from "./HomePage";
import SignInPage from "./SignInPage";

/**
 * The root route decides WHOSE page `/` is.
 *
 * A signed-in visitor lands on their own home: their recent sessions and the
 * projects those sessions belong to. A signed-out visitor lands on the GitHub
 * sign-in page. The public discovery list is no longer offered here or in the
 * nav; it keeps its own address at `/explore` for anyone who has the link.
 *
 * Neither branch renders until the session is known. Rendering the sign-in page
 * while `GET /auth/me` is still in flight would show a signed-in person the
 * front door and then swap it out under them.
 */
export default function RootPage() {
  const { isLoading, isLoggedIn } = useAuth();

  if (isLoading) {
    return (
      <div
        className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6"
        data-testid="root-route-pending"
      >
        <div className="h-8 w-64 animate-shimmer" />
        <div className="h-48 animate-shimmer" />
      </div>
    );
  }

  return isLoggedIn ? <HomePage /> : <SignInPage />;
}
