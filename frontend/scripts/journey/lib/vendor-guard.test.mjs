/* Fail-closed vendoring guard for the journey shared layer.
 *
 * scripts/journey/lib holds the app-agnostic pieces vendored from
 * fairtrade-design-system/scripts/journey/lib. The vendored body (everything
 * after the VENDORED banner) must be the canonical body, so a shared fix cannot
 * land on one side only.
 *
 * This suite NEVER skips and never reports a pass without comparing. The pin
 * lives in the committed corpus next to this file, so the comparison runs in
 * every environment, including the ones that cannot see a fairtrade checkout at
 * all. A checkout used to be the gate, which meant the invariant was unenforced
 * in exactly the environment CI runs in, and a green run there proved nothing.
 * FAIRTRADE_CHECKOUT is now an opt-in EXTRA check: when it is set the guard also
 * compares against the live canonical file, and a checkout that moved past the
 * pin is reported as its own drift class so it never reads as a corrupt copy.
 *
 * The cases are fixtures, never inline here: the closed inventory, the drift
 * shapes and the executable mutations live in vendor-digests.testdata.yaml and
 * vendor-digests.testdata.manifest.yaml.
 */
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterAll, describe, expect, it } from 'vitest'
import YAML from 'yaml'
import {
  CORPUS_FILE,
  DRIFT_CLASSES,
  FILE_KINDS,
  GUARD_DIR,
  LIVE_MATCHES_PIN,
  MANIFEST_FILE,
  UPSTREAM_SUBDIR,
  bodyDigest,
  canonicalBody,
  directoryFiles,
  inspectVendoredDirectory,
  isPinnedDigest,
  readVendoredBody,
  upstreamProblem,
  upstreamPathFor,
  vendoredCopyProblem,
} from './vendor-guard.mjs'

const CHECKOUT = process.env.FAIRTRADE_CHECKOUT
const corpus = YAML.parse(readFileSync(join(GUARD_DIR, CORPUS_FILE), 'utf8'))
const manifest = YAML.parse(readFileSync(join(GUARD_DIR, MANIFEST_FILE), 'utf8'))
const rows = corpus.files
const vendoredRows = rows.filter((row) => row.kind === 'vendored')
const REQUIRED_NAMED_SECTIONS = ['What failed:', 'Where:', 'Means:', 'Fix:']

/**
 * The drift class a problem declares in its HEAD line. Read from the head rather
 * than from the first `drift:` in the text, because each message also names the
 * OTHER classes to tell the reader which finding this is not, and a text search
 * would pick one of those up instead.
 */
const driftOf = (problem) => problem.match(/^ERROR \[journey-vendoring\] drift: ([a-z-]+)/)?.[1] ?? null
/** The problems a report carries, in class order, for a whole-run assertion. */
const driftClasses = (report) => report.problems.map(driftOf)
/** The one problem carrying a class, for an assertion about that class's text. */
const problemFor = (report, driftClass) => report.problems.find((problem) => driftOf(problem) === driftClass)

/* ── sandbox: a copy of the vendored tree, and a checkout standing in for upstream ── */

const sandboxes = []
/** A throwaway directory, removed after the suite however it ends. */
const sandbox = (label) => {
  const dir = mkdtempSync(join(tmpdir(), `journey-vendor-guard-${label}-`))
  sandboxes.push(dir)
  return dir
}
afterAll(() => {
  for (const dir of sandboxes) rmSync(dir, { recursive: true, force: true })
})

/** Copy every classified file, verbatim, so the sandbox starts byte-identical. */
const materialise = (dir) => {
  mkdirSync(dir, { recursive: true })
  for (const row of rows) copyFileSync(join(GUARD_DIR, row.name), join(dir, row.name))
}

/**
 * A stand-in fairtrade checkout carrying the canonical bodies, written WITHOUT the
 * VENDORED banner because a real canonical file has none. A mutated sandbox and this
 * checkout therefore disagree in exactly the way a moved upstream would: one side
 * changed, the other did not.
 *
 * With `stripBanner: false` it stands in for the other mistake: a checkout that is a
 * CONSUMER tree, so the file it offers is our own copy handed back to us.
 */
