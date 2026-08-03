"use client";

// Prior-version deprecation candidate: a standalone search input superseded by
// the shared fairtrade <Explore> surface's built-in .cex-searchbar. It has no
// remaining import sites but is soft-retained rather than deleted.

import { useState, useEffect, useRef } from "react";
import { Search, X } from "lucide-react";

interface SearchBarProps {
  defaultValue?: string;
  onSearch: (query: string) => void;
  placeholder?: string;
}

export default function SearchBar({
  defaultValue = "",
  onSearch,
  placeholder = "Search the commons...",
}: SearchBarProps) {
  const [value, setValue] = useState(defaultValue);

  // The parent passes a fresh onSearch each render — keep it in a ref so it
  // isn't a debounce dependency.
  const onSearchRef = useRef(onSearch);
  useEffect(() => {
    onSearchRef.current = onSearch;
  }, [onSearch]);

  const debounceRef = useRef<ReturnType<typeof setTimeout>>(undefined);
  const firstRun = useRef(true);

  // Live search — fire a debounced search as the user types.
  useEffect(() => {
    if (firstRun.current) {
      firstRun.current = false;
      return;
    }
    clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => onSearchRef.current(value), 250);
    return () => clearTimeout(debounceRef.current);
  }, [value]);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    clearTimeout(debounceRef.current);
    onSearch(value);
  };

  return (
    <form onSubmit={handleSubmit} className="relative">
      <Search
        size={15}
        className="absolute left-3 top-1/2 -translate-y-1/2 text-ink-3 pointer-events-none"
        aria-hidden
      />
      <input
        type="text"
        value={value}
        onChange={(e) => setValue(e.target.value)}
        placeholder={placeholder}
        className="w-full h-10 pl-9 pr-9 bg-surface border border-rule text-sm text-ink placeholder:text-ink-4 transition-colors hover:border-rule-strong focus:border-rule-strong focus-mono cursor-text"
      />
      {value && (
        <button
          type="button"
          onClick={() => {
            clearTimeout(debounceRef.current);
            setValue("");
            onSearch("");
          }}
          aria-label="Clear search"
          className="absolute right-2.5 top-1/2 -translate-y-1/2 text-ink-3 hover:text-ink transition-colors cursor-pointer focus-mono"
        >
          <X size={15} />
        </button>
      )}
    </form>
  );
}
