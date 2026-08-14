'use client';

// Deprecated compatibility component superseded by the design system's
// real-brand-mark ProviderTag and ProviderName components. It has no remaining
// import sites.

import { Code2 } from 'lucide-react';
import type { Provider } from '@/types/messages';
import { cn } from '@/lib/utils';

/**
 * Brand icons for the four supported providers.
 *
 * Strategy: Inline SVG paths render with `currentColor` so they inherit text
 * color and respect dark mode without hardcoded values. Claude (Anthropic),
 * Gemini (Google), and OpenCode use simple-icons-style brand glyphs.
 *
 * Codex has no widely-recognized standalone mark, so we fall back to the
 * lucide-react `Code2` glyph for consistency with the rest of the UI.
 *
 * Color: `tone="brand"` (default) renders each provider in its tokenized
 * brand color. `tone="current"` inherits `currentColor` for monochrome
 * contexts. Because every glyph draws with `currentColor`, applying a
 * `text-provider-*` class is sufficient to brand-color the mark.
 */

/** How the icon is colored. */
type ProviderIconTone = 'brand' | 'current';

/** Per-provider brand color utility. Codex and Cursor have no recognized mark —
 *  their tokens resolve to neutral tones and read near-monochrome. */
const PROVIDER_BRAND_CLASS: Record<Provider, string> = {
  'claude-code': 'text-provider-claude',
  'gemini-cli':  'text-provider-gemini',
  opencode:      'text-provider-opencode',
  codex:         'text-provider-codex',
  cursor:        'text-provider-cursor',
};

interface ProviderIconProps {
  provider: Provider;
  size?: number;
  /** `'brand'` (default) tints the mark with its provider token; `'current'`
   *  inherits `currentColor`. */
  tone?: ProviderIconTone;
  className?: string;
}

export function ProviderIcon({
  provider,
  size = 14,
  tone = 'brand',
  className,
}: ProviderIconProps) {
  const toneCls = tone === 'brand' ? PROVIDER_BRAND_CLASS[provider] : undefined;
  const common = {
    width: size,
    height: size,
    viewBox: '0 0 24 24',
    fill: 'currentColor',
    'aria-hidden': true,
    className: cn('shrink-0', toneCls, className),
  } as const;

  switch (provider) {
    case 'claude-code':
      // Anthropic / Claude mark — stylized "A" sunburst (simple-icons style).
      return (
        <svg {...common}>
          <path d="M4.709 15.955l4.72-2.647.079-.23-.079-.128H9.2l-.79-.048-2.698-.073-2.339-.097-2.266-.122-.571-.121L0 11.784l.055-.352.48-.321.686.06 1.52.103 2.278.158 1.652.097 2.449.255h.389l.055-.157-.134-.098-.103-.097-2.358-1.596-2.552-1.688-1.336-.972-.724-.491-.364-.462-.158-1.008.656-.722.881.06.225.061.893.686 1.908 1.476 2.491 1.833.365.304.145-.103.019-.073-.164-.274-1.355-2.446-1.446-2.49-.644-1.032-.17-.619a2.97 2.97 0 01-.104-.729L6.283.134 6.696 0l.996.134.42.364.62 1.414 1.002 2.229 1.555 3.03.456.898.243.832.091.255h.158V9.01l.128-1.706.237-2.095.23-2.695.08-.76.376-.91.747-.492.584.28.48.685-.067.444-.286 1.851-.559 2.903-.364 1.942h.212l.243-.242.985-1.306 1.652-2.064.73-.82.85-.904.547-.431h1.033l.76 1.129-.34 1.166-1.064 1.347-.881 1.142-1.264 1.7-.79 1.36.073.11.188-.02 2.856-.606 1.543-.28 1.841-.315.833.388.091.395-.328.807-1.969.486-2.309.462-3.439.813-.042.03.049.061 1.549.146.662.036h1.622l3.02.225.79.522.474.638-.079.485-1.215.62-1.64-.389-3.829-.91-1.312-.329h-.182v.11l1.093 1.068 2.006 1.81 2.509 2.33.127.578-.322.455-.34-.049-2.205-1.657-.851-.747-1.926-1.62h-.128v.17l.444.649 2.345 3.521.122 1.08-.17.353-.608.213-.668-.122-1.374-1.925-1.415-2.167-1.143-1.943-.14.08-.674 7.254-.316.37-.729.28-.607-.461-.322-.747.322-1.476.389-1.924.315-1.53.286-1.9.17-.632-.012-.042-.14.018-1.434 1.967-2.18 2.945-1.726 1.845-.414.164-.717-.37.067-.662.401-.589 2.388-3.036 1.44-1.882.93-1.086-.006-.158h-.055L4.132 18.56l-1.13.146-.487-.456.061-.746.231-.243 1.908-1.312-.006.006z" />
        </svg>
      );

    case 'gemini-cli':
      // Google Gemini mark — four-point star (simple-icons style).
      return (
        <svg {...common}>
          <path d="M24 12c-6.626 0-12 5.373-12 12-.001-6.628-5.373-12-12-12 6.627 0 12-5.373 12-12 0 6.627 5.373 12 12 12z" />
        </svg>
      );

    case 'opencode':
      // OpenCode — terminal-style chevron + underscore (approximation; no
      // official simple-icons entry exists for OpenCode).
      return (
        <svg
          width={size}
          height={size}
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2.5"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden
          className={cn('shrink-0', toneCls, className)}
        >
          <polyline points="4 7 9 12 4 17" />
          <line x1="12" y1="19" x2="20" y2="19" />
        </svg>
      );

    case 'codex':
    default:
      // Codex has no widely-recognized standalone brand mark — fall back to a
      // lucide glyph so the column still displays an icon.
      return (
        <Code2 size={size} aria-hidden className={cn('shrink-0', toneCls, className)} />
      );
  }
}
