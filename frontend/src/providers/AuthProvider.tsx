"use client";

import { createContext, useContext, useEffect, type ReactNode } from "react";
import { useMe } from "@/lib/queries/auth";
import { isApiErrorStatus } from "@/lib/api";
import { writeOnlineHint } from "@/lib/onlineHint";
import type { User } from "@/lib/types";

interface AuthContextValue {
  user: User | null;
  isLoading: boolean;
  isLoggedIn: boolean;
}

const AuthContext = createContext<AuthContextValue>({
  user: null,
  isLoading: true,
  isLoggedIn: false,
});

export function AuthProvider({ children }: { children: ReactNode }) {
  const { data: user, isLoading, error } = useMe();
  const handle = user?.github_username ?? null;
  const signedOut = isApiErrorStatus(error, 401);

  // Tell the marketing site who is signed in here, or that nobody is. Only the
  // server's own answers count: a failed read says nothing either way.
  useEffect(() => {
    if (handle) writeOnlineHint(handle);
    else if (signedOut) writeOnlineHint(null);
  }, [handle, signedOut]);

  return (
    <AuthContext.Provider
      value={{
        user: user ?? null,
        isLoading,
        isLoggedIn: !!user,
      }}
    >
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth() {
  return useContext(AuthContext);
}
