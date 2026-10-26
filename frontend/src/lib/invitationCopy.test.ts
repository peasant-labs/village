import { describe, expect, it } from "vitest";
import { ApiError } from "@/lib/api";
import { inviteRefusal } from "@/lib/invitationCopy";
import { assertCaseNames, readFixture } from "@/test/mountedCollectivesSurfaces";

interface RefusalCase {
  name: string;
  username: string;
  status: number;
  error: string;
  expect: string;
}

const cases = readFixture("invitation-copy.yaml").refusalCases as RefusalCase[];
assertCaseNames("invitation-copy.yaml refusalCases", cases.map((c) => c.name), [
  "a-consenting-member-is-already-a-member",
  "the-limit-is-said-plainly",
  "a-malformed-name-says-what-to-type",
  "a-signed-out-owner-is-asked-to-sign-in",
  "a-non-owner-is-told-who-may-invite",
  "a-server-failure-never-carries-the-server-message",
  "a-dropped-connection-is-the-same-line",
]);

describe("why an invitation was not sent", () => {
  it.each(cases.map((c) => [c.name, c] as const))("%s", (_name, c) => {
    const error = c.status === 0 ? new TypeError(c.error) : new ApiError(c.status, c.error);
    const line = inviteRefusal(c.username, error);
    expect(line).toBe(c.expect);
    // Chrome is lowercase apart from the handle, and never ends in "..".
    expect(line.replace(`@${c.username}`, "")).toBe(line.replace(`@${c.username}`, "").toLowerCase());
    expect(line).not.toMatch(/\.\.$/);
    if (c.error) expect(line).not.toContain(c.error);
  });
});
