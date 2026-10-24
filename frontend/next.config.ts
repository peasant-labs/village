import type { NextConfig } from "next";
import path from "path";

// @peasant-labs/fairtrade (including its graph and analytics entries) is
// pre-built bundled ESM published to npm. It must NOT be in
// transpilePackages: Next would run RSC transforms on its hook-using code
// without 'use client' guards → useState treated as null at prerender.
//
// A manual resolve.alias for react/react-dom is also harmful: it overrides
// Next's 'react-server' export-condition resolution → useState null in the
// server bundle as well. The locked dependency graph resolves one React 19 when all packages
// align on the same peer range — no manual dedup needed.
//
// The frontend is the single member of the repository-local pnpm workspace.
// Dependencies, including Fairtrade, resolve from the registry into the root
// pnpm store, so both compilation and standalone tracing need only this repo.
const repositoryRoot = path.resolve(__dirname, "..");

const shareCardFontFiles = [
  "./node_modules/@peasant-labs/fairtrade/package.json",
  "./node_modules/@peasant-labs/fairtrade/dist/lib/share-fonts/*",
];

const nextConfig: NextConfig = {
  output: "standalone",
  outputFileTracingRoot: repositoryRoot,
  // The share-card image routes read fairtrade's card fonts from disk by a
  // name computed at run time, which tracing cannot see, so they are listed,
  // with the package manifest whose exports map resolves them. The keys are
  // globs, so the brackets of the dynamic segment are escaped.
  outputFileTracingIncludes: {
    "/transcripts/\\[id\\]/opengraph-image": shareCardFontFiles,
    "/transcripts/\\[id\\]/twitter-image": shareCardFontFiles,
  },
  experimental: {
    externalDir: true,
  },
  turbopack: {
    root: repositoryRoot,
  },
  images: {
    // The optimizer is allowed to fetch GitHub avatars, but every avatar in the
    // app renders through next/image with `unoptimized`, and that is deliberate.
    // `users.avatar_url` is whichever URL the sign-in provider supplied, and
    // village supports several: GitHub (avatars.githubusercontent.com), GitLab
    // (its uploads or gravatar), HuggingFace (its CDN) and Codeberg (SourceHut
    // supplies none at all). The optimizer refuses any host not listed here and
    // answers 400, so an incomplete allowlist would break those members' avatars
    // outright — a worse outcome than an unoptimized 16-64px avatar. Pages that
    // change this should re-check every provider's avatar host, not just GitHub's.
    remotePatterns: [
      {
        protocol: "https",
        hostname: "avatars.githubusercontent.com",
      },
    ],
  },
};

export default nextConfig;
