"use client";

import { useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { LogOut, Settings, UserRound } from "lucide-react";
import { useLogout } from "@/lib/queries/auth";
import { Avatar, Dialog, Menu } from "@/lib/ft-ui";

/**
 * The signed-in person's corner of the chrome: their avatar and handle, which
 * opens a menu holding their profile, their settings and sign out. The theme
 * switch is the sidebar's own, above this corner.
 *
 * The design system's `Menu` owns the trigger, the popout and the keyboard
 * behaviour; the trigger reads as the person rather than as a generic
 * "account" button. Signing out opens a modal that says the request is in
 * flight, and says so again with a retry if it fails.
 */
export default function AccountMenu({ user }: { user: { github_username: string; avatar_url: string | null } }) {
  const router = useRouter();
  const logout = useLogout();
  const handle = user.github_username;
  const logoutAdmission = useRef(false);
  const [logoutQueued, setLogoutQueued] = useState(false);
  const [logoutOpen, setLogoutOpen] = useState(false);
  function beginLogout() {
    if (logoutAdmission.current || logout.isPending) return;
    logoutAdmission.current = true;
    setLogoutQueued(true);
    // The menu returns focus when it closes. Open the modal after that frame
    // so its canonical focus trap remains the active interaction.
    requestAnimationFrame(() => requestAnimationFrame(() => {
      setLogoutOpen(true);
      logout.mutate();
      setLogoutQueued(false);
      logoutAdmission.current = false;
    }));
  }

  return (
    <div className="on-me" data-testid="account-area">
      <Menu
        align="start"
        label={
          <span className="on-me-trigger" data-testid="account-menu-trigger">
            {/* DS Avatar: photo when avatar_url is set, else its own initials.
                Hidden from assistive technology: the handle beside it already
                names the person, and the avatar would say it twice. */}
            <Avatar name={handle} src={user.avatar_url ?? undefined} size="sm" aria-hidden="true" />
            {/* The space is its own text node: a flex container drops it from
                the layout, and the accessible name keeps it between the words. */}
            <span className="sr-only">account menu for</span>{" "}
            <span className="on-me-handle">@{handle}</span>
          </span>
        }
        items={[
          {
            label: "profile",
            icon: UserRound,
            onSelect: () => router.push(`/users/${encodeURIComponent(handle)}`),
          },
          { label: "settings", icon: Settings, onSelect: () => router.push("/settings") },
          { label: "", separator: true },
          { label: "sign out", icon: LogOut, disabled: logoutQueued || logout.isPending, onSelect: beginLogout },
        ]}
      />
      <Dialog
        open={logoutOpen}
        onClose={() => setLogoutOpen(false)}
        title={logout.isError ? "could not sign out" : "signing out"}
        dismissible={logout.isError}
        footer={logout.isError ? (
          <button type="button" className="btn btn-secondary btn-sm" onClick={() => logout.mutate()}>try again</button>
        ) : undefined}
      >
        <p role={logout.isError ? "alert" : "status"} style={{ fontSize: "var(--fs-body)" }}>
          {logout.isError
            ? `the sign-out request failed: ${logout.error instanceof Error ? logout.error.message : "an unknown error"}. try again.`
            : "waiting for the sign-out request."}
        </p>
      </Dialog>
    </div>
  );
}
