'use client';

import type { Provider } from '@/types/messages';

export const PROVIDER_LABEL: Record<Provider, string> = {
  'claude-code': 'Claude Code',
  'gemini-cli': 'Gemini CLI',
  codex: 'Codex',
  opencode: 'OpenCode',
  cursor: 'Cursor',
};
