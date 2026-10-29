const DIGEST = /^[0-9a-f]{64}$/;

/**
 * The hash of the rendered content, from the content route's
 * `Village-Content-Hash` response header. Returns the value when it is exactly
 * 64 lowercase hex characters once surrounding whitespace is trimmed, and null
 * for a missing or malformed header. Fairtrade compares it with the detail
 * response's content hash; nothing here does.
 */
export function readContentHashHeader(header: string | null): string | null {
  if (header == null) return null;
  const value = header.trim();
  return DIGEST.test(value) ? value : null;
}
