/**
 * Pure utility functions for quality analytics.
 */

// ---------------------------------------------------------------------------
// Project path decoding & display
// ---------------------------------------------------------------------------

/**
 * Common ancestor directory names that are never the project itself. Used when
 * recovering a project name from an encoded path.
 */
const ANCESTOR_DIRS = new Set([
  "Desktop",
  "Documents",
  "Projects",
  "Developer",
  "src",
  "code",
  "repos",
  "workspace",
  "dev",
]);

/**
 * Extract a short, human-readable project name from a raw path, host slug, or
 * Claude-encoded path. Pure — never mutates; only formats for display.
 *
 * Decoding of Claude-encoded paths happens up-front so the segment recovery
 * always operates on a real path, never an opaque dash blob.
 *
 * Handles:
 *  - Host slugs:            "~Users-acme-dev-Documents-Projects-phaze"      → "phaze"
 *  - Filesystem paths:      "/Users/vitorhugo/Documents/Projects/phaze"     → "phaze"
 *  - Windows paths:         "C:\\Users\\acme\\Projects\\phaze"              → "phaze"
 *  - Claude-encoded paths:  ".../-Users-vitorhugo-Desktop-w1-stc"           → "w1-stc"
 *  - Bare home dir:         "/Users/vitorhugo"                              → "vitorhugo"
 */
export function displayProject(project: string): string {
  if (!project) return "";

  // Host slugs look like "~Users-acme-dev-Documents-Projects-phaze".
  const segments = project.split("-");
  if (segments.length > 1 && segments[0].startsWith("~")) {
    return segments[segments.length - 1];
  }

  // Take the last path segment for filesystem / Windows paths.
  let last = project;
  if (project.includes("/") || project.includes("\\")) {
    const seg = project.split(/[\\/]/).filter(Boolean).pop();
    if (seg) last = seg;
  }

  // A Claude-encoded path is the absolute path with separators turned into
  // dashes, e.g. "-Users-vitorhugo-Desktop-w1-stc" (from
  // /Users/vitorhugo/.claude/projects/...). Detect it (a leading optional dash
  // followed by letters then a dash) and recover the project folder by
  // dropping the well-known home prefix + common ancestor dirs, keeping the
  // remaining tail joined (so "w1-stc" survives, not just "stc").
  if (/^-?[A-Za-z]+-/.test(last)) {
    const parts = last.split("-").filter(Boolean);
    if (parts.length > 0) {
      let i = 0;
      // Drop the encoded home prefix: (Users|home)-<username>-
      if (
        parts.length >= 2 &&
        (parts[0] === "Users" || parts[0] === "home")
      ) {
        i = 2;
      }
      // Drop common ancestor directories that are never the project itself.
      while (i < parts.length - 1 && ANCESTOR_DIRS.has(parts[i])) i++;
      const tail = parts.slice(i);
      if (tail.length > 0) return tail.join("-");
      return parts[parts.length - 1];
    }
  }

  return last;
}
