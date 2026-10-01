/**
 * What a person is told when the handle they asked for belongs to somebody
 * else. One sentence, shared by the two places a handle is chosen (`/welcome`
 * and the settings page), so the same refusal never reads two ways.
 *
 * It restates the server's answer (a 409 from `PATCH /auth/me/username`) and
 * decides nothing: whether a handle is free is only ever the server's call.
 */
export function handleTakenMessage(handle: string): string {
  return `@${handle} is already claimed. try another.`;
}
