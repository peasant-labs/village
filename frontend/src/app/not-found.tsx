import type { Metadata } from "next";
import Link from "next/link";
import { MapPinOff } from "lucide-react";
import { OnEmpty, OnPage } from "@/components/online/Page";
import { HOME_SECTION } from "@/lib/nav/sections";

export const metadata: Metadata = {
  title: "page not found | peasant",
};

/**
 * An address no route answers. Drawn in the page's usual chrome as the same
 * empty state a missing transcript or profile uses, in place of Next's default
 * page, which paints its own black or white ground over the theme.
 */
export default function NotFound() {
  return (
    <OnPage testId="not-found">
      <OnEmpty
        icon={<MapPinOff aria-hidden="true" />}
        title="page not found"
        body="nothing lives at this address. the link may be mistyped, or the page may have moved."
        actions={
          <Link href={HOME_SECTION.href} className="btn btn-secondary">
            back to home
          </Link>
        }
      />
    </OnPage>
  );
}
