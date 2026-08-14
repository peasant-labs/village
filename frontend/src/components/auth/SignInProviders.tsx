"use client";

// Deprecated compatibility component superseded by the lifted design-system
// SignInProviders (@peasant-labs/fairtrade/ui, src/ui/SignIn.jsx), which Navbar.tsx now
// composes directly. This hand-rolled duplicate used the removed bg-mark/text-mark-fg token
// pair, which renders white-on-white in dark theme. The design-system version
// uses the canonical amber fill. This component remains dormant for compatibility
// and has no app import sites.

import { useEffect, useRef, useState, type ReactNode } from "react";
import { ChevronDown } from "lucide-react";
import { API_URL_BASE } from "@/lib/api";
import { cn } from "@/lib/utils";

type Provider = {
  id: "github" | "gitlab" | "huggingface" | "codeberg" | "sourcehut";
  label: string;
  icon: ReactNode;
};

const ICON_PROPS = {
  width: 14,
  height: 14,
  viewBox: "0 0 24 24",
  fill: "currentColor",
  "aria-hidden": true,
} as const;

const PROVIDERS: Provider[] = [
  {
    id: "github",
    label: "Sign in with GitHub",
    icon: (
      <svg {...ICON_PROPS}>
        <path d="M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.807 1.305 3.492.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.4 3-.405 1.02.005 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12" />
      </svg>
    ),
  },
  {
    id: "gitlab",
    label: "Sign in with GitLab",
    icon: (
      <svg {...ICON_PROPS}>
        <path d="m23.6004 9.5927-.0337-.0862L20.3.9814a.851.851 0 0 0-.3362-.405.8748.8748 0 0 0-.9997.0539.8748.8748 0 0 0-.29.4399l-2.2055 6.748H7.5375l-2.2057-6.748a.8573.8573 0 0 0-.29-.4412.8748.8748 0 0 0-.9997-.0537.8585.8585 0 0 0-.3362.4049L.4332 9.5015l-.0325.0862a6.0657 6.0657 0 0 0 2.0119 7.0105l.0113.0087.03.0213 4.976 3.7264 2.462 1.8633 1.4995 1.1321a1.0085 1.0085 0 0 0 1.2197 0l1.4995-1.1321 2.4619-1.8633 5.006-3.7489.0125-.01a6.0682 6.0682 0 0 0 2.0094-7.003z" />
      </svg>
    ),
  },
  {
    id: "huggingface",
    label: "Sign in with Hugging Face",
    icon: (
      <svg {...ICON_PROPS} viewBox="0 0 95 88">
        <path d="M47.21 76.07c14.85 0 26.9-12.21 26.9-27.27 0-15.05-12.05-27.26-26.9-27.26-14.85 0-26.9 12.21-26.9 27.26 0 15.06 12.05 27.27 26.9 27.27z" fill="#FFD21E"/>
        <path d="M81.7 47.83c2.05-1.39 3.34-3.7 3.34-6.3 0-4.22-3.4-7.65-7.62-7.65-1.79 0-3.42.63-4.71 1.66a23.97 23.97 0 0 0-2.13-3.55c-4.7-6.83-12.5-11.3-21.37-11.3-8.87 0-16.66 4.47-21.36 11.3a23.97 23.97 0 0 0-2.13 3.55 7.6 7.6 0 0 0-4.71-1.66c-4.22 0-7.62 3.43-7.62 7.65 0 2.6 1.29 4.91 3.34 6.3a26.94 26.94 0 0 0-.42 4.74c0 14.85 12.05 26.9 26.9 26.9 14.85 0 26.9-12.05 26.9-26.9 0-1.62-.14-3.2-.42-4.74h.01z" fill="currentColor"/>
      </svg>
    ),
  },
  {
    id: "codeberg",
    label: "Sign in with Codeberg",
    icon: (
      <svg {...ICON_PROPS}>
        <path d="M11.955.49A12 12 0 0 0 0 12.49a12 12 0 0 0 .076 1.343l11.4-15.158A12 12 0 0 0 11.955.49zm.09 0a12 12 0 0 0-.474.185L23.924 13.832A12 12 0 0 0 24 12.49 12 12 0 0 0 12.045.49zM.378 15.512A12 12 0 0 0 12 23.51a12 12 0 0 0 11.622-7.998L12 6.207z"/>
      </svg>
    ),
  },
  {
    id: "sourcehut",
    label: "Sign in with SourceHut",
    icon: (
      <svg {...ICON_PROPS}>
        <path d="M12 0a12 12 0 1 0 0 24 12 12 0 0 0 0-24zm0 2.182a9.818 9.818 0 1 1 0 19.636 9.818 9.818 0 0 1 0-19.636z"/>
      </svg>
    ),
  },
];

export function SignInProviders() {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function onClickOutside(e: MouseEvent) {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    if (open) {
      document.addEventListener("mousedown", onClickOutside);
      return () => document.removeEventListener("mousedown", onClickOutside);
    }
  }, [open]);

  const start = (id: Provider["id"]) => {
    window.location.href = `${API_URL_BASE}/auth/${id}`;
  };

  const primary = PROVIDERS[0];
  const rest = PROVIDERS.slice(1);

  return (
    <div className="relative flex items-center" ref={rootRef}>
      <button
        type="button"
        onClick={() => start(primary.id)}
        className="inline-flex h-8 items-center gap-1.5 bg-mark px-3 text-xs font-medium text-mark-fg transition-colors hover:bg-mark/90 focus-mono cursor-pointer"
      >
        {primary.icon}
        {primary.label}
      </button>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-label="More sign-in providers"
        aria-expanded={open}
        aria-haspopup="menu"
        className="inline-flex h-8 w-7 items-center justify-center border-l border-mark-fg/20 bg-mark text-mark-fg transition-colors hover:bg-mark/90 focus-mono cursor-pointer"
      >
        <ChevronDown
          size={14}
          className={cn("transition-transform", open && "rotate-180")}
        />
      </button>

      {open && (
        <div
          role="menu"
          className="absolute right-0 top-full mt-2 w-56 border border-rule bg-surface py-1 z-50 shadow-sm"
        >
          {rest.map((p) => (
            <button
              key={p.id}
              type="button"
              role="menuitem"
              onClick={() => {
                setOpen(false);
                start(p.id);
              }}
              className="flex w-full items-center gap-2 px-3 py-2 text-left text-xs font-medium text-ink transition-colors hover:bg-surface-hover focus-mono cursor-pointer"
            >
              <span className="text-ink-2">{p.icon}</span>
              {p.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
