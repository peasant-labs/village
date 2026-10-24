import type { Metadata } from "next";

const title = "peasant";
const description = "The agent sessions behind your pull requests, shared with the people who review them.";

/**
 * The site's own metadata. Every page carries it, and a transcript link that
 * is not public unfurls with exactly this, so its preview reveals nothing.
 */
export const SITE_METADATA = {
  title,
  description,
  openGraph: { title, description },
  twitter: { title, description },
} satisfies Metadata;
