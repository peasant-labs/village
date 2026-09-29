"use client";

import { SignInProviders } from "@/lib/ft-ui";
import { SIGN_IN_PROVIDERS, startSignIn } from "@/lib/signIn";

/**
 * What a signed-out visitor sees at `/`: one front door.
 *
 * One sentence on what village is for, one GitHub button, and the two things a
 * newcomer asks next — which handle they get, and whether the CLI signs in the
 * same way. The handle note restates the server's rule as copy and decides
 * nothing: which handle an account gets, and whether it is asked to choose
 * one, is settled at sign-in by the backend.
 */
export default function SignInPage() {
  return (
    <div className="px-4 sm:px-8" data-testid="sign-in-page">
      <section
        aria-labelledby="sign-in-heading"
        className="mx-auto w-full max-w-[35rem] pt-[clamp(var(--sp-8),18vh,11rem)] pb-[var(--sp-8)]"
      >
        <h1
          id="sign-in-heading"
          className="font-[family-name:var(--font-display)] text-[length:var(--fs-xl)] font-semibold leading-tight tracking-tight text-ink text-balance"
        >
          the agent sessions behind your team&apos;s pull requests
        </h1>
        <p className="mt-4 text-ink-2">
          peasant records your ai coding sessions on your machine. village keeps the ones you
          publish, shared with your collectives, and links them to pull requests.
        </p>

        <div className="mt-8">
          <SignInProviders providers={SIGN_IN_PROVIDERS} onSignIn={startSignIn} />
        </div>

        <div className="mt-8 flex flex-col gap-2 border-t border-rule pt-5 text-ink-3">
          <p>
            first time here? your handle is your github login. if someone already has it, you
            choose another.
          </p>
          <p>
            using the cli?{" "}
            <code className="border border-rule bg-canvas px-1 font-mono text-ink-2">
              peasant village login
            </code>{" "}
            uses the same github sign-in.
          </p>
        </div>
      </section>
    </div>
  );
}
