'use client';

// prior-version, deprecation candidate (V-SHELL): a bespoke before/after
// redaction diff view. No remaining import sites (not currently reachable from any
// route). fairtrade ships its own Redaction/DiffView primitives (src/ui/Redaction.jsx,
// src/ui/DiffView.jsx) that aren't yet adopted here -- soft-retained per the SOFT-RETIRE
// POLICY (soft-retire-not-delete policy) rather than deleted, as a candidate for that
// future adoption rather than a reimplementation from scratch.

import {
  forwardRef,
  useCallback,
  useImperativeHandle,
  useState,
  type ElementType,
} from 'react';
import { Button, Card, Chip, Tooltip } from '@/lib/ft-ui';
import { cn } from '@/lib/utils';
import {
  ShieldAlertIcon,
  AlertTriangleIcon,
  EyeOffIcon,
  EyeIcon,
  KeyRound,
  UserRound,
  Folder,
  Building2,
  CircleX,
} from 'lucide-react';

// Chrome consumes the fairtrade design system via the typed `@/lib/ft-ui` barrel
// (Card / Chip / Tooltip / Button), replacing the local Radix-backed `primitives` shim.
// Per the design system, status never rides on color alone — every redaction category and
// state pairs a word with a lucide icon and a semantic tone (no aqua/soft-fill badges).

// ---------------------------------------------------------------------------
// Redaction types — ported alongside the view (village's `types/messages.ts`
// does not define these; the redaction flow is otherwise CLI-driven).
// ---------------------------------------------------------------------------

export type RedactionCategory = 'CREDENTIAL' | 'PII' | 'PATH' | 'INTERNAL';

export interface Redaction {
  id: string;
  category: RedactionCategory;
  confidence: number; // 0-100
  lineNumber: number;
  contextBefore: string[]; // 2-3 lines of surrounding context before
  contextAfter: string[]; // 2-3 lines of surrounding context after
  originalText: string;
  redactedReplacement: string;
  description: string; // e.g. "AWS access key detected"
  status: 'pending' | 'accepted' | 'rejected';
}

// ---------------------------------------------------------------------------
// Category styling
// ---------------------------------------------------------------------------

const CATEGORY_CONFIG: Record<
  RedactionCategory,
  { label: string; tone?: 'err' | 'warn'; icon: ElementType }
> = {
  // Dangerous category: secrets/credentials warrant the danger (err) tone + a key glyph.
  CREDENTIAL: { label: 'Credential', tone: 'err', icon: KeyRound },
  // Caution category: PII warrants the warning tone + a person glyph.
  PII: { label: 'PII', tone: 'warn', icon: UserRound },
  // Taxonomy labels — neutral tone, glyph carries the meaning.
  PATH: { label: 'Path', icon: Folder },
  INTERNAL: { label: 'Internal', icon: Building2 },
};

/** One short line per category — what kind of content it covers. */
const CATEGORY_EXPLANATION: Record<RedactionCategory, string> = {
  CREDENTIAL: 'A secret, token, key, or password.',
  PII: 'Personally identifiable information, such as a name or email.',
  PATH: 'A local filesystem path that may reveal machine or user details.',
  INTERNAL: 'An internal hostname, URL, or identifier private to your org.',
};

// ---------------------------------------------------------------------------
// Confidence indicator
// ---------------------------------------------------------------------------

function ConfidenceBadge({ confidence }: { confidence: number }) {
  const isLow = confidence < 70;
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 px-2 py-0.5 text-xs font-mono font-medium border border-rule tabular-nums',
        isLow
          ? 'bg-warning-soft text-warning'
          : 'bg-surface-hover text-ink-3',
      )}
    >
      {isLow && <AlertTriangleIcon className="size-2.5" />}
      {confidence}%
    </span>
  );
}

// ---------------------------------------------------------------------------
// Context code block (line numbers + diff highlight)
// ---------------------------------------------------------------------------

