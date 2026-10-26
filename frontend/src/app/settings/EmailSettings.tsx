"use client";

import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import type { UseQueryResult } from "@tanstack/react-query";
import type { VillageUserSettings } from "@peasant-labs/schema";
import { SettingGroup, SettingRow } from "@/lib/ft-ui";
import { isApiErrorStatus } from "@/lib/api";
import {
  useRemoveNotificationEmail,
  useSetNotificationEmail,
  useUpdateEmailInvitations,
  useVerifyNotificationEmail,
} from "@/lib/queries/notificationEmail";

/** The query parameter a verification email's link carries: `/settings?verify_email=<code>`. */
export const VERIFY_EMAIL_PARAM = "verify_email";

const NO_EMAIL = "this village doesn't send email.";
const BAD_CODE = "that code is wrong, has expired, or is for an address you've since replaced. send a new one.";

function errorMessage(error: unknown): string {
  return error instanceof Error && error.message ? error.message : String(error);
}

function setRefusal(error: unknown): string {
  if (isApiErrorStatus(error, 429)) return "too many verification emails for now. try again later.";
  return `your address was not saved: ${errorMessage(error)}`;
}

function verifyRefusal(error: unknown): string {
  return isApiErrorStatus(error, 400) ? BAD_CODE : `your address was not verified: ${errorMessage(error)}`;
}

/**
 * The email section of `/settings`: one address Village may email when a
 * collective invites the person, its verification, and the switch for those
 * emails. Adding an address emails a code to it; the email's link brings the
 * person back here with the code, which is confirmed on arrival and dropped
 * from the address. A Village that sends no email answers 501, and the section
 * says so instead of offering the form again.
 */
