import type { VillageSessionListPayload, VillageTranscriptListResponse } from "@peasant-labs/schema";

export interface TranscriptPaging {
  page: number;
  perPage: number;
  total: number;
  hasMore: boolean;
}

type FlatPaging = Pick<VillageTranscriptListResponse, "page" | "per_page" | "total" | "next_items">;
type GroupedPaging = Pick<VillageSessionListPayload, "page" | "limit" | "totalItems">;

/**
 * The grouped payload has different item shapes and totals from the flat list.
 * Their wire names are deliberately different for now; handler-side unification
 * is deferred until a consumer needs a single vocabulary on the wire.
 */
export function adaptTranscriptPaging(response: FlatPaging | GroupedPaging): TranscriptPaging {
  if ("per_page" in response) {
    return {
      page: response.page,
      perPage: response.per_page,
      total: response.total,
      hasMore: response.next_items.length > 0,
    };
  }
  return {
    page: response.page,
    perPage: response.limit,
    total: response.totalItems,
    hasMore: response.page * response.limit < response.totalItems,
  };
}
