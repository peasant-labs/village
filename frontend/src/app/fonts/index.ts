import localFont from "next/font/local";

/**
 * Self-hosted fairtrade typefaces (Atkinson Hyperlegible / …Mono) — replaces
 * importing "@peasant-labs/fairtrade/fonts.css", which loads the same
 * families via a REMOTE `@import url("https://fonts.googleapis.com/...")`.
 *
 * Two problems with that remote-@import approach (mirrors peasant's own fix —
 * see peasant web/src/app/fonts/index.ts):
 *  1. Next's CSS bundling (Turbopack/Lightning CSS) relocates `@import` rules
 *     that aren't the first thing in the merged stylesheet — which an
 *     `@import` sourced from an app-level CSS import chain (fonts.css after
 *     other imports) usually isn't — and silently DROPS the relocated,
 *     now-invalid `@import`, leaving design-system text on its
 *     ui-sans-serif/ui-monospace fallback with no visible error.
 *  2. Even when it isn't dropped, every page load phones home to
 *     fonts.googleapis.com/fonts.gstatic.com and shows a flash of fallback
 *     font (FOUT) while that round-trip resolves.
 *
 * next/font/local self-hosts the exact same woff2 files (vendored under this
 * directory, sourced from the same fonts.gstatic.com URLs fairtrade's own
 * fonts.css `@import` references, latin subset only) at BUILD time — Next
 * inlines the @font-face + a generated, collision-free font-family name, with
 * no runtime request to Google.
 */
export const atkinsonHyperlegible = localFont({
  src: [
    { path: "./atkinson-hyperlegible-400.woff2", weight: "400", style: "normal" },
    { path: "./atkinson-hyperlegible-700.woff2", weight: "700", style: "normal" },
    { path: "./atkinson-hyperlegible-400-italic.woff2", weight: "400", style: "italic" },
    { path: "./atkinson-hyperlegible-700-italic.woff2", weight: "700", style: "italic" },
  ],
  variable: "--font-atkinson-hyperlegible",
  display: "swap",
  preload: true,
});

export const atkinsonHyperlegibleMono = localFont({
  src: [
    { path: "./atkinson-hyperlegible-mono-400.woff2", weight: "400 700", style: "normal" },
    { path: "./atkinson-hyperlegible-mono-400-italic.woff2", weight: "400", style: "italic" },
  ],
  variable: "--font-atkinson-hyperlegible-mono",
  display: "swap",
  preload: true,
});
