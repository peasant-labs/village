"use client";

/**
 * LOCAL REBUILD (design-system gap) — GitHub user typeahead.
 *
 * The fairtrade design system ships no GitHub-username typeahead, so this widget
 * is rebuilt in-app from supported parts: the search field is the fairtrade input
 * shell (`.input.is-input` inside an `.input-ico` wrapper with a leading icon) and
 * the results popout is modelled on the fairtrade Menu specimen — a real
 * `role="menu"` list of `role="menuitem"` rows with roving focus (Up/Down move,
 * Home/End jump, Enter/Space select, Esc closes and returns focus to the input).
 * Styling reads only design-system tokens/classes (`var(--…)`, `.menu-*`), square
 * and hairline. The debounced `GET /search/users` fetch stays app-local.
 *
 * Tagged for future fairtrade pickup: if the design system grows a first-class
 * username/typeahead combobox, this file is its first consumer to migrate.
 */

import { useState, useEffect, useRef, useId } from "react";
import { Search, X } from "lucide-react";
import { cn } from "@/lib/utils";

interface GitHubUser {
  login: string;
  avatar_url: string;
}

interface GitHubUserSearchProps {
  value: string;
  onChange: (value: string) => void;
  onSelect: (username: string) => void;
  placeholder?: string;
  className?: string;
}

export default function GitHubUserSearch({
  value,
  onChange,
  onSelect,
  placeholder = "GitHub username",
  className = "",
}: GitHubUserSearchProps) {
  const [results, setResults] = useState<GitHubUser[]>([]);
  const [open, setOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const itemRefs = useRef<(HTMLLIElement | null)[]>([]);
  const debounceRef = useRef<ReturnType<typeof setTimeout>>(undefined);
  // True when the next `value` change is a programmatic selection (not user
  // typing), so the search effect must NOT re-open the menu over the just-picked
  // handle. Reset by any real keystroke and consumed by the effect.
  const skipSearchRef = useRef(false);
  const menuId = useId();

  // Debounced GitHub user search. Kept app-local (the design system has no
  // GitHub-aware control); silently degrades on rate-limit / network error.
  useEffect(() => {
    clearTimeout(debounceRef.current);

    // A value set by selectUser is programmatic — don't re-search/re-open over
    // the handle just chosen (the menu stays dismissed after a pick).
    if (skipSearchRef.current) {
      skipSearchRef.current = false;
      return () => clearTimeout(debounceRef.current);
    }

    if (value.length < 2) {
      // Defer the reset onto a task so we never call setState synchronously in
      // the effect body (avoids cascading renders); near-instant in practice.
      debounceRef.current = setTimeout(() => {
        setResults([]);
        setOpen(false);
      });
      return () => clearTimeout(debounceRef.current);
    }

    debounceRef.current = setTimeout(async () => {
      try {
        const res = await fetch(
          `https://api.github.com/search/users?q=${encodeURIComponent(value)}&per_page=5`
        );
        if (!res.ok) return;
        const data = await res.json();
        setResults(data.items || []);
        setOpen(true);
      } catch {
        // silently fail on rate limit etc
      }
    }, 300);

    return () => clearTimeout(debounceRef.current);
  }, [value]);

  // Outside-click dismissal (Esc is handled on the menu so it can return focus).
  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, []);

  function focusItem(index: number) {
    const el = itemRefs.current[index];
    el?.focus();
  }

  function closeAndReturnFocus() {
    setOpen(false);
    inputRef.current?.focus();
  }

  function selectUser(user: GitHubUser) {
    skipSearchRef.current = true;
    onChange(user.login);
    onSelect(user.login);
    setResults([]);
    setOpen(false);
    inputRef.current?.focus();
  }

  // From the input: Down/Up open the menu and move focus into it (the Menu
  // specimen's trigger behavior); Esc closes. Enter is left to the surrounding
  // form so a typed handle still submits.
  function handleInputKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (!open || results.length === 0) {
      if (e.key === "Escape") setOpen(false);
      return;
    }
    if (e.key === "ArrowDown") {
      e.preventDefault();
      focusItem(0);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      focusItem(results.length - 1);
    } else if (e.key === "Escape") {
      e.preventDefault();
      setOpen(false);
    }
  }

  // Roving focus within the results menu (mirrors fairtrade Menu's keyboard model).
  function handleItemKeyDown(e: React.KeyboardEvent<HTMLLIElement>, index: number) {
    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        focusItem((index + 1) % results.length);
        break;
      case "ArrowUp":
        e.preventDefault();
        focusItem((index - 1 + results.length) % results.length);
        break;
      case "Home":
        e.preventDefault();
        focusItem(0);
        break;
      case "End":
        e.preventDefault();
        focusItem(results.length - 1);
        break;
      case "Enter":
      case " ":
        e.preventDefault();
        selectUser(results[index]);
        break;
      case "Escape":
        e.preventDefault();
        closeAndReturnFocus();
        break;
      case "Tab":
        setOpen(false);
        break;
    }
  }

  const showMenu = open && results.length > 0;

  return (
    <div ref={containerRef} className={cn("relative", className)}>
      <div className="input-ico">
        <Search className="lucide" aria-hidden="true" />
        <input
          ref={inputRef}
          type="text"
          name="github-username"
          role="combobox"
          aria-expanded={showMenu}
          aria-controls={menuId}
          aria-autocomplete="list"
          value={value}
          onChange={(e) => {
            skipSearchRef.current = false;
            onChange(e.target.value);
          }}
          onKeyDown={handleInputKeyDown}
          autoComplete="off"
          autoCorrect="off"
          autoCapitalize="off"
          spellCheck={false}
          data-1p-ignore
          data-lpignore="true"
          data-bwignore
          data-form-type="other"
          className="input is-input"
          style={{ paddingRight: "2.25rem" }}
          placeholder={placeholder}
        />
        {value.length > 0 && (
          <button
            type="button"
            onClick={() => {
              skipSearchRef.current = false;
              onChange("");
              setOpen(false);
              inputRef.current?.focus();
            }}
            aria-label="Clear search"
            className="absolute right-2 top-1/2 -translate-y-1/2 inline-flex size-5 items-center justify-center text-ink-4 transition-colors hover:text-ink cursor-pointer focus-mono"
          >
            <X className="size-3.5" />
          </button>
        )}
      </div>
      {showMenu && (
        <div className="menu-pop menu-float right-0">
          <p className="menu-cap">
            GitHub users — must have a platform account to invite
          </p>
          <ul
            id={menuId}
            role="menu"
            aria-label="GitHub user results"
            className="menu-list"
          >
            {results.map((user, i) => (
              <li
                key={user.login}
                ref={(el) => {
                  itemRefs.current[i] = el;
                }}
                role="menuitem"
                tabIndex={-1}
                onClick={() => selectUser(user)}
                onKeyDown={(e) => handleItemKeyDown(e, i)}
                className="menu-item cursor-pointer"
              >
                <img
                  src={user.avatar_url}
                  alt=""
                  className="size-6 border border-rule object-cover shrink-0"
                />
                <span className="menu-text font-mono text-[13px]">{user.login}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
