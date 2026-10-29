import { useEffect, type RefObject } from "react";

/**
 * The app's search lives in Discover: the sidebar's search item
 * and ⌘K / Ctrl+K open it with the cursor in its search box. Opened from
 * another page, the request waits until Discover has drawn its box; opened on
 * Discover itself, the box takes the cursor at once.
 */
export const EXPLORE_HREF = "/discover?view=transcripts";
const FOCUS_EVENT = "peasant-online:focus-explore-search";
/** How long a request to focus the box waits for Discover to draw it. */
const ARRIVAL_MS = 3000;
let pendingUntil = 0;
/** What had the cursor when the search was asked for: the sidebar's search
 *  item stays on screen across the move, so its keeping focus is not the
 *  person putting the cursor somewhere else. */
let askedFrom: Element | null = null;

/** Opens Discover with the cursor in its search box. */
export function openExploreSearch(push: (href: string) => void, pathname: string): void {
  if (pathname === "/discover" || pathname === "/explore") {
    window.dispatchEvent(new Event(FOCUS_EVENT));
    return;
  }
  pendingUntil = Date.now() + ARRIVAL_MS;
  askedFrom = document.activeElement;
  push(EXPLORE_HREF);
}

/** Puts the cursor in the first text box inside `root` when the search asks for it. */
export function useExploreSearchFocus(root: RefObject<HTMLElement | null>): void {
  // Runs after every render: the box is drawn only once the first results
  // arrive, and a waiting request is answered on the render that draws it.
  // While the request is fresh, a box redrawn as the page settles takes the
  // cursor back, unless the person has already put it somewhere else.
  useEffect(() => {
    if (Date.now() > pendingUntil) return;
    const active = document.activeElement;
    const unmoved = !active || active === document.body || active === askedFrom;
    if (!unmoved && root.current?.contains(active)) return;
    if (!unmoved) {
      pendingUntil = 0;
      return;
    }
    focusSearch(root.current);
  });
  useEffect(() => {
    const onFocus = () => focusSearch(root.current);
    window.addEventListener(FOCUS_EVENT, onFocus);
    return () => window.removeEventListener(FOCUS_EVENT, onFocus);
  }, [root]);
}

function focusSearch(root: HTMLElement | null): boolean {
  const input = [...(root?.querySelectorAll<HTMLInputElement>('input[type="text"], input[type="search"]') ?? [])].find((box) => !box.closest("[hidden]"));
  if (!input) return false;
  input.focus();
  input.select();
  return true;
}

/** Opens the search with ⌘K or Ctrl+K anywhere in the app. */
export function useSearchShortcut(onOpen: () => void): void {
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && !event.altKey && !event.shiftKey && event.key.toLowerCase() === "k") {
        event.preventDefault();
        onOpen();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onOpen]);
}
