/* Fail-closed vendoring guard for the journey shared layer.
 *
 * scripts/journey/lib holds app-agnostic modules copied byte-for-byte out of
 * fairtrade-design-system/scripts/journey/lib. The canonical BODY (every byte
 * AFTER the VENDORED banner comment) has to be the same copy upstream, or a
 * shared fix can land on one side only and neither side notices.
 *
 * Byte identity is proved against a committed PIN, not against a checkout. The
 * corpus in vendor-digests.testdata.yaml carries one sha256 per vendored body,
 * and this guard compares every vendored body against it on EVERY run, in an
 * environment that cannot see the design system at all. That is the whole point:
 * the environments that cannot see a checkout (CI, a release image, a reviewer
 * with no fairtrade clone) are exactly the ones that must not be able to report
 * the invariant as checked.
 *
 * FAIRTRADE_CHECKOUT stays an OPT-IN extra check, never the gate. When it names
 * a checkout, the guard also compares each vendored body against the live
 * canonical file. Every way that can turn out badly is its own drift class with
 * its own text, so "the local copy is corrupt" and "upstream advanced past our
 * pin" are never the same sentence and never the same repair.
 *
 * Re-pinning after a re-vendor is the same read, not a second implementation,
 * from the repository root:
 *   node frontend/scripts/journey/lib/vendor-guard.mjs
 * prints the digest each vendored body currently carries.
 */
