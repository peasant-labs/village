"use client";

import { useEffect, useRef } from "react";
import { ConnectionPill } from "@/lib/ft-ui";
import { useAuth } from "@/providers/AuthProvider";
import { rememberNextPath, safeNextPath, signInProvider, startSignIn } from "@/lib/signIn";

/**
 * The first step of a sign-in begun on the marketing site's log-in popup: the
 * same step the sign-in page's GitHub button took. The return address is
 * remembered in this tab, where `/auth/callback` reads it back, and the browser
 * leaves for the provider's OAuth start route on the API.
 *
 * Somebody whose session is already good is sent on to the address instead of
 * round the provider again.
 */
export default function SignInStart({ provider: asked, next: wanted }: { provider: string | null; next: string | null }) {
  const { isLoading, isLoggedIn } = useAuth();
  const provider = signInProvider(asked);
  const next = safeNextPath(wanted);
  const left = useRef(false);

  useEffect(() => {
    if (isLoading || left.current) return;
    left.current = true;
    if (isLoggedIn) {
      window.location.replace(next ?? "/");
      return;
    }
    rememberNextPath(next);
    startSignIn(provider);
  }, [isLoading, isLoggedIn, next, provider]);

  return (
    <div className="on-login" data-testid="sign-in-start">
      <div className="on-login-card">
        <div className="on-login-body">
          <ConnectionPill status="connecting" showNote={false} />
          <p className="on-muted" role="status">
            taking you to {provider} to log in to peasant.
          </p>
        </div>
      </div>
    </div>
  );
}
