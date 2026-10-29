import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import { waitFor } from '@testing-library/react';
import { installHomeRouteREST, installHomeRouteTeardown, renderHeaderAt } from '@/test/mountedHomeRoute';
import YAML from 'yaml';

interface GeometryFixture {
  variable: string;
  value: string;
  consumers: Array<{ path: string; token: string }>;
  forbidden: string[];
  boundedShell: {
    pathname: string;
    selector: string;
    mainSelector: string;
    rules: Array<{ selector: string; properties: Record<string, string> }>;
  };
}

const fixture = YAML.parse(
  readFileSync(resolve(process.cwd(), 'src/components/testdata/app-shell-geometry.yaml'), 'utf8'),
) as GeometryFixture;

installHomeRouteTeardown();

describe('app shell geometry', () => {
  it('uses one shell header height for the public bar, the signed-in shell, and the transcript bound', () => {
    const globals = readFileSync(resolve(process.cwd(), 'src/app/globals.css'), 'utf8');
    expect(globals).toContain(`${fixture.variable}: ${fixture.value}`);

    for (const consumer of fixture.consumers) {
      const source = readFileSync(resolve(process.cwd(), consumer.path), 'utf8');
      expect(source, `${consumer.path} must consume the canonical shell height`).toContain(consumer.token);
      for (const forbidden of fixture.forbidden) {
        expect(source, `${consumer.path} must not restore a fixed header-height copy`).not.toContain(forbidden);
      }
    }
  });
});


it('mounts transcript pages in the canonical shell with bounded overflow', async () => {
  installHomeRouteREST({ viewerUsername: 'geometry-owner', transcripts: [] });
  await renderHeaderAt(fixture.boundedShell.pathname);
  const shell = await waitFor(() => {
    const element = document.querySelector<HTMLElement>(fixture.boundedShell.selector);
    expect(element).not.toBeNull();
    return element!;
  });
  const main = shell.querySelector(fixture.boundedShell.mainSelector);
  expect(main?.tagName).toBe('MAIN');

  // jsdom cannot measure viewport overflow. Apply just these rules from the
  // shipped canonical CSS to verify the mounted boundary's actual containment
  // declarations; browser journeys own scrollWidth/viewport measurements.
  const css = readFileSync(fileURLToPath(import.meta.resolve('@peasant-labs/fairtrade/components.css')), 'utf8');
  const style = document.createElement('style');
  style.textContent = fixture.boundedShell.rules.map(({ selector }) => {
    const start = css.indexOf(`${selector}{`);
    expect(start, `canonical CSS must style ${selector}`).toBeGreaterThanOrEqual(0);
    return css.slice(start, css.indexOf('}', start) + 1);
  }).join('\n');
  document.head.append(style);
  try {
    for (const rule of fixture.boundedShell.rules) {
      const element = document.querySelector(rule.selector);
      expect(element).not.toBeNull();
      const computed = getComputedStyle(element!);
      for (const [property, expected] of Object.entries(rule.properties)) {
        expect(computed.getPropertyValue(property), `${rule.selector} ${property}`).toBe(expected);
      }
    }
  } finally { style.remove(); }
});
