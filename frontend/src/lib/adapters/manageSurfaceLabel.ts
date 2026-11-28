import { closed } from "@peasant-labs/fairtrade/closed-value";

/**
 * Names the lifted Manage surface renders for its own controls. Fairtrade
 * chooses them; village names them here so a check that finds a control by
 * its text fails loudly when the surface renames it.
 */
export const ManageSurfaceLabel = {
  Contribute: closed("contribute", "the collective's action that opens its contribute page"),
} as const;
