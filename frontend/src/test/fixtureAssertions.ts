/**
 * Shared strict-shape assertion for YAML fixture loaders. `titleHeroAndBreadcrumbFixtures.ts`,
 * `finalContractCompatibilityFixtures.ts`, and `transcriptPageRequestFixtures.ts` each defined
 * their own byte-identical copy; kept in one place so a fourth fixture loader does not add a
 * fourth copy.
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
