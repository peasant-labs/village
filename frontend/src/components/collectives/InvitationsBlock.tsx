"use client";

import { useId, useState } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { UserCheck } from "lucide-react";
import type { VillageCollectiveInvitation } from "@peasant-labs/schema";
import { ConsentDialog, Toast } from "@/lib/ft-ui";
import { isApiErrorStatus } from "@/lib/api";
import { formatExpiresIn } from "@/lib/format";
import { invitedByLabel, invitedYouParts } from "@/lib/invitationCopy";
import { useAcceptInvitation, useDeclineInvitation, useMyInvitations } from "@/lib/queries/invitations";
import JoinConsentDialog from "@/components/group/JoinConsentDialog";
import RetryButton from "@/components/RetryButton";

/** The query parameter an invitation email's link carries: `/collectives?invitation=<id>`. */
export const INVITATION_PARAM = "invitation";

interface Notice {
  ok: boolean;
  title: string;
  detail: string;
}

function messageOf(error: unknown): string {
  return error instanceof Error && error.message ? error.message : "something went wrong. try again.";
}

/**
 * The signed-in person's open invitations, at the top of `/collectives`.
 *
 * Accepting is consent: it opens the same dialog self-join does, with the
 * inviter named first, and a person who is not discoverable is told who will
 * see their profile before they confirm. Declining is one click and tells no
 * one. An invitation link (`?invitation=<id>`) opens the accept dialog for that
 * invitation. Nothing is drawn when there is nothing to decide.
 */
