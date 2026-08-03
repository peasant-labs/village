"use client";

import { useEffect, useCallback, useSyncExternalStore } from "react";

type Theme = "light" | "dark";

const STORAGE_KEY = "peasant-theme";
const THEME_CHANGE_EVENT = "peasant-theme-change";

function currentTheme(): Theme {
  return localStorage.getItem(STORAGE_KEY) === "light" ? "light" : "dark";
}

function subscribeToTheme(onStoreChange: () => void) {
  window.addEventListener("storage", onStoreChange);
  window.addEventListener(THEME_CHANGE_EVENT, onStoreChange);
  return () => {
    window.removeEventListener("storage", onStoreChange);
    window.removeEventListener(THEME_CHANGE_EVENT, onStoreChange);
  };
}

export function useTheme() {
  // Dark is the default — matches the server-rendered <html data-theme="dark">
  // and fairtrade's :root dark token default.
  const theme = useSyncExternalStore<Theme>(
    subscribeToTheme,
    currentTheme,
    () => "dark",
  );

  useEffect(() => {
    document.documentElement.setAttribute("data-theme", theme);
  }, [theme]);

  const toggle = useCallback(() => {
    const next: Theme = currentTheme() === "light" ? "dark" : "light";
    localStorage.setItem(STORAGE_KEY, next);
    document.documentElement.setAttribute("data-theme", next);
    window.dispatchEvent(new Event(THEME_CHANGE_EVENT));
  }, []);

  return { theme, toggle };
}
