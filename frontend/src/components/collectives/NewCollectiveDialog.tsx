"use client";

import { useEffect, useId, useState, type FormEvent, type KeyboardEvent } from "react";
import Link from "next/link";
import { Building2, Check, CircleAlert, DoorOpen, Eye, ShieldCheck, UserPlus, Users, X } from "lucide-react";
import type { VillageCollective } from "@peasant-labs/schema";
import { Dialog } from "@/lib/ft-ui";
import { ApiError } from "@/lib/api";
import { useCreateGroup } from "@/lib/queries/groups";
import { useInviteToCollective } from "@/lib/queries/invitations";
import { inviteRefusal } from "@/lib/invitationCopy";
import { createGroupRequest } from "@/lib/queries/groupRequests";
import { useMyOrgs } from "@/lib/queries/orgs";
import { useAuth } from "@/providers/AuthProvider";
import { readyLine, type AcceptanceMode, type NewCollectiveAccess } from "@/lib/collectivePolicyCopy";
import { ChoiceCards, type Choice } from "./ChoiceCards";

const STEPS = ["name and purpose", "access", "invite"] as const;

type Step = 1 | 2 | 3 | 4;

/** What happened to one invitation once the collective existed. */
export type InviteOutcome = { username: string; invited: true } | { username: string; invited: false; reason: string };

const PUBLISH_CHOICES: Choice<AcceptanceMode>[] = [
  {
    value: "open",
    label: "anyone who joins",
    help: "anyone can join and publish straight away.",
    icon: <DoorOpen />,
  },
  {
    value: "verified_only",
    label: "your github org",
    help: "only members of your github organization join and publish.",
    icon: <Building2 />,
  },
  {
    value: "curated",
    label: "sessions you approve",
    help: "people you invite publish; each session waits for you.",
    icon: <ShieldCheck />,
  },
];

const READ_CHOICES: Choice<NewCollectiveAccess>[] = [
  { value: "members_only", label: "members", help: "only members read what is published.", icon: <Eye /> },
  {
    value: "contributors",
    label: "members and contributors",
    help: "everyone who publishes can also read the rest.",
    icon: <Users />,
  },
];

function messageOf(error: unknown): string {
  if (error instanceof Error && error.message) return error.message;
  return "something went wrong. try again.";
}

/** A refusal about the linked organization belongs on the access step. */
function isOrgRefusal(error: unknown): boolean {
  return error instanceof ApiError && /github organi[sz]ation|org membership/i.test(error.message);
}

/** A refusal about the name belongs on the first step, under the name field. */
function isNameRefusal(error: unknown): boolean {
  return error instanceof ApiError && (error.status === 400 || error.status === 409) && /\bname\b/i.test(error.message);
}

function inviteSummary(outcomes: readonly InviteOutcome[]): string {
  const invited = outcomes.filter((o) => o.invited).length;
  if (invited === 0) return "";
  return invited === 1 ? "1 invitation sent." : `${invited} invitations sent.`;
}

/**
 * The new-collective dialog: name and purpose, who publishes and who reads,
 * then the people to invite. The collective is created on the third step; each
 * person is then invited with the same route the settings page uses, one at a
 * time, and the last step reports every outcome. Nobody becomes a member here:
 * an invited person joins only by accepting. A refused invitation is listed
 * with the reason and is never counted as sent.
 *
 * Mount it only while open, so every opening starts from an empty form.
 */
