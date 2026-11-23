/** The trace canvas's two views, as Fairtrade's viewer names them (its `ViewMode`). Closed. */
export const TranscriptViewMode = Object.freeze({ List: "list", Graph: "graph" } as const);
export type TranscriptViewMode = (typeof TranscriptViewMode)[keyof typeof TranscriptViewMode];