export default function InvitationsBlock({ discoverable }: { discoverable: boolean }) {
  const headingId = useId();
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const invitations = useMyInvitations(true);
  const accept = useAcceptInvitation();
  const decline = useDeclineInvitation();
  const [accepting, setAccepting] = useState<VillageCollectiveInvitation | null>(null);
  const [notice, setNotice] = useState<Notice | null>(null);

  const rows = invitations.data?.invitations ?? [];

  // An invitation link opens its dialog once the list has arrived. The address
  // is the state: the dialog stays open while the parameter does, and the
  // parameter is dropped once the person acts on it, so a reload does not open
  // it again. `linkSettled` covers the moment between acting and the address
  // changing, when the accepted invitation has already left the list.
  const [linkSettled, setLinkSettled] = useState(false);
  const linked = linkSettled ? null : searchParams.get(INVITATION_PARAM);
  const linkedMatch = linked && invitations.isSuccess ? (rows.find((row) => row.id === linked) ?? null) : null;
  const linkMissing = !!linked && invitations.isSuccess && linkedMatch === null;
  const target = accepting ?? linkedMatch;
  const settleLink = () => {
    if (!linked) return;
    setLinkSettled(true);
    router.replace(pathname);
  };

  const gone = (error: unknown) => isApiErrorStatus(error, 404);

  const closeAccept = () => {
    setAccepting(null);
    settleLink();
  };

  const confirmAccept = () => {
    const invitation = target;
    if (!invitation) return;
    accept.mutate(invitation.id, {
      onSuccess: () => {
        closeAccept();
        setNotice({ ok: true, title: "joined", detail: `you're a member of ${invitation.collective.name} now.` });
      },
      onError: (error) => {
        closeAccept();
        setNotice(
          gone(error)
            ? { ok: false, title: "invitation not open", detail: "that invitation has expired or been withdrawn." }
            : { ok: false, title: "could not join", detail: messageOf(error) },
        );
      },
    });
  };

  const declineOne = (invitation: VillageCollectiveInvitation) => {
    decline.mutate(invitation.id, {
      onSuccess: () => setNotice({ ok: true, title: "invitation declined", detail: "declined. the owners aren't told." }),
      onError: (error) =>
        setNotice(
          gone(error)
            ? { ok: false, title: "invitation not open", detail: "that invitation has expired or been withdrawn." }
            : { ok: false, title: "not declined", detail: messageOf(error) },
        ),
    });
  };

  const shown: Notice | null = linkMissing
    ? { ok: false, title: "invitation not open", detail: "that invitation has expired, been withdrawn, or is for another account." }
    : notice;
  const toast = shown ? (
    <div data-testid="invitations-notice">
      <Toast
        variant={shown.ok ? "ok" : "err"}
        title={shown.title}
        onClose={() => {
          setNotice(null);
          if (linkMissing) settleLink();
        }}
      >
        {shown.detail}
      </Toast>
    </div>
  ) : null;

  if (invitations.isError) {
    return (
      <section className="on-section on-invitations" aria-labelledby={headingId} data-testid="my-invitations">
        <h2 id={headingId} className="on-h2">
          invitations
        </h2>
        <div className="on-notice" data-tone="danger" role="alert">
          <p>could not read your invitations: {messageOf(invitations.error)}</p>
          <RetryButton
            label={invitations.isFetching ? "retrying" : "try again"}
            busy={invitations.isFetching}
            onRetry={() => invitations.refetch()}
            testId="my-invitations-retry"
          />
        </div>
      </section>
    );
  }

  const parts = target ? invitedYouParts(target) : null;
  const invitedLine = parts ? (
    <>
      {parts.before}
      <span className="cns-name">{parts.name}</span>
      {parts.after}
    </>
  ) : null;

  return (
    <>
      {toast}
      {rows.length > 0 ? (
        <section className="on-section on-invitations" aria-labelledby={headingId} data-testid="my-invitations">
          <h2 id={headingId} className="on-h2">
            invitations
          </h2>
          <div className="on-tbl" tabIndex={0} role="region" aria-label="your invitations">
            <table>
              <caption className="sr-only">your invitations</caption>
              <thead>
                <tr>
                  <th scope="col">collective</th>
                  <th scope="col">from</th>
                  <th scope="col">expires</th>
                  <th scope="col">
                    <span className="sr-only">actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {rows.map((invitation) => {
                  const busy =
                    (decline.isPending && decline.variables === invitation.id) ||
                    (accept.isPending && accept.variables === invitation.id);
                  return (
                    <tr key={invitation.id} data-invitation-id={invitation.id}>
                      <td>
                        <span className="is-title normal-case">{invitation.collective.name}</span>
                        {invitation.collective.acceptance_mode === "curated" ? (
                          <>
                            {" "}
                            <span className="is-sub">joins by invitation</span>
                          </>
                        ) : null}
                      </td>
                      <td className="normal-case">from {invitedByLabel(invitation.invited_by)}</td>
                      <td className="tnum">
                        <time dateTime={invitation.expires_at}>{formatExpiresIn(invitation.expires_at)}</time>
                      </td>
                      <td>
                        <span className="on-invitation-actions">
                          <button
                            type="button"
                            className="btn btn-primary btn-sm"
                            disabled={busy}
                            aria-label={`accept the invitation to ${invitation.collective.name}`}
                            onClick={() => setAccepting(invitation)}
                          >
                            accept
                          </button>
                          <button
                            type="button"
                            className="btn btn-ghost btn-sm"
                            disabled={busy}
                            aria-label={`decline the invitation to ${invitation.collective.name}`}
                            onClick={() => declineOne(invitation)}
                          >
                            decline
                          </button>
                        </span>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </section>
      ) : null}

      {discoverable ? (
        <ConsentDialog
          open={target !== null}
          labelId="cns-accept-invitation"
          title={
            <>
              join <span className="cns-name">{target?.collective.name}</span>?
            </>
          }
          intro={<p>{invitedLine}</p>}
          requireConsent={false}
          confirmLabel="join"
          confirmIcon={UserCheck}
          busy={accept.isPending}
          onCancel={closeAccept}
          onConfirm={confirmAccept}
        />
      ) : (
        <JoinConsentDialog
          open={target !== null}
          onClose={closeAccept}
          onConfirm={confirmAccept}
          collectiveName={target?.collective.name ?? ""}
          isSubmitting={accept.isPending}
          invitedLine={invitedLine}
        />
      )}
    </>
  );
}
