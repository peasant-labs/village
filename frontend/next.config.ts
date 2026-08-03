import type { NextConfig } from "next";
import path from "path";

// @peasant-labs/fairtrade, transcript-browser, and analytics are all
// pre-built bundled ESM published to npm. They must NOT be in
// transpilePackages: Next would run RSC transforms on their hook-using code
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

const nextConfig: NextConfig = {
  output: "standalone",
  outputFileTracingRoot: repositoryRoot,
  experimental: {
    externalDir: true,
  },
  turbopack: {
    root: repositoryRoot,
  },
  images: {
    remotePatterns: [
      {
        protocol: "https",
        hostname: "avatars.githubusercontent.com",
      },
    ],
  },
};

export default nextConfig;
