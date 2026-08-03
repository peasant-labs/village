"use client";

import { type ReactNode } from "react";
import QueryProvider from "@/providers/QueryProvider";
import { AuthProvider } from "@/providers/AuthProvider";
import Navbar from "@/components/layout/Navbar";
import { UsernameGate } from "@/components/auth/UsernameGate";
import { DevAnnotateOverlay } from "@/components/dev/DevAnnotateOverlay";

export function LayoutShell({ children }: { children: ReactNode }) {
  return (
    <QueryProvider>
      <AuthProvider>
        <UsernameGate />
        <Navbar />
        {children}
        {process.env.NODE_ENV === "development" && <DevAnnotateOverlay />}
      </AuthProvider>
    </QueryProvider>
  );
}
