"use client";

import { type ReactNode } from "react";
import QueryProvider from "@/providers/QueryProvider";
import { AuthProvider } from "@/providers/AuthProvider";
import { UsernameGate } from "@/components/auth/UsernameGate";
import { DevAnnotateOverlay } from "@/components/dev/DevAnnotateOverlay";
import AppFrame from "@/components/shell/AppFrame";

export function LayoutShell({ children }: { children: ReactNode }) {
  return (
    <QueryProvider>
      <AuthProvider>
        <UsernameGate />
        <AppFrame>{children}</AppFrame>
        {process.env.NODE_ENV === "development" && <DevAnnotateOverlay />}
      </AuthProvider>
    </QueryProvider>
  );
}
