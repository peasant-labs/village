import { API_URL_BASE } from "@/lib/api";

/**
 * The providers village offers at sign-in, in the order fairtrade's
 * `SignInProviders` renders them (`[0]` is the primary button, the rest go
 * behind its chevron).
 *
 * GitHub only. The other providers are hidden, not deleted: their server
 * routes stay mounted, and a provider an operator has not configured already
 * answers 503. The label is lowercase chrome like every other control;
 * fairtrade's split button lowercases it too.
 */
export const SIGN_IN_PROVIDERS: { id: string; label: string }[] = [
  { id: "github", label: "github" },
];

/** Leaves the app for the provider's OAuth start route. */
export function startSignIn(providerId: string): void {
  window.location.href = `${API_URL_BASE}/auth/${providerId}`;
}
