"use client";

import { useState } from "react";
import Link from "next/link";
import { useAuth } from "@/providers/AuthProvider";
import {
  useDeleteAccount,
  useLogout,
  useSetUsername,
  useUpdateMySettings,
} from "@/lib/queries/auth";
import { useUpdateAutoAttachPullRequests, useUserPromptSettings } from "@/lib/queries/pulls";
import {
  activePeasantKeys,
  newestUse,
  useMyApiKeys,
  useRevokeApiKeys,
} from "@/lib/queries/account";
import { isApiErrorStatus } from "@/lib/api";
import { handleTakenMessage } from "@/lib/handle";
import { formatRelativeTime } from "@/lib/format";
import { ConfirmInline, SettingGroup, SettingRow } from "@/lib/ft-ui";
import RetryButton from "@/components/RetryButton";
import type { User } from "@/lib/types";

/**
 * `/settings`: the signed-in person's own account, one saved field at a time.
 *
 * Every control saves on its own through fairtrade's `SettingRow`: a switch
 * applies the moment it changes, and the handle saves when the person presses
 * save. A write that fails puts the previous value back and says why beside the
 * control. Every read and write here is a route village already serves.
 */
export default function AccountSettingsPage() {
  const { user, isLoading, isLoggedIn } = useAuth();

  if (isLoading) {
    return (
      <div className="iu-view">
        <div className="iu-page" data-testid="settings-pending">
          <div className="h-8 w-64 animate-shimmer" />
          <div className="h-48 animate-shimmer" />
        </div>
      </div>
    );
  }

  if (!isLoggedIn || user == null) {
    return (
      <div className="iu-view">
        <div className="iu-page" data-testid="settings-signed-out">
          <header className="iu-page-head">
            <h1 className="iu-page-title">your settings</h1>
            <p className="iu-page-sub">
              sign in to change your settings. <Link href="/" className="cmg-pr-link">go to sign-in</Link>
            </p>
          </header>
        </div>
      </div>
    );
  }

  // One mount per account, so nothing typed or failed for one person's
  // settings is shown to the next.
  return <Settings key={user.id} user={user} />;
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function Settings({ user }: { user: User }) {
  const handle = user.github_username;
  const setUsername = useSetUsername();
  const updateSettings = useUpdateMySettings();
  const settings = useUserPromptSettings(true, handle);
  const updateAutoAttach = useUpdateAutoAttachPullRequests(handle);

  async function commitHandle(next: boolean | string) {
    const wanted = String(next).trim();
    try {
      await setUsername.mutateAsync(wanted);
    } catch (error) {
      // A taken handle reads exactly as it does on /welcome. Anything else is
      // the server's own reason, such as a handle that breaks its format rule.
      if (isApiErrorStatus(error, 409)) throw new Error(handleTakenMessage(wanted));
      throw error;
    }
  }

  return (
    <div className="iu-view">
      <div className="iu-page cmg-settings" data-testid="settings-page">
        <header className="iu-page-head">
          <h1 className="iu-page-title">your settings</h1>
          <p className="iu-page-sub">switches apply right away. text changes when you press edit.</p>
        </header>

        <SettingGroup label="profile" defaultOpen data-testid="settings-profile">
          <SettingRow
            label="handle"
            control="text"
            value={handle}
            onCommit={commitHandle}
            data-testid="settings-handle"
          />
          <SettingRow
            label="discoverable profile"
            help="on: your handle shows on your transcripts and in member lists. off: you show as anon."
            control="switch"
            value={user.is_discoverable}
            onCommit={async (next) => {
              await updateSettings.mutateAsync({ is_discoverable: Boolean(next) });
            }}
            data-testid="settings-discoverable"
          />
        </SettingGroup>

        <SettingGroup label="pull requests" defaultOpen data-testid="settings-pull-requests">
          <SettingRow
            label="link my transcripts to my pull requests automatically"
            help="off: comment /peasant attach on a pull request. on: when you open a pull request in a repo that one of your collectives links, your transcripts that trace its commits are linked. who can read them does not change."
            control="switch"
            value={settings.data?.auto_attach_pull_requests ?? false}
            disabled={settings.data == null || settings.isFetching}
            onCommit={async (next) => {
              await updateAutoAttach.mutateAsync(Boolean(next));
            }}
            data-testid="settings-auto-link"
          />
          {settings.isError && (
            <div className="srow" data-testid="settings-auto-link-read-error">
              <p role="alert" style={{ fontSize: "var(--fs-body)", color: "var(--error)" }}>
                your automatic pull request setting could not be read: {errorMessage(settings.error)}
              </p>
              <RetryButton label={settings.isFetching ? "retrying" : "retry"} busy={settings.isFetching} onRetry={() => settings.refetch()} testId="settings-auto-link-retry" />
            </div>
          )}
        </SettingGroup>

        <SettingGroup
          label="connections"
          defaultOpen
          count={2}
          noun={["connection", "connections"]}
          data-testid="settings-connections"
        >
          <GitHubConnection user={user} />
          <PeasantConnection handle={handle} />
        </SettingGroup>

        <SettingGroup label="danger zone" defaultOpen count={null} data-testid="settings-danger">
          <DeleteAccount />
        </SettingGroup>
      </div>
    </div>
  );
}

function GitHubConnection({ user }: { user: User }) {
  const logout = useLogout();
  const provider = user.provider ?? "github";
  const login = user.provider_username ?? user.github_username;
  return (
    <div className="srow" data-testid="settings-sign-in-account">
      <span className="srow-text-col">
        <span className="srow-label">
          {provider} · <span className="normal-case">{login}</span>
        </span>
        <span className="srow-help">you sign in to village with this account.</span>
        {logout.isError && (
          <span role="alert" style={{ fontSize: "var(--fs-body)", color: "var(--error)" }}>
            could not sign out: {errorMessage(logout.error)}. try again.
          </span>
        )}
      </span>
      <button
        type="button"
        className="btn btn-secondary btn-sm"
        disabled={logout.isPending}
        onClick={() => logout.mutate()}
      >
        {logout.isPending ? "signing out" : "sign out"}
      </button>
    </div>
  );
}

/**
 * How many computers peasant is signed in on, and the one control that signs
 * it out of all of them.
 *
 * The count is the `peasant-cli` keys that still work. Each `peasant village
 * login` makes one, so the number counts sign-ins and can be higher than the
 * number of machines; the row says "computers" because that is what a person
 * recognises, and adds no machine identity of its own.
 */
function PeasantConnection({ handle }: { handle: string }) {
  const keys = useMyApiKeys(handle, true);
  const revoke = useRevokeApiKeys(handle);
  const [revokeError, setRevokeError] = useState<string | null>(null);

  if (keys.data == null) {
    return (
      <div className="srow" data-testid="settings-peasant">
        <span className="srow-text-col">
          <span className="srow-label">peasant</span>
          {keys.isError ? (
            <span className="srow-error" role="alert">
              your peasant sign-ins could not be loaded: {errorMessage(keys.error)}
            </span>
          ) : (
            <span className="srow-help">checking where peasant is signed in</span>
          )}
        </span>
        {keys.isError && (
          <RetryButton
            label={keys.isFetching ? "retrying" : "retry"}
            busy={keys.isFetching}
            onRetry={() => keys.refetch()}
            testId="settings-peasant-retry"
          />
        )}
      </div>
    );
  }

  const active = activePeasantKeys(keys.data);
  const lastUsed = newestUse(active);

  if (active.length === 0) {
    return (
      <div className="srow" data-testid="settings-peasant">
        <span className="srow-text-col">
          <span className="srow-label">peasant is not signed in on any computer</span>
          <span className="srow-help">
            run <code className="font-mono">peasant village login</code> on a computer to publish
            from it.
          </span>
          {revokeError && (
            <span className="srow-error" role="alert">
              {revokeError}
            </span>
          )}
        </span>
      </div>
    );
  }

  return (
    <div className="srow" data-testid="settings-peasant">
      <span className="srow-text-col">
        <span className="srow-label">
          peasant on <span className="tnum">{active.length}</span>{" "}
          {active.length === 1 ? "computer" : "computers"}
        </span>
        <span className="srow-help">
          it can publish as you.{" "}
          {lastUsed == null ? (
            "not used yet."
          ) : (
            <>
              last used <span className="tnum">{formatRelativeTime(lastUsed)}</span>.
            </>
          )}
        </span>
        {revokeError && (
          <span className="srow-error" role="alert">
            {revokeError}
          </span>
        )}
      </span>
      <ConfirmInline
        label="sign out everywhere"
        busy={revoke.isPending}
        onConfirm={async () => {
          setRevokeError(null);
          try {
            await revoke.mutateAsync(active.map((key) => key.id));
          } catch (error) {
            setRevokeError(errorMessage(error));
          }
        }}
      />
    </div>
  );
}

function DeleteAccount() {
  const deleteAccount = useDeleteAccount();
  const [error, setError] = useState<string | null>(null);
  return (
    <div className="srow" data-testid="settings-delete">
      <span className="srow-text-col">
        <span className="srow-help">
          delete your village account, with the transcripts you published and the collectives you
          own. this cannot be undone.
        </span>
        {error && (
          <span className="srow-error" role="alert">
            your account was not deleted: {error}
          </span>
        )}
      </span>
      <ConfirmInline
        label="delete account"
        busy={deleteAccount.isPending}
        onConfirm={async () => {
          setError(null);
          try {
            await deleteAccount.mutateAsync();
          } catch (failure) {
            setError(errorMessage(failure));
          }
        }}
      />
    </div>
  );
}
