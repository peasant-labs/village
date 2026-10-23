import type { Metadata } from "next";
import Script from "next/script";
import { LayoutShell } from "@/components/LayoutShell";
import { atkinsonHyperlegible, atkinsonHyperlegibleMono } from "@/app/fonts";
import { THEME_BOOT_SCRIPT } from "@/hooks/useTheme";
// CSS import order: fairtrade components.css (tokens + base + component
// styles) FIRST, then the fairtrade analytics per-surface bundle (nothing
// auto-loads it — the dashboard ships unstyled without the explicit import),
// then the fairtrade graph engine bundle (trajectory-graph node visuals +
// @xyflow chrome), then app globals. Always import fairtrade
// components.css (never base.css standalone) so its layered tokens/base win.
// The fairtrade surface is token-driven, so order among the fairtrade sheets
// no longer carries theming semantics.
//
// NOTE: @peasant-labs/fairtrade/fonts.css is NOT imported — it loads Atkinson
// via a remote `@import url(...)`, which Next's CSS bundling can relocate
// after other rules (an invalid @import position) and silently DROP, leaving
// design-system text on a ui-sans-serif/ui-monospace fallback. The fonts are
// self-hosted instead via next/font/local (src/app/fonts/index.ts) — its
// `.variable` className below feeds --font-atkinson-hyperlegible(-mono),
// which globals.css uses to override fairtrade's
// --font-body/--font-display/--font-mono tokens. Self-hosting also removes
// the runtime Google Fonts CDN dependency and its display:swap
// flash-of-fallback-font while that round-trip resolves. Same fix peasant's
// web app already applied (see its src/app/fonts/index.ts).
import "@peasant-labs/fairtrade/components.css";
import "@peasant-labs/fairtrade/analytics.css";
import "@peasant-labs/fairtrade/graph.css";
import "./globals.css";
// The online page patterns (on-*): layout only, every value a fairtrade token.
import "./online.css";

export const metadata: Metadata = {
  title: "peasant",
  description: "The agent sessions behind your pull requests, shared with the people who review them.",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    // data-theme="dark" is what the server renders; the head script below
    // replaces it before the first paint with the person's stored choice, or
    // the system's scheme when there is none, and useTheme.ts keeps it there.
    // The attribute is expected to differ from the server's at hydration.
    <html
      lang="en"
      data-theme="dark"
      suppressHydrationWarning
      className={`${atkinsonHyperlegible.variable} ${atkinsonHyperlegibleMono.variable}`}
    >
      <head>
        <Script id="peasant-theme" strategy="beforeInteractive">
          {THEME_BOOT_SCRIPT}
        </Script>
      </head>
      <body className="min-h-screen bg-canvas text-ink antialiased">
        <LayoutShell>{children}</LayoutShell>
      </body>
    </html>
  );
}
