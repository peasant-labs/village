"use client";

import { use, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import Link from "next/link";
import { ChevronRight, Users, Lock, Trash2 } from "lucide-react";
import {
  useGroup,
  useUpdateGroup,
  useDeleteGroup,
  usePromoteMember,
  useRemoveGroupMember,
} from "@/lib/queries/groups";
import { useMyOrgs } from "@/lib/queries/orgs";
import { useAuth } from "@/providers/AuthProvider";
import {
  Button,
  Input,
  Textarea,
  Select,
  Switch,
  RadioGroup,
  RoleRoster,
  DangerZone,
  ConfirmInline,
  Tag,
} from "@/lib/ft-ui";

export default function GroupSettingsPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const router = useRouter();
  const { user } = useAuth();
  const { data, isLoading } = useGroup(id);
  const updateGroup = useUpdateGroup();
  const deleteGroup = useDeleteGroup();
  const promoteMember = usePromoteMember();
  const removeMember = useRemoveGroupMember();
  const { data: myOrgs } = useMyOrgs();
  const visibleOrgs = (myOrgs ?? []).filter((o) => o.visible);
  type ChangeEvent = React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>;

  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [acceptanceMode, setAcceptanceMode] = useState("open");
  const [dataAccess, setDataAccess] = useState("members_only");
  const [linkedGithubOrg, setLinkedGithubOrg] = useState("");
  const [displayMembers, setDisplayMembers] = useState(true);
  const [deletionPolicy, setDeletionPolicy] = useState<"user_choice" | "mandatory">(
    "user_choice"
  );
  const [initialized, setInitialized] = useState(false);
  const [saved, setSaved] = useState(false);
  const [orgFilter, setOrgFilter] = useState("");

  // Derived above the early returns so the hook order stays stable across
  // renders (rules-of-hooks); `data` is undefined until the group loads.
  const members = data?.members ?? [];
  const filteredMembers = useMemo(() => {
    if (!orgFilter) return members;
    return members.filter((m) => (m.github_orgs ?? []).includes(orgFilter));
  }, [members, orgFilter]);

  if (isLoading) {
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">
        <div className="h-4 w-56 bg-surface-hover animate-shimmer" />
        <div className="h-16 w-72 bg-surface-hover animate-shimmer" />
        <div className="h-64 w-full bg-surface-hover animate-shimmer" />
      </div>
    );
  }

  if (!data) {
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">
        <div className="border border-rule bg-surface px-5 py-12 flex flex-col items-center gap-3 text-center">
          <Users size={28} className="text-ink-4" />
          <p className="text-sm font-medium text-ink">Collective not found</p>
          <Link
            href="/groups"
            className="text-[13px] text-ink-3 hover:text-ink transition-colors focus-mono cursor-pointer"
          >
            Back to collectives
          </Link>
        </div>
      </div>
    );
  }

  const { group, your_role: yourRole } = data;

  // Pre-fill the form once the group has loaded.
  if (!initialized) {
    setName(group.name);
    setDescription(group.description ?? "");
    setAcceptanceMode(group.acceptance_mode);
    setDataAccess(group.data_access);
    setLinkedGithubOrg(group.linked_github_org ?? "");
    setDisplayMembers(group.display_members ?? true);
    setDeletionPolicy(group.transcript_deletion_policy ?? "user_choice");
    setInitialized(true);
  }

  if (yourRole !== "owner") {
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">
        <div className="border border-rule bg-surface px-5 py-12 flex flex-col items-center gap-3 text-center">
          <Lock size={28} className="text-ink-4" />
          <p className="text-sm font-medium text-ink">Access restricted</p>
          <p className="text-[13px] text-ink-3 max-w-sm">
            Only the owner can edit collective settings.
          </p>
          <Link
            href={`/groups/${id}`}
            className="text-[13px] text-ink-3 hover:text-ink transition-colors focus-mono cursor-pointer"
          >
            Back to collective
          </Link>
        </div>
      </div>
    );
  }

  const handleSave = (e: React.FormEvent) => {
    e.preventDefault();
    setSaved(false);
    updateGroup.mutate(
      {
        id,
        name,
        description,
        acceptance_mode: acceptanceMode,
        data_access: dataAccess,
        linked_github_org: linkedGithubOrg || null,
        display_members: displayMembers,
        transcript_deletion_policy: deletionPolicy,
      },
      {
        onSuccess: () => {
          setSaved(true);
          setTimeout(() => setSaved(false), 2500);
        },
      }
    );
  };

  // Map API members to the RosterMember shape that RoleRoster expects.
  // Owner rows are locked in RoleRoster; self-rows are already locked as owner
  // rows (settings page is owner-only, so the owner's own row === owner row).
  const rosterMembers = filteredMembers.map((m) => ({
    id: m.id,
    // "@" prefix, display-only (round 9 addendum): the demo's own roster mock data stores the
    // "@" AS PART OF the handle string itself (CommonsManage.jsx: `handle: '@alice-dev'`) --
    // RoleRoster has no separate "@-glyph avatar" mode, its fallback avatar (no avatar_url) just
    // takes the handle's own first character, so the demo's "@" fallback avatar is simply a
    // side effect of its handle already including "@". Matching that here fixes BOTH the missing
    // "@" in the visible handle text AND the fallback avatar (previously showing "A"/"B") in one
    // change. m.github_username itself (the stored login) is untouched -- this only prefixes the
    // DISPLAY value passed to RoleRoster.
    handle: `@${m.github_username}`,
    // Display name back per explicit user feedback (round 2): RoleRoster's own .rr-who
    // renders it as a second line beneath the handle automatically when set.
    name: m.display_name ?? undefined,
    role: m.role as "owner" | "member" | "contributor" | "guest",
    owner: m.role === "owner",
    avatar: m.avatar_url ?? undefined,
  }));

  return (
    <div className="cmg-settings max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">
      {/* Breadcrumb — the shared .crumb class (fairtrade src/index.css), matching the demo's own
          CollectiveSettingsView breadcrumb (`<div className="crumb cmg-crumb">`) and this app's
          own detail page (which gets it for free via <Manage>'s internal breadcrumb): mono chrome,
          lowercase. Was hand-rolled Tailwind (text-xs, no font-mono, Title-Case JSX text) before
          this fix -- traced to .crumb rather than reinventing its font/case/color rules (round 5
          UAT: "wrong font + wrong case").
          .crumb applies text-transform:lowercase to its ENTIRE contents, including .cur -- fine
          for the literal chrome segments ("collectives"/"settings"), but group.name is real
          content (e.g. "AI Research Collective") that must keep its actual case, so `normal-case`
          explicitly overrides the inherited lowercase on just that link. */}
      <nav aria-label="Breadcrumb" className="crumb">
        <Link href="/groups" className="focus-mono">
          collectives
        </Link>
        <ChevronRight aria-hidden="true" />
        <Link
          href={`/groups/${id}`}
          className="focus-mono truncate max-w-[280px] normal-case"
          title={group.name}
        >
          {group.name}
        </Link>
        <ChevronRight aria-hidden="true" />
        <span className="cur">settings</span>
      </nav>

      {/* Header — cmg-d-hero/cmg-title/cmg-deck + a "settings" Tag, matching the fairtrade demo's
          CollectiveSettingsView header (UAT finding: "the 'collective settings' header styling
          does not match the demo's" — the app previously used ad hoc text-2xl/h1 markup here). */}
      <header className="cmg-d-hero">
        <div className="cmg-d-hero-row">
          <div>
            <h2 className="cmg-title">collective settings</h2>
            <p className="cmg-deck">
              edit how this collective accepts contributions and shares data.
            </p>
          </div>
          <Tag>settings</Tag>
        </div>
      </header>

      {/* Two-column layout (main form + danger zone / sidebar roster), matching the demo's
          cmg-d-grid so the members list sits in its own rail card rather than stacking below
          the fold under the full settings form. */}
      <div className="cmg-d-grid">
        <main className="cmg-d-main">
          {/* Settings form — panel framing: mono section header + body with fairtrade fields.
              border-rule-strong (not border-rule) to match the demo's own .card box border
              exactly (fairtrade src/index.css:440 `.card { border: var(--bd-strong); }` --
              --rule-strong, not the fainter --rule the roster/aside boxes use). Measured
              rgb(111,106,95) on the demo's own settings form; village's box was rgb(60,56,47)
              (--rule) before this fix -- visibly fainter than the demo, and inconsistent with
              the demo's own two-tier convention (a --bd-strong outer box, --bd inner rows).
              cmg-settings-form (fairtrade src/index.css): a title/hint two-tier hierarchy scoped
              to this class specifically (titles distinctive --ink-2, hints/descriptions faint
              --ink-3) -- opting into the SAME scoped rule the demo's own CollectiveSettingsView
              form carries, rather than duplicating the color rules here. */}
          <form
            onSubmit={handleSave}
            className="cmg-settings-form border border-rule-strong bg-surface"
          >
            {/* Round 8 UAT: two fixes measured against the demo's own .cmg-sub heading (not
                guessed) -- (a) font-size was text-sm (14px), demo's computed heading is 18px
                (fairtrade's --fs-md, .cmg-sub's own size) -- matched exactly. (b) this row had
                border-b border-rule separating the heading from the fields below; the demo's own
                .cmg-queue-head has NO border (computed border-bottom-width: 0px) -- removed. */}
            <div className="flex items-center justify-between px-5 py-3">
              <span className="text-[18px] font-bold text-ink font-mono lowercase tracking-wide">
                general
              </span>
            </div>
            <div className="px-5 py-4 flex flex-col gap-4">
              <Input
                label="Name"
                id="group-name"
                value={name}
                onChange={(e: ChangeEvent) => setName(e.target.value)}
                required
                placeholder="AI Research Collective"
              />
              <Textarea
                label="Description"
                id="group-description"
                value={description}
                onChange={(e: ChangeEvent) => setDescription(e.target.value)}
                rows={2}
                placeholder="What does this collective do?"
              />
              <Select
                label="Acceptance mode"
                id="group-acceptance"
                value={acceptanceMode}
                onChange={(e: ChangeEvent) => setAcceptanceMode(e.target.value)}
                options={[
                  { value: "open", label: "open - anyone can share, auto-approved" },
                  {
                    value: "verified_only",
                    label: "verified only - requires org affiliation",
                  },
                  {
                    value: "curated",
                    label: "curated - owner must approve each share",
                  },
                ]}
              />
              <Select
                label="Data access"
                id="group-access"
                value={dataAccess}
                onChange={(e: ChangeEvent) => setDataAccess(e.target.value)}
                options={[
                  {
                    value: "members_only",
                    label: "members only - full members can browse data",
                  },
                  {
                    value: "contributors",
                    label: "contributors - anyone who contributes can browse",
                  },
                  {
                    value: "public",
                    label: "public - anyone can browse the dataset",
                  },
                ]}
              />
              <Select
                label="Link to GitHub org"
                id="group-linked-org"
                hint="optional. only orgs you've set as visible appear here."
                value={linkedGithubOrg}
                onChange={(e: ChangeEvent) => setLinkedGithubOrg(e.target.value)}
              >
                <option value="">Not linked</option>
                {visibleOrgs.map((o) => (
                  <option key={o.org_login} value={o.org_login}>
                    @{o.org_login}
                  </option>
                ))}
              </Select>

              {/* Display members — a real toggle (Switch), not a checkbox: "'show the members on
                  the collective page' should be a toggle, not a checkbox" (UAT finding).
                  .sw-stack (fairtrade src/index.css): label on its own top line, the hint
                  directly beneath it, then the switch + its on/off marker sharing a third line.
                  The label is rendered HERE, outside <Switch> (not via its `label` prop), paired
                  to the switch via an explicit id + htmlFor -- letting .sw-field's own internal
                  <label> render instead put the label inside the SAME grid columns as the
                  switch/state, which (being much wider) inflated those columns and pushed the
                  state marker far from the switch. .sw-toggle-row wraps just <Switch> in a real
                  flex row so the switch + its on/off marker size and gap independently of the
                  label's width. Fixed in the demo's identical CommonsManage.jsx too (shared
                  component, both match). */}
              <div className="sw-stack">
                <label htmlFor="group-display-members" className="sw-label">
                  Show the members card on the collective page
                </label>
                <span className="sw-hint">
                  When off, only owners can see the member list.
                </span>
                <div className="sw-toggle-row">
                  <Switch
                    id="group-display-members"
                    checked={displayMembers}
                    onChange={(checked: boolean) => setDisplayMembers(checked)}
                  />
                </div>
              </div>

              {/* Transcript retention — RadioGroup with per-option description labels.
                  DS-cohesion pass (a grounded reviewer spec): this form was
                  mixing an ad-hoc 11px size (below the DS's --fs-label floor of 14px) with the
                  DS-canonical field-label tier (.label / .muted, both --fs-label=14px, mono,
                  --ink-3) elsewhere on the SAME form -- "Transcript retention on leave" (.label,
                  already correct, left as the reference tier) sat right above description text at
                  the wrong size/weight. Retention description + both radio-option descriptions
                  now reuse .muted directly (the DS's own "small print" token) + lowercase (.muted
                  itself doesn't force case, since it's also used for already-lowercase mono
                  strings elsewhere) rather than reinventing the same three properties as raw
                  Tailwind utilities. Radio option TITLES ("User's choice"/"Mandatory") move to
                  the .is-radio tier (14px, --ink-2, mono) their own wrapping <label
                  className="is-radio"> (Checkbox.jsx's <Radio>) already provides at the
                  container level -- previously overridden by an inner text-sm/text-ink span that
                  fought the wrapper's own font/color instead of inheriting it -- and lowercased
                  to match every other setting title's case convention on this form.
                  mt-2: this section's own wrapper is a sibling in the SAME gap-4 (16px) stack as
                  every other field, but round 8 UAT specifically flagged the gap directly above
                  it as too tight. Measured the demo's own established field-to-field rhythm
                  (its cmg-settings-stack: Name/Description/Acceptance/Data access/the toggle are
                  each exactly 24px apart, vs this form's uniform 16px gap-4) and matched THIS one
                  transition to it: +8px on top of the existing 16px gap-4 = 24px total. Scoped to
                  the one transition explicitly called out, not a blanket gap-4 -> gap-6 change
                  across the whole form (which would touch spacing nobody flagged). */}
              <div className="flex flex-col gap-2 mt-2">
                <span className="label">Transcript retention on leave</span>
                <p className="muted lowercase">
                  What happens to a member&apos;s shared transcripts when they leave
                  the collective.
                </p>
                <RadioGroup
                  name="deletion-policy"
                  ariaLabel="Transcript retention on leave"
                  value={deletionPolicy}
                  onChange={(value) =>
                    setDeletionPolicy(value as "user_choice" | "mandatory")
                  }
                  options={[
                    {
                      value: "user_choice",
                      label: (
                        <span className="flex flex-col gap-0.5">
                          <span className="font-mono text-[14px] text-ink-2 lowercase">
                            User&apos;s choice
                          </span>
                          <span className="muted lowercase">
                            Each leaving member decides whether to retract their
                            contributions.
                          </span>
                        </span>
                      ),
                    },
                    {
                      value: "mandatory",
                      label: (
                        <span className="flex flex-col gap-0.5">
                          <span className="font-mono text-[14px] text-ink-2 lowercase">Mandatory</span>
                          <span className="muted lowercase">
                            All of a leaving member&apos;s contributions are
                            auto-retracted from the collective.
                          </span>
                        </span>
                      ),
                    },
                  ]}
                />
              </div>

              <div className="flex items-center gap-3">
                <Button
                  type="submit"
                  variant="primary"
                  loading={updateGroup.isPending}
                  disabled={updateGroup.isPending}
                >
                  {updateGroup.isPending ? "Saving…" : "Save"}
                </Button>
                {saved && (
                  <span className="text-[13px] font-mono text-success">Saved</span>
                )}
                {updateGroup.isError && (
                  <span className="text-[13px] text-danger">
                    {updateGroup.error?.message ?? "Unable to save group settings."}
                  </span>
                )}
              </div>
            </div>
          </form>

          {/* Danger zone — DangerZone + ConfirmInline replaces the browser confirm() modal
              with the fairtrade inline confirm pattern, which is the correct
              layout-pattern for destructive actions. The delete mutation is unchanged.
              Body copy lowercased (round 9 addendum, user decision): checked the demo's own
              danger-zone body directly (CommonsManage.jsx) -- it's already all-lowercase
              ("deleting this collective removes the governance settings and member roster.")
              -- village's sentence-case copy was the outlier, not shared copy/CSS (DangerZone
              only supplies the wrapping structure; the body text is each consumer's own
              children). Wording unchanged, case only. Mono treatment (round 5,
              .rr-danger-body's own font-family) is untouched. */}
          <DangerZone title="danger zone">
            <p>
              deleting this collective permanently removes it and all of its
              membership. shared transcripts remain owned by their authors. this
              cannot be undone.
            </p>
            <ConfirmInline
              label="delete collective"
              confirmLabel="delete"
              icon={<Trash2 size={14} />}
              busy={deleteGroup.isPending}
              onConfirm={() => deleteGroup.mutateAsync(id).then(() => router.push("/groups"))}
            />
            {deleteGroup.isError && (
              <span className="text-[13px] text-danger">
                {deleteGroup.error?.message ?? "Unable to delete group."}
              </span>
            )}
          </DangerZone>
        </main>

        {/* Members roster — sidebar rail, matching the demo's cmg-d-rail. UNCONDITIONAL now (the
            demo always shows it): it used to be gated behind `group.linked_github_org`, so a
            collective with no linked GitHub org rendered no members list at all (UAT finding:
            "app is MISSING the members list"). The org filter is real functionality beyond the
            demo (kept), only shown when the viewer has visible orgs to filter by.
            border-none: .sidebar (fairtrade src/index.css) draws its OWN 1px border around this
            whole <aside> -- fine for the demo's own settings aside, which holds just ONE box
            (the roster), so that outer border reads as that single box's own edge. Village's
            aside stacks TWO separate boxes (permission-levels + the roster) with only an 8px gap
            between them -- the outer .sidebar border's left/right edges run continuously THROUGH
            that gap, reading as connecting lines linking two boxes the user wants visually
            distinct. Each box already has its own border, so the outer one is redundant here;
            border-none (a Tailwind utility, @layer utilities) outranks .sidebar's own
            @layer components rule to drop it, scoped to just this <aside> -- the demo's own
            single-box aside is untouched (fairtrade's .sidebar CSS itself isn't modified). */}
        <aside className="sidebar cmg-d-rail border-none" aria-label="settings sidebar">
          <div className="flex flex-col gap-2">
            {/* Permission-level notice — DangerZone-LIKE box framing (bordered box + an
                icon+label header strip over the body, mirroring .rr-danger/.rr-danger-head's
                structure) so it reads as a distinct, important notice the way DangerZone does,
                without using DangerZone's own clay/red palette: this note isn't a destructive
                warning, so amber (the DS's scarce "pay attention" emphasis color) is the
                semantically correct tone here, not danger-red. Slimmed to just the role explainer
                (round 2 UAT). Spacing to the roster below reverted to the round-2 baseline (round
                3's now-removed separate filter section had introduced an extra section + its own
                gaps between this notice and the roster; round 4 UAT: "the previous spacing...is
                better") — this box is a direct flex-col sibling of the roster again, one gap-2
                between them, same as before round 3. Copy verified against the actual backend
                behaviour (backend/internal/handler/groups.go PromoteMember): it PATCHes exactly
                the one target member's role — there is no "default role for new members" concept
                in the handler at all, and no bulk reassignment. */}
            <div className="border border-amber/40 bg-amber/5">
              <div className="flex items-center gap-2 px-4 py-2.5 border-b border-amber/40">
                <Lock size={14} className="text-amber" aria-hidden />
                <span className="text-[11px] font-mono lowercase tracking-wide text-ink-2">
                  permission levels
                </span>
              </div>
              <div className="px-4 py-3">
                <p className="text-[11px] font-mono text-ink-3">
                  change a member&apos;s role with the dropdown beside their
                  name. it affects only that member; owners can&apos;t be
                  reassigned here.
                </p>
              </div>
            </div>

            {/* overflow/maxWidth wrapper matching sections-react/70-governance.jsx's RoleRoster
                specimen (same containment fix as ui/commons/Manage.jsx's members rail, 932e): the
                role-select + remove-button action column doesn't wrap, so without this the roster
                pushed the WHOLE PAGE ~170px wider than the viewport instead of scrolling
                internally within its own sidebar box.
                RoleRoster is now ALWAYS rendered (round 4 UAT: the org filter moves INSIDE this
                box, as its own row directly under the header, before the member rows) -- the
                filter must stay reachable even when it filters the list down to zero results, so
                swapping the whole box out for a "No members in @X" message (the prior behaviour)
                would have made the filter itself unreachable once it filtered to empty, with no
                way back to "all members" except editing the URL. members=[] when the filtered
                result is empty renders RoleRoster's own header count as "0" -- a plainer signal
                than the removed custom copy, but one that keeps the filter itself always usable,
                which matters more functionally. */}
            <div style={{ overflow: "auto", maxWidth: "100%" }}>
              <RoleRoster
                title={
                  orgFilter
                    ? `${filteredMembers.length} / ${members.length} members`
                    : "members"
                }
                members={rosterMembers}
                roles={["contributor", "member"]}
                filterSlot={
                  visibleOrgs.length > 0 ? (
                    <>
                      <label htmlFor="member-org-filter" className="shrink-0">
                        show
                      </label>
                      <select
                        id="member-org-filter"
                        value={orgFilter}
                        onChange={(e: ChangeEvent) => setOrgFilter(e.target.value)}
                        className="bg-canvas border border-rule-strong text-ink text-[12px] font-mono px-2 py-1 focus-mono cursor-pointer normal-case"
                      >
                        <option value="">all members</option>
                        {visibleOrgs.map((o) => (
                          <option key={o.org_login} value={o.org_login}>
                            @{o.org_login}
                          </option>
                        ))}
                      </select>
                    </>
                  ) : undefined
                }
                onRole={(m, role) =>
                  promoteMember.mutate({
                    groupId: id,
                    userId: m.id!,
                    role,
                  })
                }
                onRemove={async (m) => {
                  await removeMember.mutateAsync({
                    groupId: id,
                    userId: m.id!,
                  });
                }}
              />
            </div>
          </div>
        </aside>
      </div>
    </div>
  );
}
