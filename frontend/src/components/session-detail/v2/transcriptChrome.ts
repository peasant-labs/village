import type { adaptTranscript } from '@peasant-labs/fairtrade/ui';
import { HOME_SECTION } from '@/lib/nav/sections';
import type { TranscriptContext } from '@/lib/transcriptContext';
import { publicProfile, type PublicOwner } from '@/lib/publicOwner';

/** Same label the explore card (`TranscriptCard.tsx`) shows for a transcript
 *  with no stored title. The hero must show this — never the composite's own
 *  fallback chain, which derives a heading from the first `role: user` turn
 *  and can surface raw harness preamble (village#32). */
export const UNTITLED_TRANSCRIPT_LABEL = 'Untitled transcript';

type TranscriptViewModel = ReturnType<typeof adaptTranscript>;

/**
 * Overlays the stored transcript title onto an adapted view model's session.
 * `SessionVM.title` is documented as "render-when-present; else the
 * consumer derives one from the first prompt". `adaptTranscript` never sets
 * it, so with nothing supplied the hero falls back to the composite's own
 * derivation — the first `role: user` turn — which can surface raw harness
 * preamble (e.g. `<local-command-caveat>Caveat: ...`) as the headline
 * whenever a session opens with tooling setup (village#32). The explore card
 * already shows the stored title, so the hero must show the SAME title, not
 * a re-derived one: always overlay it, falling back to the explore card's
 * own empty-title label rather than letting the composite reach past it to
 * the raw turn.
 *
 * The single call site both `SessionDetailV2` and the dev-only visual
 * harness use, so the two surfaces cannot drift onto independently
 * hand-copied rules.
 */
export function overlayStoredTitle(
  adapted: TranscriptViewModel,
  storedTitle: string | null | undefined,
): TranscriptViewModel {
  return {
    ...adapted,
    session: {
      ...adapted.session,
      title: shownTranscriptTitle(storedTitle),
    },
  };
}

/**
 * The title the transcript page shows for a stored title: the stored title,
 * or the explore card's empty-title label when it is blank. The page hero and
 * the link preview both call this, so a link unfurls with the page's title.
 */
export function shownTranscriptTitle(storedTitle: string | null | undefined): string {
  return storedTitle?.trim() || UNTITLED_TRANSCRIPT_LABEL;
}

/**
 * Builds the project page href `/users/{username}/projects/{projectHash}`
 * or `null` when there is no profile or no hash, so a caller with an
 * incomplete transcript record degrades to a label-only crumb instead of
 * emitting a broken link. An anonymous owner (or one of a kind this app does
 * not know) has no username to put in a link, so it never gets one.
 */
export function buildProjectHref(
  owner: PublicOwner | null | undefined,
  projectHash: string | null | undefined,
): string | null {
  const profile = publicProfile(owner);
  if (!profile || !projectHash) return null;
  return `/users/${encodeURIComponent(profile.username)}/projects/${encodeURIComponent(projectHash)}`;
}

export interface TranscriptBreadcrumbInput {
  /** A signed-out reader gets the same trail with no links: every place it
   *  names (home, collectives, pull requests, the project page) needs a session. */
  isLoggedIn?: boolean;
  /** The one server-resolved project display name (`Transcript.project_display_name`)
   *  — never a locally re-derived name. `null` when the server withheld it
   *  (an anonymous owner): the trail then names no project at all. */
  project: string | null;
  /** Href to the project page (`/users/{username}/projects/{projectHash}`),
   *  or `null` when the caller cannot build one (missing owner username or
   *  project hash). When `null` the crumb renders as a label with no link. */
  projectHref: string | null;
  /** Where the transcript was opened from; home when absent. */
  context?: TranscriptContext | null;
  /** The collective's name, for a collective context. */
  collectiveName?: string | null;
}

export interface TranscriptBreadcrumbCrumb {
  label: string;
  href?: string;
}

/**
 * Builds the detail page's host trail through the app router (lowercase
 * chrome): the place the transcript was opened from, never the transcript's
 * own title, which the heading under the trail already shows.
 *
 *   home          home, then the project (linking to its page when known);
 *                 no project crumb when the project is withheld
 *   a collective  collectives, then the collective
 *   a pull        pull requests, then owner/repo#n
 *
 * The single call site both `SessionDetailV2` and the dev-only visual
 * harness use, so the two surfaces cannot drift onto independently
 * hand-copied rules.
 */
export function buildTranscriptBreadcrumb({
  project,
  projectHref,
  isLoggedIn = true,
  context,
  collectiveName,
}: TranscriptBreadcrumbInput): TranscriptBreadcrumbCrumb[] {
  const trail = placeTrail({ project, projectHref, context, collectiveName });
  return isLoggedIn ? trail : trail.map(({ label }) => ({ label }));
}

function placeTrail({
  project,
  projectHref,
  context,
  collectiveName,
}: TranscriptBreadcrumbInput): TranscriptBreadcrumbCrumb[] {
  if (context?.kind === "collective") {
    return [
      { label: "collectives", href: "/collectives" },
      { label: collectiveName?.trim() || "collective", href: `/collectives/${encodeURIComponent(context.id)}` },
    ];
  }
  if (context?.kind === "pull") {
    const path = [context.owner, context.name, String(context.number)].map(encodeURIComponent).join("/");
    return [
      { label: "pull requests", href: "/pulls" },
      { label: `${context.owner}/${context.name}#${context.number}`, href: `/pulls/${path}` },
    ];
  }
  return [
    { label: HOME_SECTION.label, href: HOME_SECTION.href },
    ...(project == null ? [] : [{ label: project, ...(projectHref ? { href: projectHref } : {}) }]),
  ];
}
