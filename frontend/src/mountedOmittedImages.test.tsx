import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { parse } from 'yaml';
import { waitFor } from '@testing-library/react';
import { expect, it } from 'vitest';
import { installMountedRouteTeardown, installRESTFixture, renderProductionRoute } from '@/test/mountedProductionRoute';
import { parseTranscriptRead } from '@/lib/queries/transcriptContent';

installMountedRouteTeardown();

it('reports published omissions without requesting image bytes', async () => {
  const fixture = parse(readFileSync(resolve(process.cwd(), '../backend/internal/handler/testdata/content_parts_preservation.yaml'), 'utf8'));
  const cases = fixture.cases.filter((entry: { name: string }) => entry.name === 'root_and_earlier_ordered_parts');
  expect(cases).toHaveLength(1);
  const envelope = JSON.parse(cases[0].content);
  const detail = envelope.sessionDetail;
  const projected = parseTranscriptRead(cases[0].content, 'codex');
  expect(projected.omittedImages).toBe(2);
  const content = projected.content as typeof detail;
  expect(content.turns[0].contentParts.map((part: { text: string }) => part.text)).toEqual(['before', 'image omitted from publication', 'after']);
  expect(content.earlierHistory[0].turns[0].contentParts.map((part: { text: string }) => part.text)).toEqual(['image omitted from publication', 'old']);
  const id = 'omitted-images';
  const fetchMock = installRESTFixture(id, {
    transcript: { id, local_id: detail.id, model_provider: 'codex', visibility: 'public', title: 'images', description: null, project_name: 'village' },
    owner: { id: 'fixture-owner' }, enriched_shares: [],
  }, envelope, 'omitted images');
  await renderProductionRoute(id);
  await waitFor(() => expect(document.body.textContent).toContain('2 images omitted'));
  expect(document.body.textContent).toContain('image omitted from publication');
  const requested = fetchMock.mock.calls.map(([input]) => String(input));
  expect(requested.some((url) => /attachments|[a-f0-9]{64}/.test(url))).toBe(false);
  expect(document.querySelector('img[src*="attachment"]')).toBeNull();
});