export default function NewCollectiveDialog({ onClose }: { onClose: () => void }) {
  const ids = useId();
  const formId = `${ids}-form`;
  const nameId = `${ids}-name`;
  const nameErrorId = `${ids}-name-error`;
  const purposeId = `${ids}-purpose`;
  const orgId = `${ids}-org`;
  const orgErrorId = `${ids}-org-error`;
  const inviteId = `${ids}-invite`;
  const inviteErrorId = `${ids}-invite-error`;

  const { user } = useAuth();
  const createGroup = useCreateGroup();
  const invite = useInviteToCollective();
  const myOrgs = useMyOrgs();
  const visibleOrgs = (myOrgs.data ?? []).filter((o) => o.visible).map((o) => o.org_login);

  const [step, setStep] = useState<Step>(1);
  const [name, setName] = useState("");
  const [purpose, setPurpose] = useState("");
  const [mode, setMode] = useState<AcceptanceMode>("open");
  const [access, setAccess] = useState<NewCollectiveAccess>("members_only");
  const [org, setOrg] = useState("");
  const [invitees, setInvitees] = useState<string[]>([]);
  const [draft, setDraft] = useState("");
  const [nameError, setNameError] = useState<string | null>(null);
  const [orgError, setOrgError] = useState<string | null>(null);
  const [inviteError, setInviteError] = useState<string | null>(null);
  const [createError, setCreateError] = useState<string | null>(null);
  const [busy, setBusy] = useState<"creating" | "inviting" | null>(null);
  const [created, setCreated] = useState<VillageCollective | null>(null);
  const [outcomes, setOutcomes] = useState<InviteOutcome[]>([]);

  // The org picker offers the person's visible orgs; the first is chosen
  // until they pick another.
  const chosenOrg = org && visibleOrgs.includes(org) ? org : (visibleOrgs[0] ?? "");

  // The shared dialog mounts its body through a portal. Wait for that actual
  // body before scheduling step focus, after the dialog's own initial focus.
  const [body, setBody] = useState<HTMLDivElement | null>(null);
  useEffect(() => {
    if (!body) return;
    const frame = requestAnimationFrame(() => {
      body.querySelector<HTMLElement>("[data-autofocus]")?.focus();
    });
    return () => cancelAnimationFrame(frame);
  }, [body, step]);

  const nextFromName = () => {
    if (!name.trim()) {
      setNameError("a collective needs a name.");
      document.getElementById(nameId)?.focus();
      return;
    }
    setNameError(null);
    setStep(2);
  };

  const nextFromAccess = () => {
    if (mode === "verified_only" && !chosenOrg) {
      setOrgError("pick a github org, or choose who can publish another way.");
      return;
    }
    setOrgError(null);
    setStep(3);
  };

  const addInvitee = () => {
    const username = draft.trim().replace(/^@/, "");
    if (!username) return;
    if (/\s/.test(username)) {
      setInviteError("a username has no spaces.");
      return;
    }
    const lower = username.toLowerCase();
    if (user && lower === user.github_username.toLowerCase()) {
      setInviteError("that is you. you own the collective already.");
      return;
    }
    if (invitees.some((u) => u.toLowerCase() === lower)) {
      setInviteError(`@${username} is on the list already.`);
      return;
    }
    setInvitees((list) => [...list, username]);
    setDraft("");
    setInviteError(null);
  };

  const onInviteKey = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === "Enter") {
      event.preventDefault();
      addInvitee();
    }
  };

  const create = async () => {
    setCreateError(null);
    setBusy("creating");
    let collective: VillageCollective;
    try {
      collective = await createGroup.mutateAsync(
        createGroupRequest({
          name: name.trim(),
          purpose: purpose.trim(),
          mode,
          access,
          org: mode === "verified_only" ? chosenOrg : "",
        }),
      );
    } catch (error) {
      setBusy(null);
      if (isOrgRefusal(error)) {
        setOrgError(messageOf(error));
        setStep(2);
      } else if (isNameRefusal(error)) {
        setNameError(messageOf(error));
        setStep(1);
      } else {
        setCreateError(messageOf(error));
      }
      return;
    }
    setCreated(collective);
    setBusy("inviting");
    const results: InviteOutcome[] = [];
    for (const username of invitees) {
      try {
        await invite.mutateAsync({ groupId: collective.id, username });
        results.push({ username, invited: true });
      } catch (error) {
        results.push({ username, invited: false, reason: inviteRefusal(username, error) });
      }
    }
    setOutcomes(results);
    setBusy(null);
    setStep(4);
  };

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    if (busy) return;
    if (step === 1) nextFromName();
    else if (step === 2) nextFromAccess();
    else if (step === 3) void create();
  };

  const footer =
    step === 4 ? undefined : (
      <>
        {step === 1 ? (
          <button type="button" className="btn btn-secondary" onClick={onClose}>
            cancel
          </button>
        ) : (
          <button
            type="button"
            className="btn btn-secondary"
            disabled={busy !== null}
            onClick={() => setStep((s) => (s > 1 ? ((s - 1) as Step) : s))}
          >
            back
          </button>
        )}
        <button type="submit" form={formId} className="btn btn-primary" disabled={busy !== null} aria-busy={busy !== null || undefined}>
          {step === 3 ? (busy === "creating" ? "creating" : busy === "inviting" ? "inviting people" : "create collective") : "next"}
        </button>
      </>
    );

  const failed = outcomes.filter((o): o is Extract<InviteOutcome, { invited: false }> => !o.invited);
  const createdName = created?.name ?? name.trim();

  return (
    <Dialog
      open
      onClose={onClose}
      title="new collective"
      labelId={`${ids}-title`}
      size="wide"
      className="on-newcoll"
      dismissible={busy === null}
      footer={footer}
    >
      <div ref={setBody} style={{ display: "contents" }}>
        <ol className="on-stepper" aria-label="steps">
          {STEPS.map((label, index) => {
            const n = index + 1;
            return (
              <li key={label} aria-current={n === step ? "step" : undefined} data-done={n < step ? "true" : undefined}>
                <i aria-hidden="true">{n}</i>
                {label}
                {n < step ? <span className="sr-only">, done</span> : null}
              </li>
            );
          })}
        </ol>

        {step === 1 ? (
          <form id={formId} onSubmit={onSubmit} noValidate className="on-section" style={{ gap: "var(--sp-5)" }}>
            <div className="on-field">
              <label htmlFor={nameId}>name</label>
              <input
                id={nameId}
                data-autofocus
                className="on-input"
                value={name}
                autoComplete="off"
                aria-invalid={nameError ? true : undefined}
                aria-describedby={nameError ? nameErrorId : undefined}
                onChange={(event) => {
                  setName(event.target.value);
                  if (nameError) setNameError(null);
                }}
              />
              {nameError ? (
                <span id={nameErrorId} className="on-error">
                  <CircleAlert size={16} aria-hidden="true" />
                  {nameError}
                </span>
              ) : null}
            </div>
            <div className="on-field">
              <label htmlFor={purposeId}>purpose</label>
              <textarea
                id={purposeId}
                className="on-input"
                rows={3}
                value={purpose}
                aria-describedby={`${purposeId}-help`}
                onChange={(event) => setPurpose(event.target.value)}
              />
              <span id={`${purposeId}-help`} className="on-help">
                Optional. Shown on the collective&apos;s page and found by search.
              </span>
            </div>
          </form>
        ) : null}

        {step === 2 ? (
          <form id={formId} onSubmit={onSubmit} noValidate className="on-section" style={{ gap: "var(--sp-6)" }}>
            <ChoiceCards
              label="who can publish"
              labelId={`${ids}-publish`}
              value={mode}
              focusSelected
              onChange={(next) => {
                setMode(next);
                setOrgError(null);
              }}
              choices={PUBLISH_CHOICES}
            />
            {mode === "verified_only" ? (
              <div className="on-field">
                {myOrgs.isLoading ? (
                  <span className="on-help">reading your github orgs</span>
                ) : visibleOrgs.length === 0 ? (
                  <div className="on-notice">
                    <p>
                      no github org is linked to your profile, so there is no org to choose. link one in{" "}
                      <Link className="on-link" href="/settings">
                        settings
                      </Link>
                      , or pick who can publish another way.
                    </p>
                  </div>
                ) : (
                  <>
                    <label htmlFor={orgId}>which github org</label>
                    <select
                      id={orgId}
                      className="on-input on-select"
                      value={chosenOrg}
                      aria-invalid={orgError ? true : undefined}
                      aria-describedby={orgError ? orgErrorId : undefined}
                      onChange={(event) => {
                        setOrg(event.target.value);
                        setOrgError(null);
                      }}
                    >
                      {visibleOrgs.map((login) => (
                        <option key={login} value={login}>
                          {login}
                        </option>
                      ))}
                    </select>
                  </>
                )}
                {orgError ? (
                  <span id={orgErrorId} className="on-error" role="alert">
                    <CircleAlert size={16} aria-hidden="true" />
                    {orgError}
                  </span>
                ) : null}
              </div>
            ) : null}
            <ChoiceCards
              label="who can read"
              labelId={`${ids}-read`}
              value={access}
              onChange={setAccess}
              choices={READ_CHOICES}
            />
          </form>
        ) : null}

        {step === 3 ? (
          <form id={formId} onSubmit={onSubmit} noValidate className="on-section" style={{ gap: "var(--sp-5)" }}>
            <div className="on-field">
              <label htmlFor={inviteId}>invite by username</label>
              <div className="on-row on-invite-row">
                <input
                  id={inviteId}
                  data-autofocus
                  className="on-input"
                  style={{ flex: "1 1 14rem", width: "auto" }}
                  placeholder="username"
                  autoComplete="off"
                  autoCapitalize="none"
                  spellCheck={false}
                  value={draft}
                  disabled={busy !== null}
                  aria-invalid={inviteError ? true : undefined}
                  aria-describedby={inviteError ? inviteErrorId : undefined}
                  onChange={(event) => {
                    setDraft(event.target.value);
                    if (inviteError) setInviteError(null);
                  }}
                  onKeyDown={onInviteKey}
                />
                <button type="button" className="btn btn-secondary" disabled={busy !== null} onClick={addInvitee}>
                  <UserPlus size={16} aria-hidden="true" />
                  add
                </button>
              </div>
              {inviteError ? (
                <span id={inviteErrorId} className="on-error">
                  <CircleAlert size={16} aria-hidden="true" />
                  {inviteError}
                </span>
              ) : null}
            </div>
            {invitees.length > 0 ? (
              <ul className="on-invitees" aria-label="people to invite">
                {invitees.map((username) => (
                  <li key={username} className="on-chip-person">
                    @{username}
                    <button
                      type="button"
                      aria-label={`remove ${username}`}
                      disabled={busy !== null}
                      onClick={() => setInvitees((list) => list.filter((u) => u !== username))}
                    >
                      <X aria-hidden="true" />
                    </button>
                  </li>
                ))}
              </ul>
            ) : null}
            <p className="on-muted" style={{ margin: 0 }}>
              each person gets an invitation once the collective exists and joins only if they accept. if a name has a
              village account they&apos;ll see it; we don&apos;t say whether it does. you can invite more people later
              from settings.
            </p>
            {createError ? (
              <div className="on-notice" data-tone="danger" role="alert">
                <p>could not create the collective: {createError}</p>
              </div>
            ) : null}
          </form>
        ) : null}

        {step === 4 && created ? (
          <div className="on-done">
            <span className="on-done-mark" aria-hidden="true">
              <Check strokeWidth={2.5} />
            </span>
            <h3>{createdName} is ready</h3>
            <p role="status">
              {[inviteSummary(outcomes), readyLine(mode, mode === "verified_only" ? created.linked_github_org : null)]
                .filter(Boolean)
                .join(" ")}
            </p>
            {failed.length > 0 ? (
              <div className="on-notice" data-tone="danger" role="alert">
                <div>
                  <p>
                    {failed.length === 1 ? "1 invitation was not sent." : `${failed.length} invitations were not sent.`}
                  </p>
                  <ul aria-label="invitations not sent">
                    {failed.map((outcome) => (
                      <li key={outcome.username}>{outcome.reason}</li>
                    ))}
                  </ul>
                </div>
              </div>
            ) : null}
            <div className="on-done-actions">
              <Link className="btn btn-primary" href={`/collectives/${created.id}`} data-autofocus>
                open {createdName}
              </Link>
              <Link className="btn btn-secondary" href={`/collectives/${created.id}/settings?section=members`}>
                invite more people
              </Link>
            </div>
          </div>
        ) : null}
      </div>
    </Dialog>
  );
}
