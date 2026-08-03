"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/providers/AuthProvider";
import { useSetUsername } from "@/lib/queries/auth";
import { HandleClaim } from "@/lib/ft-ui";

// Mirror the backend handle rules: 3–30 chars, lowercase alphanumeric with
// single internal hyphens, starting/ending alphanumeric.
const HANDLE_RE = /^[a-z0-9][a-z0-9-]{1,28}[a-z0-9]$/;

function suggest(raw: string | null | undefined): string {
  if (!raw) return "";
  return raw
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 30)
    .replace(/-+$/g, "");
}

export default function WelcomePage() {
  const router = useRouter();
  const { user, isLoading, isLoggedIn } = useAuth();
  const setUsername = useSetUsername();

  // Track a handle that was rejected by the server as already taken.
  const [takenHandle, setTakenHandle] = useState<string | null>(null);

  const seeded = useMemo(
    () => suggest(user?.provider_username ?? user?.github_username),
    [user]
  );

  // Redirect away if not logged in, or already onboarded.
  useEffect(() => {
    if (isLoading) return;
    if (!isLoggedIn) {
      router.replace("/");
    } else if (user?.username_chosen) {
      router.replace("/");
    }
  }, [isLoading, isLoggedIn, user?.username_chosen, router]);

  // Format-only validator: idle → invalid → available (taken fed back from server).
  const validate = useCallback(
    (raw: string) => {
      const handle = raw.trim();
      if (!handle) return { state: "idle" as const };
      const normal = handle.toLowerCase();
      if (!HANDLE_RE.test(normal)) {
        return {
          state: "invalid" as const,
          hint: "3–30 characters: letters, numbers, and single hyphens.",
        };
      }
      if (takenHandle && normal === takenHandle.toLowerCase()) {
        return {
          state: "taken" as const,
          hint: `@${handle} is already claimed. Try another.`,
        };
      }
      return { state: "available" as const };
    },
    [takenHandle]
  );

  function handleSubmit(handle: string) {
    setTakenHandle(null);
    setUsername.mutate(handle, {
      onSuccess: (u) => router.replace(`/users/${u.github_username}`),
      onError: () => {
        // Mark the submitted handle as taken so the validate callback can
        // surface it as state='taken' on the next render cycle.
        setTakenHandle(handle);
      },
    });
  }

  if (isLoading || !isLoggedIn || user?.username_chosen) {
    return null;
  }

  return (
    <div className="flex min-h-[70vh] items-center justify-center px-6 animate-fade-up">
      <HandleClaim
        initialValue={seeded}
        suggestedFrom={user?.provider_username ?? user?.github_username ?? ""}
        validate={validate}
        onSubmit={handleSubmit}
      />
    </div>
  );
}
