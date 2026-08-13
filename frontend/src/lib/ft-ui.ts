/**
 * Canonical typed barrel for the fairtrade `/ui` components consumed by this app.
 *
 * fairtrade's `/ui` components forward unrecognised props onto their underlying
 * DOM node via `{...rest}` at runtime, but their published `.d.ts` files declare
 * only the design-system props — so native DOM attributes (`onClick`, `type`,
 * `value`, `aria-*`, …) are invisible to TypeScript at the call site. The few
 * components that need those native attributes are re-exported here intersected
 * with the DOM surface they actually forward; the rest are re-exported as-is.
 *
 * Import fairtrade `/ui` through this module (`@/lib/ft-ui`), never directly
 * from `@peasant-labs/fairtrade/ui`, and extend it as more components are
 * adopted. (A future fairtrade `.d.ts` fix removes the need for the widenings.)
 */
import type {
  ButtonHTMLAttributes,
  ChangeEvent,
  ComponentProps,
  ComponentType,
  CSSProperties,
  InputHTMLAttributes,
  ReactNode,
  TextareaHTMLAttributes,
} from "react";
import {
  Button as FtButton,
  Checkbox as FtCheckbox,
  Input as FtInput,
  Radio as FtRadio,
  RadioGroup as FtRadioGroup,
  RailSection as FtRailSection,
  RailShell as FtRailShell,
  Select as FtSelect,
  Switch as FtSwitch,
  TeachingEmptyState as FtTeachingEmptyState,
  Textarea as FtTextarea,
} from "@peasant-labs/fairtrade/ui";

/**
 * fairtrade Button. Renders a real `<button>` (default) and forwards native
 * button attributes (`onClick`, `type`, `form`, `aria-*`, …) via `{...rest}`;
 * widen its published type to expose them.
 */
export const Button = FtButton as ComponentType<
  ComponentProps<typeof FtButton> & ButtonHTMLAttributes<HTMLButtonElement>
>;

/**
 * fairtrade Input. Renders a real `<input>` and forwards native input
 * attributes (`value`, `onChange`, `name`, `placeholder`, …) via `{...rest}`.
 */
export const Input = FtInput as ComponentType<
  ComponentProps<typeof FtInput> & InputHTMLAttributes<HTMLInputElement>
>;

/**
 * fairtrade Textarea. Renders a real `<textarea>` and forwards native textarea
 * attributes (`value`, `onChange`, `placeholder`, `rows`, …) via `{...rest}`.
 */
export const Textarea = FtTextarea as ComponentType<
  ComponentProps<typeof FtTextarea> & TextareaHTMLAttributes<HTMLTextAreaElement>
>;

/**
 * fairtrade Select. Renders a real `<select>` (its declared type already covers
 * `value`/`onChange`/`<option>` children); widen it to also expose the native
 * `aria-label` it forwards via `{...rest}`.
 */
export const Select = FtSelect as ComponentType<
  ComponentProps<typeof FtSelect> & { "aria-label"?: string }
>;

/**
 * Components whose published types are already sufficient at this app's
 * call-sites — re-exported as shipped:
 *  - `Pagination` — windowed numbered pager.
 *  - `ProviderTag` / `ProviderName` — brand-marked harness identity.
 *  - `Tag` / `Chip` — neutral / semantic pills.
 *  - `Card` / `Tooltip` / `DataTable` — layout & data chrome.
 *  - `VisibilityEye` — transcript visibility glyph + tooltip.
 */
export {
  Card,
  Chip,
  DataTable,
  Pagination,
  ProviderName,
  ProviderTag,
  Tag,
  Tooltip,
  VisibilityEye,
} from "@peasant-labs/fairtrade/ui";

// ── Layout shell ──────────────────────────────────────────────────────────────

/**
 * RailShell — two-column canvas + sticky-rail layout (desktop: 320 px hairline
 *   rail card alongside a scrollable main column; below 880 px: rail collapses
 *   into a fixed bottom-sheet). Compose the `rail` prop with <RailSection>
 *   children.
 * RailSection — one titled hairline section inside a rail. When `collapsible`,
 *   the header becomes a button that toggles the body; the chevron rotates open.
 * SplitRail   — dual-rail variant (outline-left / filters-right), each column
 *   independently collapsible. Use as the `rail` prop of <RailShell> or standalone.
 *
 * Note: RailShell and RailSection are widened because the published `.d.ts`
 * declares optional props (`toolbar`, `sheetMeta`, `title`, `icon`, `meta`) as
 * required `any` in the function parameters — the correct `RailShellProps` /
 * `RailSectionProps` typedefs exist but are not re-exported from the index.
 */
export const RailShell = FtRailShell as ComponentType<{
  toolbar?: ReactNode;
  children: ReactNode;
  rail: ReactNode;
  railSide?: "left" | "right";
  sheetTitle?: string;
  sheetMeta?: ReactNode;
  className?: string;
}>;

export const RailSection = FtRailSection as ComponentType<{
  title?: ReactNode;
  icon?: ComponentType<{ className?: string; size?: number }>;
  meta?: ReactNode;
  children: ReactNode;
  collapsible?: boolean;
  defaultOpen?: boolean;
  className?: string;
}>;

