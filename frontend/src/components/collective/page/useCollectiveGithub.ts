"use client";

import { useQueryClient } from "@tanstack/react-query";
import {
  isNotConfigured,
  useAvailableRepositories,
  useLinkRepository,
  useRepositories,
  useUnlinkRepository,
} from "@/lib/queries/repositories";
import { linkedRepoIds, orgSummary, repoPickerOwners, type RepoPickerOwner } from "@/lib/adapters/collective";
import { applyRepoLinks, repoLinkMessage, repoLinkSteps, splitRepo } from "@/lib/repoLinks";

export interface CollectiveGithub {
  /** False when the server answered 501: the GitHub App is not set up here. */
  configured: boolean;
  /** The org line: null when nothing is linked; the `of M` total is the owner's alone. */
  org: { login: string; linked?: number; total?: number } | null;
  owners: RepoPickerOwner[];
  linkedIds: string[];
  /** The owner may open the repo picker. */
  canManage: boolean;
  /** The owner's way forward is the install handshake. */
  needsInstall: boolean;
  /** Link and unlink what the picker changed; resolves to the message the page shows. */
  saveLinks: (diff: { add: string[]; remove: string[] }) => Promise<{ ok: boolean; title: string; detail: string }>;
}

/**
 * The collective's GitHub org and repositories, for the collective page's
 * stats and its settings page: one read of what is linked (members may read
 * it), one of what the App offers (the owner's alone), and the picker's save.
 */
export function useCollectiveGithub(
  id: string,
  { isOwner, canReadLinks, linkedOrg }: { isOwner: boolean; canReadLinks: boolean; linkedOrg: string | null },
): CollectiveGithub {
  const qc = useQueryClient();
  const repositories = useRepositories(id, canReadLinks);
  const available = useAvailableRepositories(id, isOwner && !!linkedOrg);
  const linkRepository = useLinkRepository();
  const unlinkRepository = useUnlinkRepository();

  // A 501 on the linked list is the server saying GitHub is not set up here:
  // nothing can be linked, so nothing offers to.
  const configured = !(repositories.isError && isNotConfigured(repositories.error));
  const linked = repositories.data?.repositories ?? [];
  const offered = available.data?.repositories ?? [];
  const owners = repoPickerOwners(offered, linked, linkedOrg);

  // No org yet, or an org the App is not installed on (it offers nothing and
  // nothing is linked): the owner's way forward is the install handshake.
  const needsInstall =
    isOwner &&
    configured &&
    repositories.isSuccess &&
    (!linkedOrg || (available.isSuccess && offered.length === 0 && linked.length === 0));

  const saveLinks = async (diff: { add: string[]; remove: string[] }) => {
    const outcome = await applyRepoLinks(repoLinkSteps(diff), (step) => {
      const { owner, name } = splitRepo(step.repo);
      return step.action === "link"
        ? linkRepository.mutateAsync({ groupId: id, owner, name })
        : unlinkRepository.mutateAsync({ groupId: id, owner, name });
    });
    await qc.invalidateQueries({ queryKey: ["group-repositories", id] });
    await qc.invalidateQueries({ queryKey: ["group-repositories-available", id] });
    return repoLinkMessage(outcome);
  };

  return {
    configured,
    org: orgSummary(linkedOrg, repositories.data ? linked : undefined, available.data ? owners : undefined) ?? null,
    owners,
    linkedIds: linkedRepoIds(linked, owners),
    canManage: isOwner && !!linkedOrg && configured && repositories.isSuccess,
    needsInstall,
    saveLinks,
  };
}
