"use client";

import Link from "next/link";
import { MailX } from "lucide-react";
import { isApiErrorStatus } from "@/lib/api";
import { useUnsubscribeInvitationEmails } from "@/lib/queries/notificationEmail";
import { OnEmpty, OnHead, OnPage } from "@/components/online/Page";

const INVALID = "this link is invalid or expired. you can turn invitation emails off in settings.";

/**
 * The one-click unsubscribe for invitation emails. It works signed out: the
 * signed token in the link names the account, and it can only turn invitation
 * emails off. The request is sent when the person presses the button and never
 * on arrival, so a mail scanner that opens the link changes nothing.
 */
export default function UnsubscribePage({ token }: { token: string | null }) {
  const unsubscribe = useUnsubscribeInvitationEmails();

  const failed = token === null || (unsubscribe.isError && isApiErrorStatus(unsubscribe.error, 400));
  const failure = unsubscribe.isError && !failed ? unsubscribe.error.message : null;

  return (
    <OnPage tight testId="unsubscribe-page">
      <OnHead title="stop invitation emails" />
      {unsubscribe.isSuccess ? (
        <OnEmpty
          testId="unsubscribe-done"
          icon={<MailX />}
          title="invitation emails are off"
          body="you won't get emails when a collective invites you. invitations still show on your collectives page, and you can turn the emails back on in settings."
          actions={
            <Link className="btn btn-secondary" href="/settings#email">
              settings
            </Link>
          }
        />
      ) : failed ? (
        <OnEmpty
          testId="unsubscribe-invalid"
          icon={<MailX />}
          title="link not valid"
          body={INVALID}
          actions={
            <Link className="btn btn-secondary" href="/settings#email">
              settings
            </Link>
          }
        />
      ) : (
        <OnEmpty
          testId="unsubscribe-ask"
          icon={<MailX />}
          title="stop emails about invitations?"
          body="this turns off the emails village sends when a collective invites you. nothing else changes."
          actions={
            <>
              <button
                type="button"
                className="btn btn-primary"
                disabled={unsubscribe.isPending}
                aria-busy={unsubscribe.isPending || undefined}
                onClick={() => token && unsubscribe.mutate(token)}
              >
                {unsubscribe.isPending ? "stopping" : "stop invitation emails"}
              </button>
              {failure ? (
                <p role="alert" className="on-error">
                  the emails were not stopped: {failure}. try again.
                </p>
              ) : null}
            </>
          }
        />
      )}
    </OnPage>
  );
}