const materialiseUpstream = (label, { stripBanner = true } = {}) => {
  const checkout = sandbox(label)
  const lib = join(checkout, UPSTREAM_SUBDIR)
  mkdirSync(lib, { recursive: true })
  for (const row of vendoredRows) {
    const source = readFileSync(join(GUARD_DIR, row.name), 'utf8')
    writeFileSync(join(lib, row.name), stripBanner ? canonicalBody(source) : source)
  }
  return checkout
}

/** Edit bytes in a copied file. An ambiguous anchor is refused, not guessed at. */
const mutateFile = (dir, mutation) => {
  const path = join(dir, mutation.target)
  if (mutation.create !== undefined) {
    writeFileSync(path, mutation.create)
    return
  }
  const source = readFileSync(path, 'utf8')
  const occurrences = source.split(mutation.from).length - 1
  if (occurrences !== 1) {
    throw new Error(
      `mutation ${JSON.stringify(mutation.name)}: its anchor occurs ${occurrences} times in ${mutation.target}, ` +
        'expected exactly once, so the test would be mutating an unchosen byte',
    )
  }
  writeFileSync(path, source.replace(mutation.from, mutation.to))
}

/** Edit the corpus rows the guard reads, leaving the files on disk alone. */
const mutateCorpus = (mutation) => {
  const next = structuredClone(rows)
  if (mutation.dropRow === true) return next.filter((row) => row.name !== mutation.target)
  const row = next.find((candidate) => candidate.name === mutation.target)
  if (row === undefined) throw new Error(`mutation ${JSON.stringify(mutation.name)} names no corpus row`)
  if (mutation.delete === true) delete row[mutation.field]
  else row[mutation.field] = mutation.value
  return next
}

/**
 * Run the guard over a sandbox that has had one mutation applied, in BOTH modes:
 * pin-only, and against a checkout standing in for upstream. A mutation has to be
 * caught with no checkout in the environment, because that is the mode CI runs.
 */
const runMutated = (mutation) => {
  const dir = sandbox('mutated')
  materialise(dir)
  let mutatedRows = rows
  if (mutation.subject === 'vendored-file') {
    mutateFile(dir, mutation)
  } else if (mutation.subject === 'digest-corpus') {
    mutatedRows = mutateCorpus(mutation)
  } else {
    throw new Error(`mutation ${JSON.stringify(mutation.name)} has unknown subject ${mutation.subject}`)
  }
  return {
    pinOnly: inspectVendoredDirectory({ dir, rows: mutatedRows, checkout: null }),
    withCheckout: inspectVendoredDirectory({ dir, rows: mutatedRows, checkout: materialiseUpstream('upstream') }),
  }
}

