import type { adaptTranscript } from '@peasant-labs/fairtrade/ui';
import { displayProject } from '@/lib/quality/utils';
import { EXPLORE_SECTION } from '@/lib/nav/sections';

/** Same label the explore card (`TranscriptCard.tsx`) shows for a transcript
 *  with no stored title. The hero must show this — never the composite's own
 *  fallback chain, which derives a heading from the first `role: user` turn
 *  and can surface raw harness preamble (village#32). */
export const UNTITLED_TRANSCRIPT_LABEL = 'Untitled transcript';

/** Breadcrumb crumbs share one line in a fixed-height trail. A stored title
 *  is frequently a whole sentence, so bound it the way fairtrade bounds the
 *  hero title (`TranscriptViewer.jsx`'s `rawTitle`/`cut` guard), at a
 *  shorter, breadcrumb-appropriate length. The `codePointAt` check keeps the
 *  cut from landing inside a surrogate pair (no mojibake before the
 *  ellipsis). */
export function truncateCrumbLabel(raw: string): string {
  const MAX = 60;
  if (raw.length <= MAX) return raw;
  const cut = (raw.codePointAt(MAX - 2) ?? 0) > 0xffff ? MAX - 2 : MAX - 1;
  return raw.slice(0, cut).trimEnd() + '…';
}

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
      title: storedTitle?.trim() || UNTITLED_TRANSCRIPT_LABEL,
    },
  };
}

export interface TranscriptBreadcrumbInput {
  /** Peasant's privacy-safe project label. */
  project: string;
  /** The transcript's raw stored title (untrimmed is fine; trimmed here). */
  storedTitle: string | null | undefined;
  /** The real village transcript id — the id in this page's own
   *  `/transcripts/<id>` URL — used for the short-id fallback. Never
   *  Peasant's local session id. */
  transcriptId: string;
}

export interface TranscriptBreadcrumbCrumb {
  label: string;
  href?: string;
}

/**
 * Builds the detail page's host trail through the app router (lowercase
 * chrome): (1) the nav registry's home crumb; (2) the project label with no
 * href (village has no `/projects` route, but the label still carries
 * meaning — Peasant's privacy-safe project label); (3) the last crumb,
 * reading the RAW stored title (trimmed, truncated) — not the hero's
 * overlaid "Untitled transcript" placeholder — so an untitled transcript
 * shows the short VILLAGE transcript id instead of repeating "Untitled
 * transcript" three words wide in a breadcrumb.
 *
 * The single call site both `SessionDetailV2` and the dev-only visual
 * harness use, so the two surfaces cannot drift onto independently
 * hand-copied rules.
 */
export function buildTranscriptBreadcrumb({
  project,
  storedTitle,
  transcriptId,
}: TranscriptBreadcrumbInput): TranscriptBreadcrumbCrumb[] {
  const trimmedTitle = storedTitle?.trim() ?? '';
  const shortVillageId = transcriptId.slice(0, 8);
  const crumbTitle = trimmedTitle ? truncateCrumbLabel(trimmedTitle) : shortVillageId;
  return [
    { label: EXPLORE_SECTION.label, href: EXPLORE_SECTION.href },
    { label: displayProject(project) },
    { label: crumbTitle },
  ];
}