export default function EmailSettings({ handle, settings }: { handle: string; settings: UseQueryResult<VillageUserSettings> }) {
  const ids = useId();
  const setEmail = useSetNotificationEmail(handle);
  const verify = useVerifyNotificationEmail(handle);
  const remove = useRemoveNotificationEmail(handle);
  const updateInvitations = useUpdateEmailInvitations(handle);

  const [emailOff, setEmailOff] = useState(false);
  const [editing, setEditing] = useState(false);
  const [address, setAddress] = useState("");
  const [code, setCode] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [status, setStatus] = useState<string | null>(null);

  // The verification link: confirm its code once, then drop it from the
  // address so a reload or a shared screenshot does not carry it.
  const linkHandled = useRef(false);
  useEffect(() => {
    if (linkHandled.current) return;
    const params = new URLSearchParams(window.location.search);
    const linked = params.get(VERIFY_EMAIL_PARAM);
    if (!linked) return;
    linkHandled.current = true;
    params.delete(VERIFY_EMAIL_PARAM);
    const rest = params.toString();
    window.history.replaceState(window.history.state, "", `${window.location.pathname}${rest ? `?${rest}` : ""}${window.location.hash}`);
    // The promise, not per-call callbacks: React's development double mount
    // detaches a mutation's call callbacks, and this call is made only once.
    verify.mutateAsync(linked).then(
      () => {
        setError(null);
        setStatus("your address is verified.");
      },
      (failure: unknown) => setError(verifyRefusal(failure)),
    );
  }, [verify]);

  const current = settings.data?.notification_email ?? null;
  const showForm = !emailOff && (current === null || editing);

  const send = (event: FormEvent) => {
    event.preventDefault();
    const wanted = address.trim();
    if (!wanted) return;
    setError(null);
    setStatus(null);
    setEmail.mutate(wanted, {
      onSuccess: () => {
        setEditing(false);
        setAddress("");
        setStatus("we sent a code to that address. enter it below, or open the link in the email.");
      },
      onError: (failure) => {
        if (isApiErrorStatus(failure, 501)) setEmailOff(true);
        else setError(setRefusal(failure));
      },
    });
  };

  const resend = () => {
    if (!current) return;
    setError(null);
    setStatus(null);
    setEmail.mutate(current.address, {
      onSuccess: () => setStatus("we sent a new code."),
      onError: (failure) => {
        if (isApiErrorStatus(failure, 501)) setEmailOff(true);
        else setError(setRefusal(failure));
      },
    });
  };

  const confirm = (event: FormEvent) => {
    event.preventDefault();
    const wanted = code.trim();
    if (!wanted) return;
    setError(null);
    setStatus(null);
    verify.mutate(wanted, {
      onSuccess: () => {
        setCode("");
        setStatus("your address is verified.");
      },
      onError: (failure) => setError(verifyRefusal(failure)),
    });
  };

  const removeAddress = () => {
    setError(null);
    setStatus(null);
    remove.mutate(undefined, {
      onSuccess: () => {
        setEditing(false);
        setStatus("your address is removed. village keeps no copy of it.");
      },
      onError: (failure) => setError(`your address was not removed: ${errorMessage(failure)}`),
    });
  };

  return (
    <SettingGroup id="email" label="email" defaultOpen count={null} data-testid="settings-email">
      {settings.data == null ? (
        <div className="srow">
          <span className="srow-help">{settings.isError ? "your email settings could not be read." : "checking your email settings"}</span>
        </div>
      ) : (
        <>
          {current && !editing ? (
            <div className="srow" data-testid="settings-email-address">
              <span className="srow-text-col">
                <span className="srow-label normal-case">{current.address}</span>
                <span className="srow-help">
                  {current.verified ? "verified." : "not verified yet. enter the code we emailed you."}
                </span>
              </span>
              <span className="on-row">
                {!current.verified && !emailOff ? (
                  <button type="button" className="btn btn-ghost btn-sm" disabled={setEmail.isPending} onClick={resend}>
                    {setEmail.isPending ? "sending" : "send a new code"}
                  </button>
                ) : null}
                {!emailOff ? (
                  <button type="button" className="btn btn-secondary btn-sm" onClick={() => setEditing(true)}>
                    change
                  </button>
                ) : null}
                <button type="button" className="btn btn-secondary btn-sm" disabled={remove.isPending} onClick={removeAddress}>
                  {remove.isPending ? "removing" : "remove"}
                </button>
              </span>
            </div>
          ) : null}

          {current && !current.verified && !editing ? (
            <form className="srow" onSubmit={confirm} data-testid="settings-email-verify">
              <span className="srow-text-col">
                <label className="srow-label" htmlFor={`${ids}-code`}>
                  verification code
                </label>
                <input
                  id={`${ids}-code`}
                  className="on-input"
                  value={code}
                  autoComplete="one-time-code"
                  spellCheck={false}
                  onChange={(event) => setCode(event.target.value)}
                />
              </span>
              <button type="submit" className="btn btn-secondary btn-sm" disabled={!code.trim() || verify.isPending}>
                {verify.isPending ? "verifying" : "verify"}
              </button>
            </form>
          ) : null}

          {emailOff ? (
            <div className="srow" data-testid="settings-email-off">
              <span className="srow-help">{NO_EMAIL}</span>
            </div>
          ) : showForm ? (
            <form className="srow" onSubmit={send} data-testid="settings-email-form">
              <span className="srow-text-col">
                <label className="srow-label" htmlFor={`${ids}-address`}>
                  {current ? "new email address" : "email address"}
                </label>
                <span className="srow-help">
                  only for invitations from collectives. we email you a code to check it&apos;s yours.
                </span>
                <input
                  id={`${ids}-address`}
                  className="on-input"
                  type="email"
                  value={address}
                  autoComplete="email"
                  spellCheck={false}
                  onChange={(event) => setAddress(event.target.value)}
                />
              </span>
              <span className="on-row">
                {editing ? (
                  <button type="button" className="btn btn-ghost btn-sm" onClick={() => setEditing(false)}>
                    cancel
                  </button>
                ) : null}
                <button type="submit" className="btn btn-secondary btn-sm" disabled={!address.trim() || setEmail.isPending}>
                  {setEmail.isPending ? "sending" : "send code"}
                </button>
              </span>
            </form>
          ) : null}

          {status ? (
            <div className="srow">
              <span className="srow-help" role="status">
                {status}
              </span>
            </div>
          ) : null}
          {error ? (
            <div className="srow">
              <p role="alert" className="on-acct-alert">
                {error}
              </p>
            </div>
          ) : null}

          <SettingRow
            label="email me when a collective invites me"
            help="only to a verified address. every email has a link to stop them."
            control="switch"
            value={settings.data.email_invitations}
            disabled={settings.isFetching}
            onCommit={async (next) => {
              await updateInvitations.mutateAsync(Boolean(next));
            }}
            data-testid="settings-email-invitations"
          />
        </>
      )}
    </SettingGroup>
  );
}
