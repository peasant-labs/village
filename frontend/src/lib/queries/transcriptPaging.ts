import { AllPageSizes, isPageSize, type PageSize, type VillageSessionListPayload, type VillageTranscriptListResponse } from "@peasant-labs/schema";

export interface TranscriptPaging {
  page: number;
  perPage: PageSize;
  total: number;
  hasMore: boolean;
}

type FlatPaging = Pick<VillageTranscriptListResponse, "page" | "per_page" | "total" | "next_items">;
type GroupedPaging = Pick<VillageSessionListPayload, "page" | "limit" | "totalItems">;

export class TranscriptPageSizeError extends Error {
  constructor(readonly received: number) {
    super(`Transcript list response rejected before caching: page size ${received} is not supported; accepted sizes are ${AllPageSizes.join(", ")}. Retry the list, and check the deployed API if it persists.`);
    this.name = "TranscriptPageSizeError";
  }
}

function parsePageSize(received: number): PageSize {
  // A server echo describes actual capacity, not a user preference. Defaulting
  // would mislabel the page and could mislead continuation, so fail closed.
  if (!isPageSize(received)) throw new TranscriptPageSizeError(received);
  return received;
}

/**
 * The grouped payload has different item shapes and totals from the flat list.
 * Their wire names are deliberately different for now; handler-side unification
 * is deferred until a consumer needs a single vocabulary on the wire.
 */
export function adaptTranscriptPaging(response: FlatPaging | GroupedPaging): TranscriptPaging {
  if ("per_page" in response) {
    return {
      page: response.page,
      perPage: parsePageSize(response.per_page),
      total: response.total,
      hasMore: response.next_items.length > 0,
    };
  }
  const perPage = parsePageSize(response.limit);
  return {
    page: response.page,
    perPage,
    total: response.totalItems,
    hasMore: response.page * perPage < response.totalItems,
  };
}
