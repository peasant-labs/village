import type { Metadata } from "next";
import { LayoutShell } from "@/components/LayoutShell";
import { atkinsonHyperlegible, atkinsonHyperlegibleMono } from "@/app/fonts";
// CSS import order: fairtrade components.css (tokens + base + component
// styles) FIRST, then the fairtrade analytics per-surface bundle (nothing
// auto-loads it — the dashboard ships unstyled without the explicit import),
// then app globals, then transcript-browser styles. Always import fairtrade
// components.css (never base.css standalone) so its layered tokens/base win.
// The old analytics-last ordering hack (the external package's own layered
// styles had to win over app CSS) is gone with the package — the fairtrade
// surface is token-driven, so order among the fairtrade sheets no longer
// carries theming semantics.
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
import "./globals.css";
import "@peasant-labs/transcript-browser/styles.css";

export const metadata: Metadata = {
  title: "village | peasant labs",
  description: "A commons for AI agent transcripts",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    // data-theme="dark" is the server-rendered default; useTheme.ts takes over
    // on the client and applies the stored preference (dark unless overridden).
    <html
      lang="en"
      data-theme="dark"
      className={`${atkinsonHyperlegible.variable} ${atkinsonHyperlegibleMono.variable}`}
    >
      <body className="min-h-screen bg-surface text-ink antialiased">
        <LayoutShell>
          <main className="min-h-screen pt-[var(--app-header-height)]">{children}</main>
        </LayoutShell>
      </body>
    </html>
  );
}
