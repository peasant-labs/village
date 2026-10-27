import type { Metadata } from "next";
import UnsubscribePage from "./UnsubscribePage";

export const metadata: Metadata = {
  title: "stop invitation emails | peasant",
};

/**
 * Where an invitation email's unsubscribe link lands, signed out. The token is
 * read from the request on the server and handed to the page; nothing is sent
 * until the person presses the button, because mail scanners open links.
 */
export default async function UnsubscribeRoute({
  searchParams,
}: {
  searchParams?: Promise<Record<string, string | string[] | undefined>>;
}) {
  const params = (await searchParams) ?? {};
  const token = params.token;
  return <UnsubscribePage token={typeof token === "string" ? token : null} />;
}
