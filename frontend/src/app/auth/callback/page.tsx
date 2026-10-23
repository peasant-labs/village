"use client";

import { Suspense, useEffect } from "react";
import { useSearchParams } from "next/navigation";
import { ConnectionPill, DataState } from "@/lib/ft-ui";
import { setAuthTokenCookie } from "@/lib/api";
import { takeNextPath } from "@/lib/signIn";

function CallbackHandler() {
  const searchParams = useSearchParams();

  useEffect(() => {
    const token = searchParams.get("token");
    if (token) {
      setAuthTokenCookie(token);
    }
    // Hard navigation so the auth state is fully re-fetched. The page that sent
    // the visitor to sign in, if any, is where they return; otherwise home.
    window.location.replace(takeNextPath() ?? "/");
  }, [searchParams]);

  return null;
}

// Transient OAuth handshake screen: a connecting DataState skeleton with a
// ConnectionPill header while the token is written and navigation fires.
function CallbackStatus() {
  return (
    <div className="on-login">
      <div className="on-login-card">
        <div className="on-login-body">
          <ConnectionPill status="connecting" showNote={false} />
          <p className="on-muted">finishing the github sign-in and taking you back to peasant.</p>
          <DataState status="connecting" loading skeletonRows={2} />
        </div>
      </div>
    </div>
  );
}

export default function AuthCallback() {
  return (
    <Suspense>
      <CallbackHandler />
      <CallbackStatus />
    </Suspense>
  );
}
