import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { AuthProvider } from "@/providers/AuthProvider";

/** A confirmed anonymous auth query for component-only orchestration tests. */
export function anonymousAuthBoundary() {
  const client = new QueryClient();
  client.setQueryDefaults(["me"], { staleTime: Infinity });
  client.setQueryData(["me"], null);
  return function Boundary({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={client}><AuthProvider>{children}</AuthProvider></QueryClientProvider>;
  };
}
