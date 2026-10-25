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
  "any-other-refusal-carries-the-server-reason",
]);

describe("why an invitation was not sent", () => {
  it.each(cases.map((c) => [c.name, c] as const))("%s", (_name, c) => {
    expect(inviteRefusal(c.username, new ApiError(c.status, c.error))).toBe(c.expect);
  });
});
