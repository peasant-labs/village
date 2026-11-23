/**
 * Fairtrade DOM selectors read by Village's scroll-position persistence and
 * offset restore. A Fairtrade scroll-restore API (reporting and restoring an
 * active row's viewport offset) would replace this module.
 */
export const FairtradeScrollSelector = Object.freeze({
  Stream: '.txn-stream',
  EarlierHistory: '.txn-earlier',
  TurnAttribute: 'data-turn',
} as const);

/** Selector for a rendered turn row by its served position. */
export function turnRowSelector(position: number): string {
  return `[${FairtradeScrollSelector.TurnAttribute}="${position}"]`;
}
