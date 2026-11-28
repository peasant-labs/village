import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { it, expect } from 'vitest'
import YAML from 'yaml'
const here = dirname(fileURLToPath(import.meta.url))
const directory = join(here, '../../../.github/workflows')
const PRIVATE_INSTALL_FIXTURE_RELATIVE_PATH = 'testdata/private-install.yaml'
const PRIVATE_INSTALL_FIXTURE = join(here, PRIVATE_INSTALL_FIXTURE_RELATIVE_PATH)
it('keeps workflow npm installs job-private', () => {
  const config = YAML.parse(readFileSync(PRIVATE_INSTALL_FIXTURE, 'utf8'))
  const regex = (pattern) => new RegExp(pattern.replace(/^\(\?m\)/, ''), 'm')
  const normalize = (s) => s.replace(/\\\n\s*/g, ' ')
  const doc = YAML.parse(readFileSync(join(directory, config.workflow), 'utf8'))
  const target = []
  for (const step of doc.jobs?.[config.job]?.steps ?? []) {
    if (step.name === config.step) target.push(normalize(step.run ?? ''))
  }
  expect(target).toHaveLength(1)
  for (const pattern of [config.prefix_pattern, config.install_pattern, config.path_pattern]) expect(target[0]).toMatch(regex(pattern))
})
