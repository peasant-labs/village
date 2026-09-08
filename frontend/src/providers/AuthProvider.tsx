"use client";

import { createContext, useContext, type ReactNode } from "react";
import { useMe } from "@/lib/queries/auth";
import type { User } from "@/lib/types";
import { isApiErrorStatus } from "@/lib/api";

interface AuthContextValue {
  user: User | null;
  isLoading: boolean;
  isLoggedIn: boolean;
  isError: boolean;
}

const AuthContext = createContext<AuthContextValue>({
  user: null,
  // Component harnesses without a provider represent an established anonymous
  // viewer. The real provider below still reports its actual pending state.
  isLoading: false,
  isLoggedIn: false,
  isError: false,
});

export function AuthProvider({ children }: { children: ReactNode }) {
  const { data: user, isLoading, isError, error } = useMe();
  // A 401 conclusively establishes an anonymous viewer. Transport/server
  // failures do not establish identity and remain fail-closed.
  const identityError = isError && !isApiErrorStatus(error, 401);

  return (
    <AuthContext.Provider
      value={{
        user: user ?? null,
        isLoading,
        isLoggedIn: !!user,
        isError: identityError,
      }}
    >
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth() {
  return useContext(AuthContext);
}
