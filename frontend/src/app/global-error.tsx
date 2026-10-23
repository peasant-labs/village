"use client";

import Link from "next/link";
import { atkinsonHyperlegible, atkinsonHyperlegibleMono } from "@/app/fonts";
import PeasantWordmark from "@/components/online/PeasantWordmark";
import { useTheme } from "@/hooks/useTheme";
import { THEME_BOOT_SCRIPT } from "@/lib/themeBoot";
import "@peasant-labs/fairtrade/components.css";
import "./globals.css";
import "./online.css";

/**
 * The last fallback: the root layout itself failed, so this replaces the whole
 * document and cannot lean on the shell, its providers or its stylesheets. It
 * brings its own fonts, styles and theme, and draws the sign-in card's frame,
 * in place of Next's unstyled default. The error's own message is never shown.
 */
export default function GlobalError({ reset }: { error: Error & { digest?: string }; reset: () => void }) {
  useTheme();
  return (
    <html
      lang="en"
      data-theme="dark"
      suppressHydrationWarning
      className={`${atkinsonHyperlegible.variable} ${atkinsonHyperlegibleMono.variable}`}
    >
      <head>
        {/* The same fixed head script the root layout runs, for a document the
            server draws; on the client useTheme applies the theme instead. */}
        <script dangerouslySetInnerHTML={{ __html: THEME_BOOT_SCRIPT }} />
        <title>something went wrong | peasant</title>
      </head>
      <body className="min-h-screen bg-canvas text-ink antialiased">
        <main id="main" className="on-login" data-testid="global-error">
          <section className="on-login-card" aria-labelledby="global-error-heading">
            <header>
              <PeasantWordmark />
              <h1 id="global-error-heading">peasant could not load</h1>
            </header>
            <div className="on-login-body">
              <p className="on-muted">something went wrong before the page could draw. try again in a moment.</p>
              <button type="button" className="btn btn-primary" onClick={() => reset()}>
                try again
              </button>
            </div>
            <footer>
              <Link href="/" className="on-link on-chrome">
                back to home
              </Link>
            </footer>
          </section>
        </main>
      </body>
    </html>
  );
}
