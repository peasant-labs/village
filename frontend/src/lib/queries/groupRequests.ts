import {
  zVillageCreateGroupRequest,
  zVillageUpdateGroupRequest,
  type VillageCreateGroupRequest,
  type VillageUpdateGroupRequest,
} from "@peasant-labs/schema";

/** What the create-collective dialog hands back. */
export interface CreateGroupForm {
  name: string;
  purpose: string;
  mode: string;
  access: string;
  org: string;
}

/**
 * Builds the create body the contract accepts and proves it with the
 * contract's own parser before anything is sent; the server enforces the same
 * schema, so a body the parser refuses is a body the server refuses. The
 * organisation is omitted when blank: the create request types it as a string
 * and does not accept null, unlike the update request.
 */
export function createGroupRequest(form: CreateGroupForm): VillageCreateGroupRequest {
  const body: Record<string, unknown> = {
    name: form.name,
    description: form.purpose,
    acceptance_mode: form.mode,
    data_access: form.access,
  };
  if (form.org) {
    body.linked_github_org = form.org;
  }
  return zVillageCreateGroupRequest.parse(body);
}

/** What the collective settings form hands back. */
export interface UpdateGroupForm {
  name: string;
  description: string;
  acceptance_mode: string;
  data_access: string;
  linked_github_org: string | null;
  display_members?: boolean;
  transcript_deletion_policy?: string;
}

/**
 * Builds the update body and proves it with the contract's own parser before
 * anything is sent. A null organisation clears the link; the update request
 * accepts null where the create request does not.
 */
export function updateGroupRequest(form: UpdateGroupForm): VillageUpdateGroupRequest {
  return zVillageUpdateGroupRequest.parse(form);
}
