"use client";

import { createContext, useContext, useState, type ReactNode } from "react";
import { useMe } from "@/lib/queries/auth";
import type { User } from "@/lib/types";

interface AuthContextValue {
  user: User | null;
  isLoading: boolean;
  isLoggedIn: boolean;
  isError: boolean;
  isFetching: boolean;
  /** Current verification, separate from the established identity used by routes. */
  isVerified: boolean;
  retry: () => void;
}

const AuthContext = createContext<AuthContextValue>({
  user: null,
  isLoading: true,
  isLoggedIn: false,
  isError: false,
  isFetching: false,
  isVerified: false,
  retry: () => {},
});

export function AuthProvider({ children }: { children: ReactNode }) {
  const { data, isPending, isFetching, isError, refetch } = useMe();
  const [failed, setFailed] = useState(false);
  if (isError && !failed) setFailed(true);
  if (!isPending && !isFetching && !isError && failed) setFailed(false);
  // Keep established route/form identity through background checks and outages.
  // Only a successful identity response (including normalized 401/null) changes it.
  // Discovery separately requires current verification before using private data.
  const user = data ?? null;

  return (
    <AuthContext.Provider
      value={{
        user: user ?? null,
        isLoading: isPending,
        isLoggedIn: !!user,
        isError: isError || (failed && (isPending || isFetching)),
        isFetching,
        isVerified: !isPending && !isFetching && !isError,
        retry: () => { void refetch(); },
      }}
    >
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth() {
  return useContext(AuthContext);
}
