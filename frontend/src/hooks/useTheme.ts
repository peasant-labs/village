"use client";

import { useEffect, useCallback, useSyncExternalStore } from "react";

type Theme = "light" | "dark";

import { LIGHT_SCHEME, THEME_STORAGE_KEY } from "@/lib/themeBoot";

const THEME_CHANGE_EVENT = "peasant-theme-change";

function storedTheme(): Theme | null {
  try {
    const value = globalThis.localStorage?.getItem(THEME_STORAGE_KEY) ?? null;
    return value === "light" || value === "dark" ? value : null;
  } catch {
    return null;
  }
}

function storeTheme(theme: Theme): void {
  try {
    globalThis.localStorage?.setItem(THEME_STORAGE_KEY, theme);
  } catch {
    // The theme still applies to the current document; persistence is best effort.
  }
}

/** The system's scheme, where the browser can say; dark where it cannot. */
function systemTheme(): Theme {
  const query = typeof window !== "undefined" && typeof window.matchMedia === "function"
    ? window.matchMedia(LIGHT_SCHEME)
    : null;
  return query?.matches ? "light" : "dark";
}

/** The person's own choice when they made one on this device, else the system's. */
function currentTheme(): Theme {
  return storedTheme() ?? systemTheme();
}

function subscribeToTheme(onStoreChange: () => void) {
  window.addEventListener("storage", onStoreChange);
  window.addEventListener(THEME_CHANGE_EVENT, onStoreChange);
  // With no stored choice the page follows the system, so a system switch
  // (a scheduled dark mode, a changed setting) re-themes it too.
  const query = typeof window.matchMedia === "function" ? window.matchMedia(LIGHT_SCHEME) : null;
  query?.addEventListener?.("change", onStoreChange);
  return () => {
    window.removeEventListener("storage", onStoreChange);
    window.removeEventListener(THEME_CHANGE_EVENT, onStoreChange);
    query?.removeEventListener?.("change", onStoreChange);
  };
}

export function useTheme() {
  // The server cannot know the theme and renders dark; the head script
  // (lib/themeBoot.ts) has already set the real one before the first paint,
  // and this read agrees with it.
  const theme = useSyncExternalStore<Theme>(
    subscribeToTheme,
    currentTheme,
    () => "dark",
  );

  // Applied from the live read rather than from `theme`: during hydration
  // `theme` is still the server's "dark", and writing it would flash dark over
  // the head script's light for a frame.
  useEffect(() => {
    document.documentElement.setAttribute("data-theme", currentTheme());
  }, [theme]);

  const setTheme = useCallback((next: Theme) => {
    storeTheme(next);
    document.documentElement.setAttribute("data-theme", next);
    window.dispatchEvent(new Event(THEME_CHANGE_EVENT));
  }, []);

  const toggle = useCallback(() => {
    setTheme(currentTheme() === "light" ? "dark" : "light");
  }, [setTheme]);

  return { theme, setTheme, toggle };
}
