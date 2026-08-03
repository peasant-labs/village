"use client";

import { Suspense, useEffect } from "react";
import { useSearchParams } from "next/navigation";
import { ConnectionPill, DataState } from "@/lib/ft-ui";

function CallbackHandler() {
  const searchParams = useSearchParams();

  useEffect(() => {
    const token = searchParams.get("token");
    if (token) {
      document.cookie = `peasant_token=${token}; path=/; max-age=${7 * 24 * 60 * 60}; secure; samesite=lax`;
    }
    // Use hard navigation so the auth state is fully re-fetched
    window.location.replace("/");
  }, [searchParams]);

  return null;
}

// Transient OAuth handshake screen — shows a connecting DataState skeleton
// with a ConnectionPill header while the token is written and navigation fires.
function CallbackStatus() {
  return (
    <div className="flex min-h-[60vh] items-center justify-center px-6 animate-fade-up">
      <div className="flex w-full max-w-sm flex-col gap-4 border border-rule bg-surface px-6 py-8">
        <div className="flex flex-col gap-2">
          <ConnectionPill status="connecting" showNote={false} />
          <p className="text-sm text-ink-3">
            Finishing the OAuth handshake and returning you to the village.
          </p>
        </div>
        <DataState status="connecting" loading skeletonRows={2} />
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