describe('journey lib vendoring', () => {
  /* ── the closed inventory, by name in both directions ───────────────────── */

  it('classifies every file in the vendored directory exactly once, by name', () => {
    const names = rows.map((row) => row.name)
    expect(new Set(names).size, `duplicate corpus rows: ${names.join(', ')}`).toBe(names.length)
    // every row is one of the closed kinds, and a new kind cannot be invented
    for (const row of rows) expect(FILE_KINDS, `${row.name} has kind ${row.kind}`).toContain(row.kind)
    // exact MEMBERSHIP against the required-name manifest, not a count: a swapped
    // name would satisfy a count and break the contract
    expect([...names].sort()).toEqual([...manifest.requiredFileNames].sort())
    // and the same set, read off the directory rather than off the corpus, so a
    // file nobody classified cannot hide behind a corpus that looks tidy
    expect(directoryFiles(GUARD_DIR)).toEqual([...names].sort())
  })

  it('pins every vendored copy, and pins nothing that is not a copy', () => {
    const pinned = vendoredRows.map((row) => row.name)
    expect([...pinned].sort()).toEqual([...manifest.requiredVendoredNames].sort())
    for (const row of vendoredRows) {
      expect(isPinnedDigest(row.sha256), `${row.name} must carry a sha256 of its canonical body`).toBe(true)
      // the pin is over the body, so it must not be the digest of the banner too
      expect(row.sha256).not.toBe(bodyDigest(readFileSync(join(GUARD_DIR, row.name), 'utf8')))
    }
    for (const row of rows.filter((entry) => entry.kind !== 'vendored')) {
      expect(row.sha256, `${row.name} is ${row.kind} and must not be pinned`).toBeUndefined()
    }
    expect(corpus.digestAlgorithm).toBe('sha256')
  })

  it('reaches a verdict with no fairtrade checkout in the environment', () => {
    // The no-skip property, asserted from the inside: `compared` carries one
    // record per vendored copy whatever the environment, so a run that compared
    // nothing would leave it empty. Silence is not a pass here.
    const previous = process.env.FAIRTRADE_CHECKOUT
    delete process.env.FAIRTRADE_CHECKOUT
    try {
      const report = inspectVendoredDirectory({ dir: GUARD_DIR, rows, checkout: null })
      expect(report.compared.map((record) => record.name).sort()).toEqual(
        [...manifest.requiredVendoredNames].sort(),
      )
      for (const record of report.compared) {
        expect(isPinnedDigest(record.bodyDigest), `${record.name} must have been digested`).toBe(true)
        expect(record.bodyDigest, `${record.name} must have been compared to its pin`).toBe(record.pinnedDigest)
      }
      expect(report.live, 'the live half is opt-in, so it is absent rather than silently passing').toEqual([])
      expect(report.problems).toEqual([])
    } finally {
      if (previous === undefined) delete process.env.FAIRTRADE_CHECKOUT
      else process.env.FAIRTRADE_CHECKOUT = previous
    }
  })

  it('treats the checkout as an extra check, never as the gate', () => {
    const withCheckout = inspectVendoredDirectory({ dir: GUARD_DIR, rows, checkout: materialiseUpstream('real') })
    // the pin comparison ran in full either way; naming a checkout only adds to it
    expect(withCheckout.compared.map((record) => record.name).sort()).toEqual(
      inspectVendoredDirectory({ dir: GUARD_DIR, rows, checkout: null }).compared.map((record) => record.name).sort(),
    )
    expect(withCheckout.live.map((record) => record.name).sort()).toEqual([...manifest.requiredVendoredNames].sort())
    for (const record of withCheckout.live) {
      expect(record.liveState, `${record.name} drifted from its pin: ${withCheckout.problems.join('\n\n')}`)
        .toBe(LIVE_MATCHES_PIN)
    }
    expect(withCheckout.problems).toEqual([])
  })

  /* ── the comparison itself, one always-run test per vendored copy ────────── */

  for (const row of vendoredRows) {
    it(`${row.name}: the vendored body matches its pinned canonical digest, with no checkout required`, () => {
      const body = readVendoredBody(GUARD_DIR, row.name)
      expect(bodyDigest(body), `${row.name} drifted from its pin`).toBe(row.sha256)
      const report = inspectVendoredDirectory({ dir: GUARD_DIR, rows, checkout: null })
      expect(report.problems, report.problems.join('\n\n')).toEqual([])
    })
  }

  /* ── the opt-in live comparison, one test per vendored copy ─────────────── */

  for (const row of vendoredRows) {
    // Reported as SKIPPED rather than absent when no checkout is named, so a run
    // never looks as though it proved upstream agreement. It is not the gate:
    // the pinned comparison above already ran and already passed.
    it.runIf(CHECKOUT !== undefined)(
      `${row.name}: the live fairtrade canonical body matches the pin`,
      () => {
        const report = inspectVendoredDirectory({ dir: GUARD_DIR, rows, checkout: CHECKOUT })
        const record = report.live.find((entry) => entry.name === row.name)
        expect(record, `${row.name} was not compared against FAIRTRADE_CHECKOUT`).toBeTruthy()
        expect(
          record.liveState,
          report.problems.join('\n\n'),
        ).toBe('matches-pin')
        expect(record.path).toBe(upstreamPathFor(CHECKOUT, row.name))
      },
    )
  }

  /* ── the two drift classes must not read as each other ──────────────────── */

  it('names the required drift cases, so a dropped case is a failure', () => {
    expect(corpus.driftCases.map((entry) => entry.name).sort()).toEqual(
      [...manifest.requiredDriftCaseNames].sort(),
    )
  })

  it.each(corpus.driftCases.map((entry) => [entry.name, entry]))('drift case: %s', (_name, entry) => {
    const problem = entry.drift === DRIFT_CLASSES.VENDORED_COPY
      ? vendoredCopyProblem({
          name: entry.subject,
          dir: GUARD_DIR,
          bodyDigest: entry.bodyDigest,
          pinnedDigest: entry.pinnedDigest,
        })
      : upstreamProblem({
          name: entry.subject,
          dir: GUARD_DIR,
          checkout: '/checkouts/fairtrade',
          liveDigest: entry.liveDigest,
          pinnedDigest: entry.pinnedDigest,
          liveState: entry.drift,
        })
    expect(problem, `${entry.drift} must produce a problem`).not.toBeNull()
    // the class is stated, so a reader and the test both know which side is wrong
    expect(problem).toContain(`drift: ${entry.drift}`)
    // it names the numbers a failing run would print
    expect(problem).toContain(entry.pinnedDigest)
    if (entry.bodyDigest !== entry.pinnedDigest) expect(problem).toContain(entry.bodyDigest)
    if (entry.liveDigest !== null) expect(problem).toContain(entry.liveDigest)
    // and it is actionable
    for (const phrase of entry.expectedPhrases) expect(problem, `missing ${JSON.stringify(phrase)}`).toContain(phrase)
    for (const section of REQUIRED_NAMED_SECTIONS) expect(problem).toContain(section)
  })

  it('keeps a corrupt copy and a moved checkout as different findings', () => {
    const [vendoredCase, upstreamCase, missingCase, notCanonicalCase] = corpus.driftCases
    const corrupt = vendoredCopyProblem({
      name: vendoredCase.subject,
      dir: GUARD_DIR,
      bodyDigest: vendoredCase.bodyDigest,
      pinnedDigest: vendoredCase.pinnedDigest,
    })
    const upstreamFor = (entry) => upstreamProblem({
      name: entry.subject,
      dir: GUARD_DIR,
      checkout: '/checkouts/fairtrade',
      liveDigest: entry.liveDigest,
      pinnedDigest: entry.pinnedDigest,
      liveState: entry.drift,
    })
    const moved = upstreamFor(upstreamCase)
    const missing = upstreamFor(missingCase)
    const notCanonical = upstreamFor(notCanonicalCase)
    // The copy here is wrong in one case and intact in the other three, and each
    // text says which, so none of them sends the reader to re-pin the wrong side.
    expect(corrupt).toContain('is the VENDORED COPY being wrong, not upstream movement')
    expect(moved).toContain('the VENDORED COPY HERE IS INTACT')
    expect(missing).toContain('must not be reported as a broken vendored copy')
    expect(notCanonical).toContain('Do not re-pin from this checkout: it would bless our own copy')
    // the four classes are four texts, so no two findings can be read as one
    const distinct = new Set([corrupt, moved, missing, notCanonical])
    expect(distinct.size, 'a drift class is reusing another class\'s text').toBe(4)
    for (const problem of distinct) {
      for (const section of REQUIRED_NAMED_SECTIONS) expect(problem).toContain(section)
    }
  })

  /* ── the mutations, applied for real, in both modes ─────────────────────── */

  it('names every required mutation, so a dropped case is a failure', () => {
    const names = manifest.mutations.map((entry) => entry.name)
    expect(new Set(names).size, `duplicate mutation names: ${names.join(', ')}`).toBe(names.length)
    expect([...names].sort()).toEqual([...manifest.requiredMutationNames].sort())
    for (const mutation of manifest.mutations) {
      expect(['digest-corpus', 'vendored-file'], `${mutation.name} subject`).toContain(mutation.subject)
      expect(['null', 'a phrase'], `${mutation.name} diagnostic kind`).toContain(
        mutation.expectedDiagnostic === null ? 'null' : 'a phrase',
      )
    }
  })

  it.each(manifest.mutations.map((entry) => [entry.name, entry]))('mutation: %s', (_name, mutation) => {
    const reports = runMutated(mutation)
    for (const [mode, report] of Object.entries(reports)) {
      const detail = (text) => `${mutation.name} [${mode}]\n${text}\n\n${report.problems.join('\n\n')}`
      if (mutation.expectedDiagnostic === null) {
        // the one mutation that must NOT be caught: a reworded banner leaves the
        // canonical body, and therefore the pin, untouched
        expect(report.problems, detail('expected no problem')).toEqual([])
        expect(report.compared, detail('the pin must still have been compared')).toHaveLength(vendoredRows.length)
        continue
      }
      const matching = report.problems.filter((problem) => problem.includes(mutation.expectedDiagnostic))
      expect(matching, detail(`no problem mentioned ${JSON.stringify(mutation.expectedDiagnostic)}`))
        .not.toHaveLength(0)
      for (const problem of matching) {
        // it names the file the mutation touched, so the two inventory cases
        // (a dropped row, and a file with no row) are told apart by subject
        expect(problem, detail(`no problem named ${mutation.target}`)).toContain(mutation.target)
        // every refusal a mutation can trigger is still actionable
        for (const section of REQUIRED_NAMED_SECTIONS) {
          expect(problem, detail(`the problem is missing ${section}`)).toContain(section)
        }
      }
    }
  })

  it('catches a moved checkout only when a checkout is named', () => {
    // The upstream half has to stay optional without becoming a silent pass: with
    // no checkout there is no upstream verdict at all, and a drifted canonical
    // body is only ever reported as its own drift class.
    const mutated = sandbox('drifted-copy')
    materialise(mutated)
    writeFileSync(
      join(mutated, 'determinism-constants.mjs'),
      readFileSync(join(mutated, 'determinism-constants.mjs'), 'utf8').replace(
        'export const PRNG_SEED = 0x9e3779b9',
        'export const PRNG_SEED = 0x9e3779b8',
      ),
    )
    const pinOnly = inspectVendoredDirectory({ dir: mutated, rows, checkout: null })
    const upstream = inspectVendoredDirectory({ dir: mutated, rows, checkout: materialiseUpstream('intact') })

    expect(driftClasses(pinOnly)).toEqual([DRIFT_CLASSES.VENDORED_COPY])
    // the intact checkout is not blamed for our copy being wrong
    expect(driftClasses(upstream)).toEqual([DRIFT_CLASSES.VENDORED_COPY])
    expect(upstream.live.every((record) => record.liveState === LIVE_MATCHES_PIN)).toBe(true)

    // and a checkout that moved while our copy stayed pinned is only ever upstream
    const stale = sandbox('stale-copy')
    materialise(stale)
    const staleRows = structuredClone(rows)
    staleRows.find((row) => row.name === 'determinism-constants.mjs').sha256 = '4'.repeat(64)
    const staleReport = inspectVendoredDirectory({ dir: stale, rows: staleRows, checkout: materialiseUpstream('moved') })
    expect(driftClasses(staleReport).sort()).toEqual(
      [DRIFT_CLASSES.VENDORED_COPY, DRIFT_CLASSES.UPSTREAM_CANONICAL].sort(),
    )
    // the two never share a sentence: the upstream one says the copy here is fine
    expect(problemFor(staleReport, DRIFT_CLASSES.UPSTREAM_CANONICAL)).toContain('the VENDORED COPY HERE IS INTACT')
    expect(problemFor(staleReport, DRIFT_CLASSES.VENDORED_COPY)).toContain(
      'is the VENDORED COPY being wrong, not upstream movement',
    )

    // and a checkout holding a consumer's copy says so, rather than reporting the
    // banner as if it were an upstream edit
    const misreported = inspectVendoredDirectory({
      dir: GUARD_DIR,
      rows,
      checkout: materialiseUpstream('consumer-tree', { stripBanner: false }),
    })
    expect(driftClasses(misreported)).toEqual(vendoredRows.map(() => DRIFT_CLASSES.UPSTREAM_NOT_CANONICAL))
    expect(problemFor(misreported, DRIFT_CLASSES.UPSTREAM_NOT_CANONICAL))
      .toContain('FAIRTRADE_CHECKOUT is not pointing at the design system')
  })
})
