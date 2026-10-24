"use client";

import { useRef, useState, type ComponentProps, type ReactNode } from "react";
import { useRouter } from "next/navigation";
import { LogOut, Settings, UserRound } from "lucide-react";
import { useLogout } from "@/lib/queries/auth";
import { Dialog, type Menu } from "@/lib/ft-ui";

type MenuItems = NonNullable<ComponentProps<typeof Menu>["items"]>;

/**
 * The signed-in person's corner of the sidebar: fairtrade's account row (their
 * avatar and handle) is itself the menu holding their profile, their settings
 * and sign out. The row, its trigger, the popout and the keyboard behaviour
 * are fairtrade's; online supplies the rows and what choosing each one does.
 *
 * Signing out opens a modal that says the request is in flight, and says so
 * again with a retry if it fails; `dialog` is that modal, mounted beside the
 * sidebar. With no person yet there are no rows and no modal.
 */
export function useAccountMenu(user: { github_username: string } | null): { items: MenuItems; dialog: ReactNode } {
  const router = useRouter();
  const logout = useLogout();
  const handle = user?.github_username ?? "";
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

  if (!user) return { items: [], dialog: null };

  const items: MenuItems = [
    {
      label: "profile",
      icon: UserRound,
      onSelect: () => router.push(`/users/${encodeURIComponent(handle)}`),
    },
    { label: "settings", icon: Settings, onSelect: () => router.push("/settings") },
    { label: "", separator: true },
    { label: "sign out", icon: LogOut, disabled: logoutQueued || logout.isPending, onSelect: beginLogout },
  ];

  const dialog = (
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
  );

  return { items, dialog };
}
