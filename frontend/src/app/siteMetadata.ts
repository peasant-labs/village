import type { Metadata } from "next";

const title = "peasant";
const description = "The agent sessions behind your pull requests, shared with the people who review them.";

/**
 * Search engines may not list or follow any page. A public transcript is
 * readable by anyone holding its link, which is not the same as being listed
 * in search results: nobody who made a transcript public agreed to that.
 * Link-preview crawlers still read the page, since noindex only asks a search
 * engine to leave it out of its index.
 */
export const NO_SEARCH_INDEXING = { index: false, follow: false } satisfies Metadata["robots"];

/**
 * The site's own metadata. Every page carries it, and a transcript link that
 * is not public unfurls with exactly this, so its preview reveals nothing.
 */
export const SITE_METADATA = {
  title,
  description,
  robots: NO_SEARCH_INDEXING,
  openGraph: { title, description },
  twitter: { title, description },
} satisfies Metadata;
