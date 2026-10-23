"use client";

import { useEffect } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { SignInProviders } from "@/lib/ft-ui";
import { useAuth } from "@/providers/AuthProvider";
import PeasantWordmark from "@/components/online/PeasantWordmark";
import { NEXT_PARAM, SIGN_IN_PROVIDERS, rememberNextPath, safeNextPath, startSignIn } from "@/lib/signIn";

/**
 * The one front door: the wordmark, the GitHub button, the terminal command that
 * uses the same sign-in, and the privacy notice.
 *
 * A `next` address (set by a page that asked a signed-out visitor to sign in)
 * is remembered before leaving for GitHub, and `/auth/callback` returns there.
 * Somebody who is already signed in is sent on straight away.
 */
export default function LoginPage() {
  const router = useRouter();
  const params = useSearchParams();
  const { isLoading, isLoggedIn } = useAuth();
  const next = safeNextPath(params.get(NEXT_PARAM));

  useEffect(() => {
    if (!isLoading && isLoggedIn) router.replace(next ?? "/");
  }, [isLoading, isLoggedIn, next, router]);

  const signIn = (providerId: string) => {
    rememberNextPath(next);
    startSignIn(providerId);
  };

  return (
    <div className="on-login" data-testid="sign-in-page">
      <section className="on-login-card" aria-labelledby="login-heading">
        <header>
          <PeasantWordmark />
          <h1 id="login-heading">log in to peasant</h1>
        </header>
        <div className="on-login-body">
          <SignInProviders providers={SIGN_IN_PROVIDERS} onSignIn={signIn} />
          <p className="on-chrome">
            in the terminal: <code>peasant village login</code>
          </p>
        </div>
        <footer>
          <Link href="/privacy" className="on-link on-chrome">
            privacy notice
          </Link>
        </footer>
      </section>
    </div>
  );
}
