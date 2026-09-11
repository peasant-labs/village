import type { Metadata } from "next";
import { PrivacyNotice } from "./PrivacyNotice";

// Static route: no data fetch, so a server component with route metadata.
// The gallery and tab name carry the strict term; the description carries the
// familiar one so search and link previews match both.
export const metadata: Metadata = {
  title: "privacy notice | village",
  description:
    "Village privacy notice (privacy policy): who operates Village and what a public contribution licenses.",
};

export default function PrivacyPage() {
  return <PrivacyNotice />;
}
