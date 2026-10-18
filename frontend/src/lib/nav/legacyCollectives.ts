import { redirect } from "next/navigation";

export type LegacySearchParams = Record<string, string | string[] | undefined>;

/** Keep inbound links working for the complete compatibility release window. */
export function redirectLegacyCollective(path: string, search: LegacySearchParams): never {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(search)) {
    for (const item of Array.isArray(value) ? value : value === undefined ? [] : [value]) {
      query.append(key, item);
    }
  }
  redirect(`${path}${query.size ? `?${query}` : ""}`);
}