export { SplitRail } from "@peasant-labs/fairtrade/ui";

/**
 * FacetRail — order / provider / topic filter rail for the Explore view.
 * The `.d.ts` already declares `...rest` as `[x: string]: any`; no widening needed.
 */
export { FacetRail } from "@peasant-labs/fairtrade/ui";

// ── KPI / governance tiles ────────────────────────────────────────────────────

/**
 * StatTile   — single KPI tile: mono eyebrow label + large tabular display
 *   number + optional sub line + optional lucide icon.
 * StatGrid   — responsive grid of StatTiles (1–4 columns via container query so
 *   tile widths stay equal wherever the grid is dropped).
 * GovTile    — governance fact: mono eyebrow label + optional icon + value that
 *   may carry one earth-tone accent. Tones: 'amber' | 'teal' | 'olive' | 'clay'
 *   | 'mauve'.
 * ProviderBars — horizontal share distribution (monochrome bars + tabular %).
 *   Bars survive greyscale and AT — length + label + written % encode the data.
 */
export { StatTile, StatGrid, GovTile, ProviderBars } from "@peasant-labs/fairtrade/ui";

// ── Step wizard ───────────────────────────────────────────────────────────────

/**
 * StepWizard    — uncontrolled numbered wizard: progress rail + per-step body
 *   slot + sticky footer (continue / back / submit on the last step). Step bodies
 *   via `children` array (index-aligned to `steps`) or a `renderStep` render-prop
 *   receiving { step, index, isLast, setValid }.
 * StepIndicator — the pure controlled rail alone: numbered square markers joined
 *   by connector lines. current=amber; completed=olive+check (clickable back);
 *   future/unreachable=hairline.
 */
export { StepWizard, StepIndicator } from "@peasant-labs/fairtrade/ui";

// ── Consent / governance dialogs ──────────────────────────────────────────────

/**
 * ConsentDialog  — scrim + bordered modal for governance decisions. Gates the
 *   primary action behind an "i understand and consent" checkbox when
 *   `requireConsent` (default true). Tone: 'primary' | 'danger'. Esc / scrim
 *   click / close all cancel; focus is trapped while open.
 * ConsentSummary — reusable axes grid (icon chip + mono key + value + optional
 *   scope note). Each row carries a tone: 'reveal' (amber) | 'open' (teal) |
 *   'restricted' (clay).
 */
export { ConsentDialog, ConsentSummary } from "@peasant-labs/fairtrade/ui";

// ── Moderation ────────────────────────────────────────────────────────────────

/**
 * ModerationQueue — panel listing pending member-request or share items. Acting
 *   on a row resolves it optimistically in place (buttons swap for an
 *   approved/rejected pill; row struck + dimmed, never removed) so the reviewer
 *   keeps an audit trail.
 * ApprovalBar     — sticky top bar for reviewing a single pending item. Collapses
 *   to a one-line resolved acknowledgement (icon + word) after action rather than
 *   disappearing.
 */
export { ModerationQueue, ApprovalBar } from "@peasant-labs/fairtrade/ui";

// ── Commit graph ──────────────────────────────────────────────────────────────

/**
 * CommitGraph — lane-gutter + selectable row list for commit history (newest
 *   first). Lane 0 = main line; higher lanes = branches. Session commits carry a
 *   sparkle glyph; merge commits get a chip; branch tips get a small tip
 *   affordance. `selectedId` receives the scarce amber treatment.
 */
export { CommitGraph } from "@peasant-labs/fairtrade/ui";

// ── Connection / data state ───────────────────────────────────────────────────

/**
 * DataState          — discriminates between the states a data view can be in so
 *   a dropped connection never reads as "empty". Precedence: loading → skeleton;
 *   disconnected|error → lost-connection panel + retry; empty → `emptyState`
 *   slot; else → children (the real content).
 * ConnectionPill     — small glanceable connection indicator (live / connecting /
 *   disconnected). State encoded in icon + word, never color alone.
 * TeachingEmptyState — empty state that teaches the mechanism: icon + title +
 *   guidance prose + copyable `$ command` chip (with copy button) + privacy line.
 */
export { DataState, ConnectionPill } from "@peasant-labs/fairtrade/ui";

/**
 * fairtrade TeachingEmptyState — widened to accept `style` so callers can embed
 * it inside a bordered container and clear its own border/background.
 */
export const TeachingEmptyState = FtTeachingEmptyState as ComponentType<
  ComponentProps<typeof FtTeachingEmptyState> & { style?: CSSProperties }
>;

// ── Sign-in / handle onboarding ───────────────────────────────────────────────

/**
 * SignInProviders — multi-provider OAuth split button. providers[0] is the
 *   primary action ("continue with <label>"); the rest live behind a chevron that
 *   opens a keyboard-operable menu. Fires onSignIn(providerId).
 * HandleClaim    — post-OAuth handle-claim card: @-prefixed input with live
 *   validation states (idle / checking / available / taken / invalid), suggestion
 *   chips derived from suggestedFrom or explicit suggestions[], and a "claim
 *   handle" button disabled until the handle validates as available.
 * OnboardingCard — SignInProviders + HandleClaim composed as one front-door
 *   specimen.
 */
