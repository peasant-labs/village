import type { Metadata } from "next";
import SignInStart from "./SignInStart";

export const metadata: Metadata = {
  title: "log in | peasant",
};

/**
 * Where the marketing site's log-in popup starts a sign-in. The provider and the
 * return address are read from the request on the server, so the page is served
 * whole and the browser leaves for the provider as soon as it runs.
 */
export default async function SignInStartRoute({
  searchParams,
}: {
  searchParams?: Promise<Record<string, string | string[] | undefined>>;
}) {
  const params = (await searchParams) ?? {};
  const one = (value: string | string[] | undefined) => (typeof value === "string" ? value : null);
  return <SignInStart provider={one(params.provider)} next={one(params.next)} />;
}
