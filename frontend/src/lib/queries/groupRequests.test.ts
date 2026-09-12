import { zVillageCreateGroupRequest, zVillageUpdateGroupRequest } from "@peasant-labs/schema";
import { describe, expect, it } from "vitest";
import { createGroupRequest, updateGroupRequest } from "./groupRequests";

/**
 * The bodies the collectives pages send are checked against the contract's
 * own parsers, which ship in the dependency. The server enforces the same
 * schemas, so a body these parsers refuse is a body the server refuses.
 */
describe("createGroupRequest", () => {
  it("omits the organisation when the form leaves it blank, because the contract's create request does not accept null", () => {
    const body = createGroupRequest({ name: "night shift", purpose: "late sessions", mode: "open", access: "members_only", org: "" });
    expect(body).not.toHaveProperty("linked_github_org");
    expect(() => zVillageCreateGroupRequest.parse(body)).not.toThrow();
  });

  it("carries the organisation when the form names one", () => {
    const body = createGroupRequest({ name: "night shift", purpose: "", mode: "curated", access: "public", org: "acme" });
    expect(body.linked_github_org).toBe("acme");
    expect(() => zVillageCreateGroupRequest.parse(body)).not.toThrow();
  });

  it("refuses an acceptance mode outside the contract's closed set before anything is sent", () => {
    expect(() => createGroupRequest({ name: "x", purpose: "", mode: "whenever", access: "public", org: "" })).toThrow();
  });
});

describe("updateGroupRequest", () => {
  it("clears the organisation with null, which the contract's update request accepts", () => {
    const body = updateGroupRequest({
      name: "renamed",
      description: "",
      acceptance_mode: "open",
      data_access: "members_only",
      linked_github_org: null,
      display_members: true,
      transcript_deletion_policy: "user_choice",
    });
    expect(body.linked_github_org).toBeNull();
    expect(() => zVillageUpdateGroupRequest.parse(body)).not.toThrow();
  });

  it("refuses a data access policy outside the contract's closed set before anything is sent", () => {
    expect(() =>
      updateGroupRequest({
        name: "renamed",
        description: "",
        acceptance_mode: "open",
        data_access: "everyone",
        linked_github_org: null,
        display_members: true,
        transcript_deletion_policy: "user_choice",
      }),
    ).toThrow();
  });
});
