/**
 * Shared assertions for YAML fixture loaders. `titleHeroAndBreadcrumbFixtures.ts`,
 * `finalContractCompatibilityFixtures.ts`, and `transcriptPageRequestFixtures.ts` each defined
 * their own byte-identical copy of the strict-shape check; kept in one place so a fourth fixture
 * loader does not add a fourth copy. The required-name check below went the same way: two loaders
 * had grown identical copies of it.
 */

/** Throws with the exact got/want field sets when `value`'s keys differ from `expected` — a
 *  fixture row with an unknown or missing field fails loudly instead of silently reading
 *  `undefined`. */
export function assertExactKeys(value: object, expected: string[], location: string): void {
  const actual = Object.keys(value).sort();
  const wanted = [...expected].sort();
  if (JSON.stringify(actual) !== JSON.stringify(wanted)) {
    throw new Error(`${location} has unknown or missing fields: got ${actual.join(", ")}; want ${wanted.join(", ")}`);
  }
}

/**
 * Throws when the case names present differ from the ones a loader declares it
 * requires — the deletion guard every fixture corpus uses.
 *
 * A required-NAME manifest, never a tally: a count churns on every legitimate
 * addition and conflicts whenever two changes append at once, while a name set
 * says exactly which case went missing.
 */
export function assertNamesMatch(
  actual: string[],
  required: readonly string[],
  label: string,
): void {
  const got = [...actual].sort();
  const want = [...required].sort();
  if (JSON.stringify(got) !== JSON.stringify(want)) {
    throw new Error(
      `${label} case names differ: got ${got.join(", ")}; want ${want.join(", ")}. ` +
        `A case was added, renamed or deleted without updating this loader's required-name ` +
        `manifest, so the corpus no longer covers what the manifest claims. Add the new name ` +
        `to the manifest, or restore the missing case.`,
    );
  }
  if (new Set(actual).size !== actual.length) {
    throw new Error(`${label} fixture case names must be unique`);
  }
}