function ContextBlock({ redaction }: { redaction: Redaction }) {
  const startLine = redaction.lineNumber - redaction.contextBefore.length;

  return (
    <div className="border border-rule bg-code-bg font-mono text-xs overflow-x-auto">
      {redaction.contextBefore.map((line, i) => (
        <div key={`before-${i}`} className="flex">
          <span className="inline-block w-12 shrink-0 select-none px-2 py-0.5 text-right text-ink-4 tabular-nums border-r border-rule">
            {startLine + i}
          </span>
          <span className="py-0.5 px-2 whitespace-pre-wrap break-all text-ink-2">
            {line}
          </span>
        </div>
      ))}

      {/* Deleted / redacted-out line */}
      <div className="flex bg-diff-del">
        <span className="inline-block w-12 shrink-0 select-none px-2 py-0.5 text-right text-diff-del-gutter font-medium tabular-nums border-r border-diff-del-accent">
          {redaction.lineNumber}
        </span>
        <span className="py-0.5 px-2 whitespace-pre-wrap break-all">
          <span className="line-through v2-diff-del px-0.5">
            {redaction.originalText}
          </span>
        </span>
      </div>

      {/* Added / replacement line */}
      <div className="flex bg-diff-add">
        <span className="inline-block w-12 shrink-0 select-none px-2 py-0.5 text-right text-diff-add-gutter font-medium tabular-nums border-r border-diff-add-accent">
          {redaction.lineNumber}
        </span>
        <span className="py-0.5 px-2 whitespace-pre-wrap break-all">
          <span className="v2-diff-add px-0.5 font-medium">
            {redaction.redactedReplacement}
          </span>
        </span>
      </div>

      {redaction.contextAfter.map((line, i) => (
        <div key={`after-${i}`} className="flex">
          <span className="inline-block w-12 shrink-0 select-none px-2 py-0.5 text-right text-ink-4 tabular-nums border-r border-rule">
            {redaction.lineNumber + 1 + i}
          </span>
          <span className="py-0.5 px-2 whitespace-pre-wrap break-all text-ink-2">
            {line}
          </span>
        </div>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Single redaction card — redacted by default, single Opt out toggle.
// ---------------------------------------------------------------------------

function RedactionCard({
  redaction,
  optedOut,
  onToggleOptOut,
}: {
  redaction: Redaction;
  optedOut: boolean;
  onToggleOptOut: (id: string, optedOut: boolean) => void;
}) {
  const cat = CATEGORY_CONFIG[redaction.category];
  const isLowConfidence = redaction.confidence < 70;

  return (
    <div
      className={cn(
        'border border-rule p-4 space-y-3 transition-colors',
        // Opt-out is sticky and visible: the card dims + picks up a danger
        // border to flag "this will leave your machine as-is".
        optedOut && 'border-danger/50 bg-danger-soft/30',
        // Low-confidence items get an emphatic border (warning) only when
        // still being redacted — once opted out, the danger border wins.
        !optedOut && isLowConfidence && 'border-warning',
      )}
    >
      {/* Header */}
      <div className="flex items-center gap-2 flex-wrap">
        <Tooltip
          content={CATEGORY_EXPLANATION[redaction.category]}
          id={`redaction-cat-${redaction.id}`}
        >
          <Chip tone={cat.tone} icon={cat.icon} className="cursor-help">
            {cat.label}
          </Chip>
        </Tooltip>
        <ConfidenceBadge confidence={redaction.confidence} />
        {optedOut && (
          <Tooltip
            content="This match will NOT be redacted — it stays in the shared transcript."
            id={`redaction-unredacted-${redaction.id}`}
          >
            <Chip tone="err" icon={CircleX} className="cursor-help">
              Un-redacted
            </Chip>
          </Tooltip>
        )}
        <span className="text-xs text-ink-3 ml-auto font-mono tabular-nums">
          line {redaction.lineNumber}
        </span>
      </div>

      {/* Description */}
      <p className="text-sm text-ink">{redaction.description}</p>

      {/* Diff context block */}
      <ContextBlock redaction={redaction} />

      {/* Replacement summary */}
      <div className="text-xs text-ink-3">
        <span className="line-through text-diff-del-text">
          {redaction.originalText.length > 50
            ? redaction.originalText.slice(0, 47) + '...'
            : redaction.originalText}
        </span>
        <span className="mx-2">&rarr;</span>
        <span className="text-diff-add-text font-medium">
          {redaction.redactedReplacement}
        </span>
      </div>

      {/* Single per-item action: Opt out (un-redact) ↔ Redact.
          Default state is redacted; opting out lets the original text leave
          the machine as-is. */}
      <div className="flex items-center gap-2 pt-1">
        {optedOut ? (
          <>
            <Button
              size="sm"
              variant="secondary"
              icon={EyeOffIcon}
              onClick={() => onToggleOptOut(redaction.id, false)}
            >
              Redact
            </Button>
            <span className="text-xs text-danger flex items-center gap-1">
              <AlertTriangleIcon className="size-3" />
              This content will be exposed
            </span>
          </>
        ) : (
          // Opting out exposes the original content — a genuinely consequential
          // action, so it carries the danger affordance (icon + word + tone).
          <Button
            size="sm"
            variant="danger"
            icon={EyeIcon}
            onClick={() => onToggleOptOut(redaction.id, true)}
          >
            Opt out
          </Button>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Empty state
// ---------------------------------------------------------------------------

function RedactionEmptyState() {
  return (
    <Card>
      <div className="p-4">
        <div className="flex flex-col items-center justify-center py-12 text-center">
          <div className="bg-surface-hover p-3 mb-4 border border-rule">
            <ShieldAlertIcon className="size-6 text-ink-3" />
          </div>
          <h3 className="text-sm font-medium text-ink">No redactions detected</h3>
          <p className="mt-1 text-xs text-ink-3 max-w-sm">
            No sensitive content was auto-detected in this transcript.
            The session is safe to share as-is.
          </p>
        </div>
      </div>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Main component — safe-by-default: every flagged item is redacted unless the
// user opts it out per-item. No bulk Accept/Reject, no summary tallies.
// ---------------------------------------------------------------------------

/**
 * Imperative handle kept for prop-shape compatibility with existing callers.
 * Accept/Reject is gone; this is now a no-op so the parent does not have to
 * special-case `ref?.current` access.
 */
export interface RedactionDiffViewHandle {
  acceptAll: () => void;
}

interface RedactionDiffViewProps {
  initialRedactions: Redaction[];
  /**
   * Called whenever the number of opted-out vs. still-redacted items changes.
   * The shape is preserved for callers: `pending` = still-redacted (the safe
   * default), `resolved` = opted-out (un-redacted by the user).
   */
  onCountsChange?: (pending: number, resolved: number, total: number) => void;
}

export const RedactionDiffView = forwardRef<
  RedactionDiffViewHandle,
  RedactionDiffViewProps
>(function RedactionDiffView({ initialRedactions, onCountsChange }, ref) {
  // The component's state is now a single boolean per item: opted-out (true)
  // means "do not redact this; publish as-is". Default is false (redacted).
  const [optedOut, setOptedOut] = useState<Set<string>>(() => new Set());

  const total = initialRedactions.length;

  const handleToggleOptOut = useCallback(
    (id: string, next: boolean) => {
      setOptedOut((prev) => {
        const updated = new Set(prev);
        if (next) updated.add(id);
        else updated.delete(id);
        if (onCountsChange) {
          const resolved = updated.size;
          onCountsChange(total - resolved, resolved, total);
        }
        return updated;
      });
    },
    [onCountsChange, total],
  );

  // No-op handle: bulk Accept/Reject has been removed.
  useImperativeHandle(ref, () => ({ acceptAll: () => {} }), []);

  if (initialRedactions.length === 0) {
    return <RedactionEmptyState />;
  }

  return (
    <Card>
      <div className="p-4">
        <div className="space-y-4">
          {initialRedactions.map((r) => (
            <RedactionCard
              key={r.id}
              redaction={r}
              optedOut={optedOut.has(r.id)}
              onToggleOptOut={handleToggleOptOut}
            />
          ))}
        </div>
      </div>
    </Card>
  );
});
