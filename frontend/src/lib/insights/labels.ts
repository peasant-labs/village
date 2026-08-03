import type { PhaseType } from "./types";

/**
 * Human-readable names for phase types. Single source of truth for the sticky
 * PhaseDivider section headers and anywhere else a phase is labelled.
 */
const PHASE_LABELS: Record<PhaseType, string> = {
  planning: "Planning",
  exploration: "Exploration",
  implementation: "Implementation",
  testing: "Testing",
  error: "Errors",
  debug: "Debugging",
  "retry-loop": "Retry loop",
  "user-correction": "User correction",
  recovery: "Recovery",
  abandonment: "Abandonment",
};

/** Label for a phase type, falling back to the raw value for unknown types. */
export function phaseLabel(type: PhaseType | string): string {
  return PHASE_LABELS[type as PhaseType] ?? String(type);
}
