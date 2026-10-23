/**
 * Where a signed-out visitor to `/` is sent: the peasant marketing site.
 *
 * The hosted app has no front page of its own for somebody who is not signed
 * in; the product is introduced on the marketing site, which links back to
 * `/login`. An operator can point it elsewhere (a staging site, a mirror) with
 * `NEXT_PUBLIC_MARKETING_URL`, read at build time like every other public value.
 */
export const DEFAULT_MARKETING_URL = "https://peasantlabs.org";

export const MARKETING_URL: string =
  process.env.NEXT_PUBLIC_MARKETING_URL?.trim() || DEFAULT_MARKETING_URL;
