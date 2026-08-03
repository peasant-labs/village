"use client";

import { useEffect } from "react";
import { usePathname, useRouter } from "next/navigation";
import { useAuth } from "@/providers/AuthProvider";

// Routes that must remain reachable without a chosen handle, so the onboarding
// redirect can't trap the user (welcome itself, and the OAuth landing).
const EXEMPT = new Set(["/welcome", "/auth/callback"]);

// UsernameGate forces a logged-in user who hasn't picked a canonical handle yet
// to the post-SSO onboarding step before using the rest of the app.
export function UsernameGate() {
  const { user, isLoading, isLoggedIn } = useAuth();
  const pathname = usePathname();
  const router = useRouter();

  useEffect(() => {
    if (isLoading || !isLoggedIn || !user) return;
    if (user.username_chosen) return;
    if (EXEMPT.has(pathname)) return;
    router.replace("/welcome");
  }, [isLoading, isLoggedIn, user, pathname, router]);

  return null;
}