export { SignInProviders, HandleClaim, OnboardingCard } from "@peasant-labs/fairtrade/ui";

// ── CLI onboarding ────────────────────────────────────────────────────────────

/**
 * CliSteps       — numbered step list: square amber-tinted number marker + title
 *   + optional body prose + optional CommandBlock, with connector lines between
 *   markers.
 * CommandBlock   — `$ command` mono block with a copy button that flips to an
 *   olive check + the word "copied" for ~1.5 s. Only the command itself (not the
 *   `$`) is copied.
 * GettingStarted — dismissible card wrapping CliSteps: header (title + close
 *   button) over the step list. Dismissed state optionally persists to localStorage
 *   via `storageKey`.
 */
export { CliSteps, CommandBlock, GettingStarted } from "@peasant-labs/fairtrade/ui";

// ── Form controls (checkbox / radio) ─────────────────────────────────────────

/**
 * Checkbox — `<label class="check">` wrapping a styled `<input type="checkbox"
 *   class="check-box">`. Supports controlled (`checked` + `onChange(checked,
 *   event)`) and uncontrolled (`defaultChecked`) use. `children` become the
 *   label text (rendered as-is, never lowercased).
 * Radio    — `<label class="is-radio">` wrapping `<input type="radio"
 *   class="is-radio-dot">`. Always render in groups of ≥2 sharing `name` so a
 *   selection can be changed; a lone radio cannot be deselected (a11y
 *   anti-pattern). For a single binary choice use Checkbox or Switch.
 * RadioGroup — `<div role="radiogroup">` of Radio rows. Handles controlled
 *   (`value` + `onChange(value, event)`) and uncontrolled (`defaultValue`) use.
 *   `ariaLabel` is required for an accessible group name.
 *
 * Note: these are widened because the published `.d.ts` uses `& any` in the
 * function parameter intersection, which collapses all prop types to `any` and
 * prevents TypeScript from inferring callback parameter types at call sites.
 */
export const Checkbox = FtCheckbox as ComponentType<{
  checked?: boolean;
  defaultChecked?: boolean;
  onChange?: (checked: boolean, event: ChangeEvent<HTMLInputElement>) => void;
  disabled?: boolean;
  children?: ReactNode;
}>;

export const Radio = FtRadio as ComponentType<{
  name: string;
  value?: string;
  checked?: boolean;
  defaultChecked?: boolean;
  onChange?: (event: ChangeEvent<HTMLInputElement>) => void;
  disabled?: boolean;
  children?: ReactNode;
}>;

export const RadioGroup = FtRadioGroup as ComponentType<{
  name: string;
  value?: string;
  defaultValue?: string;
  onChange?: (value: string, event: ChangeEvent<HTMLInputElement>) => void;
  options: Array<{ value: string; label: ReactNode; disabled?: boolean }>;
  ariaLabel: string;
}>;

/**
 * fairtrade Switch — a real accessible toggle (`role="switch"`), for a single on/off setting.
 * Prefer this over Checkbox for a setting that takes effect immediately (no form submit), per
 * the fairtrade convention documented on Checkbox above. Its published type is already precise
 * (no `& any` widening needed) — re-exported as shipped.
 */
export const Switch = FtSwitch;

// ── Role roster + inline danger ───────────────────────────────────────────────

/**
 * RoleRoster   — member role roster: avatar/handle + name + org chip + role
 *   select, one row per member. Owner rows are locked (disabled select + lock
 *   glyph, no remove). `onRole(member, role)` fires on role change;
 *   `onRemove(member)` fires when an inline remove confirm is accepted.
 * ConfirmInline — inline destructive-confirm: a trigger button that swaps in
 *   place to "{label}? [yes] [cancel]" — never a modal. `onConfirm` can return
 *   a Promise; `busy` disables yes + shows "{verb}…" while pending.
 * DangerZone   — clay-bordered section wrapping destructive actions. Fronted by
 *   a warning glyph + the literal word "WARNING" so danger is never color-only.
 */
export { RoleRoster, ConfirmInline, DangerZone } from "@peasant-labs/fairtrade/ui";

// ── Navigation / identity (V-SHELL) ───────────────────────────────────────────

/**
 * GraphSectionNav — route-agnostic subnav renderer (the primitive behind the
 *   fairtrade demo's `.iu-subnav`, already reused by peasant's own TopNavbar and
 *   reused here too). Pass real sections + a `hrefFor`/`LinkComponent` pair to get
 *   real next/link navigation instead of the demo's internal view-switcher.
 * Avatar / AvatarGroup — identity tile: photo with a styled initials fallback
 *   (never a bare bg-mark/text-mark-fg box). AvatarGroup stacks several with
 *   overlap + an optional "+N" overflow tile.
 */
export { GraphSectionNav, Avatar, AvatarGroup } from "@peasant-labs/fairtrade/ui";
