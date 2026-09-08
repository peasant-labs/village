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
  retry: () => void;
}

const AuthContext = createContext<AuthContextValue>({
  user: null,
  isLoading: true,
  isLoggedIn: false,
  isError: false,
  isFetching: false,
  retry: () => {},
});

export function AuthProvider({ children }: { children: ReactNode }) {
  const { data, isPending, isFetching, isError, refetch } = useMe();
  const [failed, setFailed] = useState(false);
  if (isError && !failed) setFailed(true);
  if (!isPending && !isFetching && !isError && failed) setFailed(false);
  // Cached identity is not confirmation of the credentials being checked now.
  const user = isFetching || isError ? null : data ?? null;

  return (
    <AuthContext.Provider
      value={{
        user: user ?? null,
        isLoading: isPending || isFetching,
        isLoggedIn: !!user,
        isError: isError || (failed && (isPending || isFetching)),
        isFetching,
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
