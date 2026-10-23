/**
 * The signed-in person's own landing page, as the in-page links that lead
 * back to it name it: the profile and project breadcrumbs, the transcript
 * page's crumb, and the not-found states' way home.
 *
 * The navigation itself lives in the sidebar (`components/shell/OnlineSidebar`),
 * which fairtrade's `AppSidebar` draws; this is only the label and address the
 * page bodies share, so they import it instead of re-typing `/`.
 */
export interface NavSection {
  id: string;
  href: string;
  label: string;
}

export const HOME_SECTION: NavSection = {
  id: "home",
  href: "/",
  label: "home",
};
