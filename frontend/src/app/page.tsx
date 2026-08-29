"use client";

import { useAuth } from "@/providers/AuthProvider";
import ExplorePage from "./explore/ExplorePage";
import HomePage from "./HomePage";

/**
 * The root route decides WHOSE page `/` is.
 *
 * A signed-in visitor lands on their own home: their recent sessions and the
 * projects those sessions belong to. A signed-out visitor still lands on the
 * public discovery list, exactly as before — that page now also has its own
 * durable address at `/explore`, which a signed-in visitor uses to browse the
 * commons.
 *
 * Neither branch renders until the session is known. Rendering discovery while
 * `GET /auth/me` is still in flight would show a signed-in person the wrong
 * page and then swap it out under them.
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

  return isLoggedIn ? <HomePage /> : <ExplorePage />;
}
