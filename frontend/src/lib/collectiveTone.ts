/**
 * A collective's colour: one of the four palette tones, picked from its id so
 * the same collective wears the same colour on every page and every visit.
 *
 * The colour is a wayfinding aid, never information: the collective's name is
 * always written beside it. Nothing is stored; the pick is a pure function of
 * the id the server already sends.
 */
export const COLLECTIVE_TONES = ["olive", "clay", "teal", "mauve"] as const;

export type CollectiveTone = (typeof COLLECTIVE_TONES)[number];

export function collectiveTone(id: string): CollectiveTone {
  // FNV-1a over the id's UTF-16 code units: stable across browsers and builds.
  let hash = 0x811c9dc5;
  for (let i = 0; i < id.length; i += 1) {
    hash ^= id.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193) >>> 0;
  }
  return COLLECTIVE_TONES[hash % COLLECTIVE_TONES.length];
}

/** The CSS value of a tone, for components that take a colour rather than a class. */
export function collectiveToneColor(id: string): string {
  return `var(--${collectiveTone(id)})`;
}
