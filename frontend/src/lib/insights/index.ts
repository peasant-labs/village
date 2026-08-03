/**
 * Insight computation pipeline — barrel export.
 *
 * Used by the session detail viewer.
 */

// Types
export type { Phase } from "./types";

// Phase detection
export { detectPhases } from "./phase-detection";

// Phase labels (shared by the inline divider + sticky phase bar)
export { phaseLabel } from "./labels";
