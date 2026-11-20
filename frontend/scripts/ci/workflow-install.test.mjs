import { readFileSync, readdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { it, expect } from 'vitest'
import YAML from 'yaml'
const here = dirname(fileURLToPath(import.meta.url))
const directory = join(here, '../../../.github/workflows')
it('keeps workflow npm installs job-private', () => {
  const config = YAML.parse(readFileSync(join(here, 'testdata/private-install.yaml'), 'utf8'))
  const regex = (pattern) => new RegExp(pattern.replace(/^\(\?m\)/, ''), 'm')
  const global = regex(config.global_pattern)
  const normalize = (s) => s.replace(/\\\n\s*/g, ' ')
  const names = config.cases.map((row) => row.name)
  expect([...new Set(names)].sort()).toEqual([...config.required_names].sort()); expect(names).toHaveLength(new Set(names).size)
  for (const row of config.cases) expect(global.test(normalize(row.input)), row.name).toBe(row.want)
  const files = readdirSync(directory).filter((name) => /\.ya?ml$/.test(name))
  expect(files.length).toBeGreaterThan(0)
  const target = []
  for (const file of files) {
    const doc = YAML.parse(readFileSync(join(directory, file), 'utf8'))
    for (const [job, value] of Object.entries(doc.jobs ?? {})) {
      for (const step of value.steps ?? []) {
        const run = normalize(step.run ?? '')
        expect(run, `${file} ${job} ${step.name}`).not.toMatch(global)
        if (file === config.workflow && job === config.job && step.name === config.step) target.push(run)
      }
    }
  }
  expect(target).toHaveLength(1)
  for (const pattern of [config.prefix_pattern, config.install_pattern, config.path_pattern]) expect(target[0]).toMatch(regex(pattern))
})
