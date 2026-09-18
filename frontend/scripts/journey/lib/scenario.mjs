/* Scenario control for the composed journey mock (scripts/journey/mock.mjs).
 *
 * A journey declares the world it needs before navigating, instead of relying on
 * an env var or a mock restart. The mock's scenario persists for the run, so
 * callers reset it when done.
 */
const MOCK_BASE =
  process.env.JOURNEY_MOCK_BASE || `http://localhost:${process.env.JOURNEY_MOCK_PORT || 8799}`

/** Set the composed mock's scenario. Throws if the mock rejects it. */
export async function setScenario(request, name) {
  const res = await request.post(`${MOCK_BASE}/__mock/scenario`, { data: { name } })
  if (!res.ok()) {
    throw new Error(`failed to set mock scenario "${name}": HTTP ${res.status()}`)
  }
  return res.json()
}