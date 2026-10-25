import "server-only";

import type { Metadata } from "next";

import { publicOrigin } from "@/lib/server/shareCardEnv";

import { SITE_METADATA } from "./siteMetadata";

/**
 * The root layout's metadata: the site's own, plus `metadataBase` when the
 * public origin is configured, so link-preview image URLs are absolute. It
 * reads the public origin alone; the API URL plays no part in it.
 */
export function rootMetadata(): Metadata {
  const origin = publicOrigin();
  return { ...SITE_METADATA, ...(origin ? { metadataBase: origin } : {}) };
}
