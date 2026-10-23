import type { Metadata } from "next";
import LoginPage from "./LoginPage";

export const metadata: Metadata = {
  title: "log in | peasant",
};

/**
 * The sign-in page. The return address is read from the request on the server,
 * so the page is served whole (no client-only bail-out) and works before any
 * script has run.
 */
export default async function LoginRoute({
  searchParams,
}: {
  searchParams?: Promise<Record<string, string | string[] | undefined>>;
}) {
  const params = (await searchParams) ?? {};
  const next = params.next;
  return <LoginPage next={typeof next === "string" ? next : null} />;
}
