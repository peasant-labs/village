"use client";

import { createContext, useContext, type ReactNode } from "react";
import { useMe } from "@/lib/queries/auth";
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
  const { data: user, isLoading } = useMe();

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
