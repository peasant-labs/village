import { MARKETING_URL } from "@/lib/marketing";

/**
 * Where the other two peasant apps live, for the sidebar's app switcher and
 * its help group. Both open outside this app, and the sidebar says so.
 */

/** The local app the `peasant` binary serves on the person's own computer. */
export const LOCAL_APP_URL = "http://localhost:8690";

/** The documentation, on the marketing site. */
export const DOCS_URL = `${MARKETING_URL.replace(/\/+$/, "")}/docs`;
