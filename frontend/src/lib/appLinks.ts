import { MARKETING_URL } from "@/lib/marketing";

/** The documentation, on the marketing site: the sidebar's app switcher opens it outside this app. */
export const DOCS_URL = `${MARKETING_URL.replace(/\/+$/, "")}/docs`;

/*
 * How-to lives only in the docs; the app links to the page that explains a
 * thing rather than restating it.
 */

/** Installing peasant, setting it up, and publishing a session (and the license publishing grants). */
export const DOCS_PUBLISH_URL = `${DOCS_URL}#publish`;

/** How a pull request gets its transcripts: `/peasant attach`, automatic linking, and the waiting list. */
export const DOCS_PULL_REQUESTS_URL = `${DOCS_URL}/guides/reviews`;
