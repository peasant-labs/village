import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { parse } from 'yaml';
import { waitFor } from '@testing-library/react';
import { expect, it } from 'vitest';
import { installMountedRouteTeardown, installRESTFixture, renderProductionRoute } from '@/test/mountedProductionRoute';
import { parseTranscriptRead } from '@/lib/queries/transcriptContent';

installMountedRouteTeardown();

it('counts omissions in root and retained earlier history without altering either', () => {
  const fixture = parse(readFileSync(resolve(process.cwd(), '../backend/internal/handler/testdata/content_parts_preservation.yaml'), 'utf8'));
  const cases = fixture.cases.filter((entry: { name: string }) => entry.name === 'root_and_earlier_ordered_parts');
  expect(cases).toHaveLength(1);
  const result = parseTranscriptRead(cases[0].content, 'codex');
  expect(result.omittedImages).toBe(2);
  expect(result.content).toEqual(JSON.parse(cases[0].content).sessionDetail);
});

it('reports published omissions without requesting image bytes', async () => {
  const fixture = parse(readFileSync(resolve(process.cwd(), 'src/testdata/published-image-reader.yaml'), 'utf8'));
  const cases = fixture.cases.filter((entry: { name: string }) => entry.name === 'two-published-images');
  expect(cases).toHaveLength(1);
  const envelope = JSON.parse(cases[0].content);
  const detail = envelope.sessionDetail;
  const parsed = parseTranscriptRead(cases[0].content, 'codex');
  expect(parsed.omittedImages).toBe(2);
  expect(parsed.published).toBe(true);
  expect(parsed.content).toEqual(detail);
  const id = 'omitted-images';
  const fetchMock = installRESTFixture(id, {
    transcript: { id, local_id: detail.id, model_provider: 'codex', visibility: 'public', title: 'images', description: null, project_name: 'village' },
    owner: { id: 'fixture-owner' }, enriched_shares: [],
  }, envelope, 'omitted images');
  await renderProductionRoute(id);
  await waitFor(() => expect(document.body.textContent).toContain('2 images omitted'));
  const cards = document.querySelectorAll('[data-image-omitted="not_published"]');
  expect(cards).toHaveLength(2);
  cards.forEach((card) => expect(card.textContent).toContain('image not published'));
  const requested = fetchMock.mock.calls.map(([input]) => String(input));
  expect(requested.some((url) => /\/api\/v1\/attachments\/|[a-f0-9]{64}/.test(url))).toBe(false);
  expect(document.querySelector('img[src*="attachment"]')).toBeNull();
});
