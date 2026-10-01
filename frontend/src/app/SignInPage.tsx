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
    <div className="iu-page cmg-signin" data-testid="sign-in-page">
      <section aria-labelledby="sign-in-heading" className="cmg-signin-card">
        <p className="cmg-signin-brand mono">village</p>
        <h1 id="sign-in-heading" className="iu-page-title">
          the agent sessions behind your team&apos;s pull requests
        </h1>
        <p className="iu-page-sub">
          peasant records your ai coding sessions on your machine. village keeps the ones you
          publish, shared with your collectives, and links them to pull requests.
        </p>
        <SignInProviders providers={SIGN_IN_PROVIDERS} onSignIn={startSignIn} />
        <ul className="cmg-signin-notes">
          <li>
            first time here? your handle is your github login. if someone already has it, or it
            does not fit a village handle, you choose another.
          </li>
          <li>
            using the cli? <code className="mono">peasant village login</code> uses the same github sign-in.
          </li>
        </ul>
      </section>
    </div>
  );
}
