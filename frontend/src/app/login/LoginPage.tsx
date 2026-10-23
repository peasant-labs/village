"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/providers/AuthProvider";
import { MARKETING_URL, leaveForSignIn, marketingLoginURL } from "@/lib/marketing";
import { safeNextPath } from "@/lib/signIn";

/**
 * The sign-in page's address, kept for every "sign in" link and bookmark. Signing
 * in happens in the marketing site's log-in popup now, so this page hands the
 * visitor there with the address it was asked to return to.
 *
 * A page load without a session never reaches it: the proxy sends that visitor
 * to the popup first. What arrives here is a link followed inside the app, or a
 * session cookie that turned out to be stale. Somebody who is signed in is sent
 * on straight away.
 */
export default function LoginPage({ next: asked }: { next: string | null }) {
  const router = useRouter();
  const { isLoading, isLoggedIn } = useAuth();
  const next = safeNextPath(asked);
  const destination = marketingLoginURL(next);

  useEffect(() => {
    if (isLoading) return;
    if (isLoggedIn) router.replace(next ?? "/");
    else leaveForSignIn(next);
  }, [isLoading, isLoggedIn, next, router]);

  return (
    <div className="on-login" data-testid="leaving-for-sign-in">
      {!isLoading && !isLoggedIn && (
        <p className="on-muted" role="status">
          taking you to log in on <a className="on-link" href={destination}>{MARKETING_URL.replace(/^https?:\/\//, "")}</a>
        </p>
      )}
    </div>
  );
}
