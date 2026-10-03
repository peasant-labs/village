import { API_URL_BASE } from "@/lib/api";

/**
 * Full-page URL for the GitHub App install handshake for one collective; the
 * backend redirects to GitHub, and GitHub returns to the collective's settings
 * with `?github_installed=1`.
 */
export function githubInstallURL(groupId: string): string {
  return `${API_URL_BASE}/integrations/github/install?group_id=${encodeURIComponent(groupId)}`;
}
