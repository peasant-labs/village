import { redirect } from "next/navigation";
import { DOCS_PUBLISH_URL } from "@/lib/appLinks";

/**
 * The address the publishing dashboard had. How to publish lives in the docs
 * now (install, set up, push, and what publishing licenses), and the person's
 * published transcripts are on home, so the address leads to the docs.
 */
export default function PublishRoute() {
  redirect(DOCS_PUBLISH_URL);
}