import { createHash } from 'node:crypto'
import { readFileSync, readdirSync, realpathSync, statSync } from 'node:fs'
import { dirname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import YAML from 'yaml'

/** This directory, i.e. the vendored tree itself. */
export const GUARD_DIR = dirname(fileURLToPath(import.meta.url))

/** Where the corpus and the required-name manifest live, for problem text. */
export const CORPUS_FILE = 'vendor-digests.testdata.yaml'
export const MANIFEST_FILE = 'vendor-digests.testdata.manifest.yaml'

/** The vendored tree's path inside the fairtrade checkout. */
export const UPSTREAM_SUBDIR = join('scripts', 'journey', 'lib')

/**
 * The banner a vendored copy carries above the canonical body. Only the banner is
 * Village's: it is stripped before the digest is taken, so rewording it is not a
 * re-vendor and does not require a re-pin.
 */
const BANNER = /^\/\* VENDORED from [\s\S]*?\*\/\n/

/** The corpus file kinds. A file in the directory must be exactly one of them. */
export const FILE_KINDS = Object.freeze(['app-local', 'guard', 'vendored'])

/**
 * The ways a pin can stop describing the world. Each is its own class with its own
 * text, because each has a different side that is wrong and a different repair:
 * a copy that moved off its pin is repaired here, a checkout that moved past the pin
 * is repaired upstream and re-vendored, an absent or mis-pointed checkout is repaired
 * in the invocation. Collapsing any two of them sends the reader to re-pin the wrong
 * side and makes the gate green over a false invariant.
 */
export const DRIFT_CLASSES = Object.freeze({
  VENDORED_COPY: 'vendored-copy',
  UPSTREAM_CANONICAL: 'upstream-canonical',
  UPSTREAM_MISSING: 'upstream-missing',
  UPSTREAM_NOT_CANONICAL: 'upstream-not-canonical',
})

/** The only live state that is not a drift. */
export const LIVE_MATCHES_PIN = 'matches-pin'

/* ── reading bytes ───────────────────────────────────────────────────────── */

/** The canonical body of a vendored file: every byte after the VENDORED banner. */
export const canonicalBody = (source) => source.replace(BANNER, '')

/** sha256 of a canonical body, lowercase hex. */
export const bodyDigest = (body) => createHash('sha256').update(body, 'utf8').digest('hex')

/** True for a well-formed pin: 64 lowercase hex characters. */
export const isPinnedDigest = (value) => typeof value === 'string' && /^[0-9a-f]{64}$/.test(value)

/** The vendored body of `<dir>/<name>`, banner removed. */
export const readVendoredBody = (dir, name) => canonicalBody(readFileSync(join(dir, name), 'utf8'))

/** The regular files in `dir`, sorted. Symlinks are followed, directories skipped. */
export const directoryFiles = (dir) =>
  readdirSync(dir)
    .filter((name) => statSync(join(dir, name)).isFile())
    .sort()

/* ── text helpers ─────────────────────────────────────────────────────────── */

const detail = (head, what, where, means, fix) =>
  `${head}\n` +
  `  What failed: ${what}\n` +
  `  Where: ${where}\n` +
  `  Means: ${means}\n` +
  `  Fix: ${fix}`

/** A path relative to the repository root, so a problem names a repo file. */
const repoRoot = join(GUARD_DIR, '..', '..', '..', '..')
const rel = (path) => {
  const fromRepo = relative(repoRoot, path)
  if (fromRepo === '' || fromRepo.startsWith('..')) return path
  return fromRepo
}

/** How a value reads inside a problem, so a malformed corpus row is quotable. */
const describe = (value) => {
  if (value === undefined) return 'nothing'
  if (typeof value === 'string') return `"${value}"`
  return JSON.stringify(value) ?? 'nothing'
}

/* ── the closed inventory ─────────────────────────────────────────────────── */

/**
 * Null when the corpus classifies the vendored directory exactly, else the reason.
 *
 * Closed in both directions. A file on disk with no row would otherwise be a
 * vendored copy nothing compares; a row with no file, or a vendored row with no
 * pin, or a pin on a row that is not a vendored copy, would each leave the
 * inventory claiming coverage it does not have.
 */
export const classificationProblem = ({ dir, rows }) => {
  const where = `${rel(dir)}, classified by ${rel(dir)}/${CORPUS_FILE}`
  const cover = (what, fix) => detail(
    `ERROR [journey-vendoring] the vendored file inventory is not closed: ${what}`,
    what,
    where,
    'the byte-identity invariant is only as wide as the inventory that describes it. A file the ' +
      'inventory does not name exactly once is a module in the journey shared layer that nothing ' +
      'compares, so a fix can land on one side of the vendoring boundary while the other side ' +
      'stays stale, and this gate would still be green.',
    fix,
  )

  if (!Array.isArray(rows) || rows.length === 0) {
    return cover(
      `the corpus has no file rows (got ${describe(rows)}).`,
      `add one row per file in ${rel(dir)} to ${CORPUS_FILE}, each with a name and a kind, and give ` +
        'every vendored row a sha256 of its canonical body.',
    )
  }

  for (const row of rows) {
    if (row === null || typeof row !== 'object' || Array.isArray(row)) {
      return cover(
        `the corpus has a row that is not a record (got ${describe(row)}).`,
        `fix that row in ${CORPUS_FILE}.`,
      )
    }
    const { name, kind } = row
    if (typeof name !== 'string' || name === '' || name.includes('/') || name.includes('\\') || name === '.' || name === '..') {
      return cover(
        `the corpus row ${JSON.stringify(name)} is not a bare file name in this directory.`,
        'name each row with a plain file name such as "assertions.mjs" in ' + CORPUS_FILE + '.',
      )
    }
    if (!FILE_KINDS.includes(kind)) {
      return cover(
        `the corpus row "${name}" has kind ${JSON.stringify(kind)}, which is not one of ${FILE_KINDS.join(' / ')}.`,
        `set "${name}" to kind ${FILE_KINDS.join(', ')} in ${CORPUS_FILE}; a vendored copy needs kind ` +
          '"vendored" so its body is compared against a pin.',
      )
    }
    if (kind === 'vendored' && !isPinnedDigest(row.sha256)) {
      return cover(
        `the vendored row "${name}" carries no usable pin (sha256 is ${describe(row.sha256)}).`,
        `run \`node ${rel(dir)}/vendor-guard.mjs\` and write the digest it prints for "${name}" into the ` +
          `sha256 field of that row in ${CORPUS_FILE}. A vendored row without a pin is a copy nothing compares.`,
      )
    }
    if (kind !== 'vendored' && row.sha256 !== undefined) {
      return cover(
        `the ${kind} row "${name}" carries a sha256, so a non-vendored file looks pinned.`,
        `delete the sha256 field from "${name}" in ${CORPUS_FILE}, or set its kind to "vendored" if it ` +
          'really is a copy of a fairtrade module and should be compared against its upstream body.',
      )
    }
  }

  const rowNames = rows.map((row) => row.name)
  const duplicates = [...new Set(rowNames.filter((name, at) => rowNames.indexOf(name) !== at))].sort()
  if (duplicates.length > 0) {
    return cover(
      `the corpus classifies ${duplicates.map((name) => `"${name}"`).join(', ')} more than once.`,
      `give every file in ${rel(dir)} exactly one row in ${CORPUS_FILE}.`,
    )
  }

  const onDisk = directoryFiles(dir)
  const unclassified = onDisk.filter((name) => !rowNames.includes(name)).sort()
  if (unclassified.length > 0) {
    return cover(
      `${unclassified.map((name) => `"${name}"`).join(', ')} ${unclassified.length === 1 ? 'is' : 'are'} in ` +
        `${rel(dir)} but not classified by ${CORPUS_FILE}.`,
      `add a row for ${unclassified.map((name) => `"${name}"`).join(', ')} in ${CORPUS_FILE}. If it is a copy ` +
        'of a fairtrade module, set kind "vendored" and pin its canonical body; if it is Village\'s own, set ' +
        'kind "app-local"; if it belongs to this guard, set kind "guard".',
    )
  }

  const absent = rowNames.filter((name) => !onDisk.includes(name)).sort()
  if (absent.length > 0) {
    return cover(
      `${absent.map((name) => `"${name}"`).join(', ')} ${absent.length === 1 ? 'is' : 'are'} classified by ` +
        `${CORPUS_FILE} but not in ${rel(dir)}.`,
      `delete the ${absent.length === 1 ? 'row' : 'rows'} for ${absent.map((name) => `"${name}"`).join(', ')} from ` +
        `${CORPUS_FILE}, or re-vendor the ${absent.length === 1 ? 'file' : 'files'}.`,
    )
  }

  return null
}

/* ── the drift classes ────────────────────────────────────────────────────── */

/** The upstream path of a vendored file, inside a fairtrade checkout. */
export const upstreamPathFor = (checkout, name) => join(checkout, UPSTREAM_SUBDIR, name)

/**
 * The problem for a vendored copy whose body no longer digests to its pin, else
 * null. The copy on disk is the thing that is wrong here; the text says so, and
 * says the checkout is not implicated, so this never reads as upstream drift.
 */
export const vendoredCopyProblem = ({ name, dir, bodyDigest: actual, pinnedDigest }) => {
  if (actual === pinnedDigest) return null
  const upstream = `fairtrade-design-system/${join(UPSTREAM_SUBDIR, name)}`
  return detail(
    `ERROR [journey-vendoring] drift: ${DRIFT_CLASSES.VENDORED_COPY} in ${name}.`,
    `the vendored body of ${rel(join(dir, name))} digests to ${actual}, but the pinned canonical digest is ` +
      `${pinnedDigest}.`,
    `the vendored copy at ${rel(join(dir, name))}, pinned by the "${name}" row of ${rel(dir)}/${CORPUS_FILE}.`,
    'the copy in this tree is not the canonical body any more, so it no longer matches ' +
      `${upstream} and a shared fix can land on one side only. This is the VENDORED COPY being wrong, not ` +
      'upstream movement: upstream drift is reported as drift class ' +
      `${DRIFT_CLASSES.UPSTREAM_CANONICAL} and states that the copy here is intact.`,
    `(1) re-vendor: copy ${upstream} over ${rel(join(dir, name))} and prepend the VENDORED banner. ` +
      `(2) re-pin: run \`node ${rel(dir)}/vendor-guard.mjs\` and write the digest it prints for "${name}" ` +
      `into the sha256 field of that row in ${rel(dir)}/${CORPUS_FILE}. ` +
      '(3) re-run `pnpm --dir frontend check:journey-vendoring`.',
  )
}

/**
 * The problem for a checkout whose live canonical body has moved past the pin,
 * else null. Here the copy in this tree still digests to the pin, so the text
 * says the copy is intact and refuses to be read as a corrupt vendored copy.
 */
export const upstreamProblem = ({ name, dir, checkout, liveDigest, pinnedDigest, liveState }) => {
  if (liveState === LIVE_MATCHES_PIN) return null
  const livePath = upstreamPathFor(checkout, name)
  if (liveState === DRIFT_CLASSES.UPSTREAM_MISSING) {
    return detail(
      `ERROR [journey-vendoring] drift: ${DRIFT_CLASSES.UPSTREAM_MISSING} for ${name}.`,
      `there is no canonical ${name} at ${livePath}, so the pinned digest ${pinnedDigest} could not be ` +
        'compared against upstream.',
      `${rel(join(dir, name))} pinned by ${rel(dir)}/${CORPUS_FILE}, against FAIRTRADE_CHECKOUT=${checkout}.`,
      `the checkout does not carry the upstream this copy was taken from. The copy in this tree is still ` +
        `compared against its pin, so the byte-identity invariant holds; only the upstream half of the ` +
        'proof is unavailable. An unusable checkout must not be reported as a broken vendored copy: that ' +
        `is drift class ${DRIFT_CLASSES.VENDORED_COPY}, which names the vendored path as the side that is ` +
        'wrong.',
      `point FAIRTRADE_CHECKOUT at a checkout that has fairtrade-design-system/${UPSTREAM_SUBDIR}/${name} ` +
        'in it, or unset FAIRTRADE_CHECKOUT to run the pin-only check. If the module was removed upstream, ' +
        `delete the "${name}" row from ${rel(dir)}/${CORPUS_FILE} and delete the copy.`,
    )
  }
  if (liveState === DRIFT_CLASSES.UPSTREAM_NOT_CANONICAL) {
    return detail(
      `ERROR [journey-vendoring] drift: ${DRIFT_CLASSES.UPSTREAM_NOT_CANONICAL} for ${name}.`,
      `${livePath} opens with a VENDORED banner, so it is a consumer's COPY of ${name} and not the ` +
        `canonical file; its body digests to ${liveDigest} where a canonical file digests to ` +
        `${pinnedDigest}.`,
      `${rel(join(dir, name))} pinned by ${rel(dir)}/${CORPUS_FILE}, against FAIRTRADE_CHECKOUT=${checkout}.`,
      `FAIRTRADE_CHECKOUT is not pointing at the design system. A consumer's copy of ${name} carries the ` +
        'VENDORED banner this guard adds, so a checkout holding one is a peasant, village or other ' +
        `consumer tree, where the upstream half of the proof does not exist at all. The copy in this tree ` +
        'is still compared against its own pin, so the byte-identity invariant holds.',
      `point FAIRTRADE_CHECKOUT at a fairtrade-design-system checkout (the one with ` +
        `fairtrade-design-system/${UPSTREAM_SUBDIR}/${name} in it), or unset FAIRTRADE_CHECKOUT to run the ` +
        'pin-only check. Do not re-pin from this checkout: it would bless our own copy.',
    )
  }
  return detail(
    `ERROR [journey-vendoring] drift: ${DRIFT_CLASSES.UPSTREAM_CANONICAL} for ${name}.`,
    `the live canonical body at ${livePath} digests to ${liveDigest}, but the pinned canonical digest is ` +
      `${pinnedDigest}.`,
    `${rel(join(dir, name))} pinned by ${rel(dir)}/${CORPUS_FILE}, against FAIRTRADE_CHECKOUT=${checkout}.`,
    'the VENDORED COPY HERE IS INTACT: its own body still digests to the pinned value, so the mismatch is ' +
      'entirely upstream. The fairtrade module changed after the last re-vendor, so this checkout no longer ' +
      'proves that the two sides agree. A corrupted vendored copy is a different finding, reported as drift ' +
      `class ${DRIFT_CLASSES.VENDORED_COPY} and naming the vendored path as the side that is wrong; do not ` +
      're-pin from a drifted checkout without re-vendoring first, or the pin would bless a stale copy.',
    `(1) re-vendor: copy ${livePath} to ${rel(join(dir, name))} and prepend the VENDORED banner. (2) re-pin: run ` +
      `\`node ${rel(dir)}/vendor-guard.mjs\` and write the digest it prints for "${name}" into the sha256 ` +
      `field of that row in ${rel(dir)}/${CORPUS_FILE}. (3) re-run ` +
      '`pnpm --dir frontend check:journey-vendoring` with FAIRTRADE_CHECKOUT still pointing at the checkout.',
  )
}

/* ── the guard itself ─────────────────────────────────────────────────────── */

const vendoredRows = (rows) => (Array.isArray(rows) ? rows : []).filter((row) => row?.kind === 'vendored')

/**
 * Compare every vendored body against its pin, and against the live canonical
 * file when a checkout is named. Never returns without having compared: `compared`
 * carries one record per vendored row whatever the environment, so a caller (or a
 * test) can see that the comparison happened rather than infer it from silence.
 *
 * @param {object} input
 * @param {string} input.dir the vendored directory to read
 * @param {object[]} input.rows the corpus rows
 * @param {string|null} [input.checkout] a fairtrade checkout, or null for pin-only
 * @returns {{ problems: string[], compared: object[], live: object[] }}
 */
export const inspectVendoredDirectory = ({ dir, rows, checkout = null }) => {
  const problems = [classificationProblem({ dir, rows })].filter((problem) => problem !== null)
  const compared = []
  const live = []

  for (const row of vendoredRows(rows)) {
    // A row whose pin is malformed cannot be compared, and there is nothing to
    // compare it against. That is a refusal, not an exemption: the inventory
    // problem above already fails the run, so a run that compared nothing still
    // does not pass.
    if (!isPinnedDigest(row.sha256)) continue
    let body
    try {
      body = readVendoredBody(dir, row.name)
    } catch (error) {
      problems.push(
        `ERROR [journey-vendoring] ${row.name} could not be read, so its pinned digest was not compared.\n` +
          `  What failed: reading ${rel(join(dir, row.name))} raised ${error?.code ?? error}.\n` +
          `  Where: the vendored copy at ${rel(join(dir, row.name))}, pinned by ${rel(dir)}/${CORPUS_FILE}.\n` +
          '  Means: an unreadable vendored copy is not a passing copy. Leaving it uncompared would let a ' +
          'byte-identity invariant pass on a file nothing could read.\n' +
          `  Fix: re-vendor ${row.name} from fairtrade-design-system/${join(UPSTREAM_SUBDIR, row.name)} and ` +
          `re-pin it, then re-run \`pnpm --dir frontend check:journey-vendoring\`.`,
      )
      continue
    }

    const actual = bodyDigest(body)
    compared.push({ name: row.name, bodyDigest: actual, pinnedDigest: row.sha256 })
    const local = vendoredCopyProblem({ name: row.name, dir, bodyDigest: actual, pinnedDigest: row.sha256 })
    if (local !== null) problems.push(local)

    if (checkout === null) continue
    const path = upstreamPathFor(checkout, row.name)
    let liveState
    let liveDigest
    try {
      const live = readFileSync(path, 'utf8')
      liveDigest = bodyDigest(live)
      if (BANNER.test(live)) liveState = DRIFT_CLASSES.UPSTREAM_NOT_CANONICAL
      else if (liveDigest === row.sha256) liveState = LIVE_MATCHES_PIN
      else liveState = DRIFT_CLASSES.UPSTREAM_CANONICAL
    } catch {
      liveState = DRIFT_CLASSES.UPSTREAM_MISSING
      liveDigest = null
    }
    live.push({ name: row.name, path, liveDigest, liveState })
    const upstream = upstreamProblem({ name: row.name, dir, checkout, liveDigest, pinnedDigest: row.sha256, liveState })
    if (upstream !== null) problems.push(upstream)
  }

  return { problems, compared, live }
}

/* ── tool mode: the digest a re-vendor would re-pin ───────────────────────── */

/**
 * Print the digest each vendored body currently carries, so re-vendoring and
 * re-pinning are one read of one implementation. Only when this module is the
 * process entry point, so importing it into a test never prints.
 */
const printPins = () => {
  // `node vendor-guard.mjs | head` closes the pipe early. That is the reader
  // stopping, not a failed guard, so EPIPE is swallowed instead of thrown.
  process.stdout.on('error', (error) => {
    if (error?.code !== 'EPIPE') throw error
  })
  const rows = YAML.parse(readFileSync(join(GUARD_DIR, CORPUS_FILE), 'utf8')).files
  const inventory = classificationProblem({ dir: GUARD_DIR, rows })
  for (const row of vendoredRows(rows)) {
    const actual = bodyDigest(readVendoredBody(GUARD_DIR, row.name))
    const state = actual === row.sha256 ? 'unchanged' : 'CHANGED'
    process.stdout.write(`${row.name} ${actual} ${state}\n`)
  }
  if (inventory !== null) {
    process.stderr.write(`${inventory}\n`)
    process.exitCode = 1
    return
  }
  process.stdout.write(
    `\nPaste the CHANGED digests into the sha256 fields in ${rel(GUARD_DIR)}/${CORPUS_FILE} once the copies ` +
      'themselves are the canonical bodies again.\n',
  )
}

const entry = process.argv[1] === undefined ? null : realpathSync(process.argv[1])
if (entry === realpathSync(fileURLToPath(import.meta.url))) printPins()
