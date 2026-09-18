/* Post journey evidence to the pull request as a GitHub App bot.
 *
 * Reads the JSON reporter output, summarizes the run, and uploads a small,
 * representative set of screenshots (plus, on failure, the failing tests'
 * videos converted to H.264 mp4) as inline PR comment attachments via
 * `gh --attach`. GitHub plays video inline only as a comment attachment, so
 * this is what makes a clip watchable on the PR.
 *
 * Idempotent: any earlier bot comment carrying MARKER is deleted first, so a
 * re-run replaces the evidence instead of piling up comments.
 *
 * env: GH_TOKEN (App installation token with Issues: write), PR_NUMBER,
 *      GITHUB_REPOSITORY, GITHUB_SERVER_URL, GITHUB_RUN_ID
 * optional: MAX_IMAGES (default 4), MAX_VIDEOS (default 2)
 */
import { execFileSync } from 'node:child_process'
import { existsSync, globSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import { homedir, tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const REPORT = join(HERE, '.artifacts', 'report.json')
const MARKER = 'journey-evidence'
const MAX_IMAGES = Number(process.env.MAX_IMAGES || 4)
const MAX_VIDEOS = Number(process.env.MAX_VIDEOS || 2)

const { PR_NUMBER, GITHUB_REPOSITORY, GITHUB_SERVER_URL = 'https://github.com', GITHUB_RUN_ID } = process.env
if (!PR_NUMBER || !GITHUB_REPOSITORY) {
  console.error('ci-post-evidence: PR_NUMBER and GITHUB_REPOSITORY are required')
  process.exit(1)
}
if (!existsSync(REPORT)) {
  console.error(`ci-post-evidence: no report at ${REPORT}; skipping evidence`)
  process.exit(0)
}

const walkTests = (report) => {
  const tests = []
  const walk = (suite) => {
    for (const spec of suite.specs || []) {
      for (const t of spec.tests || []) {
        const result = t.results?.[0] || {}
        tests.push({
          title: spec.title,
          project: t.projectName,
          status: result.status,
          attachments: result.attachments || [],
        })
      }
    }
    for (const child of suite.suites || []) walk(child)
  }
  for (const suite of report.suites || []) walk(suite)
  return tests
}

const tests = walkTests(JSON.parse(readFileSync(REPORT, 'utf8')))
const failed = tests.filter((t) => t.status && t.status !== 'passed' && t.status !== 'skipped')
const passed = tests.filter((t) => t.status === 'passed')
const skipped = tests.filter((t) => t.status === 'skipped')

const byContentType = (list, ct) =>
  list.flatMap((t) =>
    (t.attachments || [])
      .filter((a) => a.contentType === ct && a.path && existsSync(a.path))
      .map((a) => ({ path: a.path, alt: `${t.project}: ${t.title}` })),
  )

const chosen = []
const seen = new Set()
for (const a of [...byContentType(failed, 'image/png'), ...byContentType(passed.filter((t) => t.project === 'dark'), 'image/png'), ...byContentType(passed, 'image/png')]) {
  if (chosen.length >= MAX_IMAGES) break
  if (seen.has(a.path)) continue
  seen.add(a.path)
  chosen.push(a)
}

const ffmpeg = (() => {
  const roots = [process.env.PLAYWRIGHT_BROWSERS_PATH, join(homedir(), '.cache', 'ms-playwright')].filter(Boolean)
  for (const root of roots) {
    const found = [...globSync(`${root}/ffmpeg-*/ffmpeg-linux`), ...globSync(`${root}/ffmpeg-*/ffmpeg`)]
    if (found.length) return found[0]
  }
  return null
})()

const tmp = mkdtempSync(join(tmpdir(), 'journey-evidence-'))
const attachments = chosen.map((a) => `${a.path}#${a.alt}`)
for (const v of byContentType(failed, 'video/webm').slice(0, MAX_VIDEOS)) {
  if (ffmpeg) {
    const out = join(tmp, `clip-${attachments.length}.mp4`)
    try {
      execFileSync(ffmpeg, ['-y', '-loglevel', 'error', '-i', v.path, '-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-movflags', '+faststart', out], { stdio: 'inherit' })
      attachments.push(`${out}#${v.alt}`)
      continue
    } catch {
      /* fall back to the webm */
    }
  }
  attachments.push(`${v.path}#${v.alt}`)
}

const runUrl = GITHUB_RUN_ID ? `${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}/actions/runs/${GITHUB_RUN_ID}` : null
const body = [
  `<!-- ${MARKER} -->`,
  `## journey: ${failed.length ? 'failed' : 'passed'}`,
  '',
  `**${passed.length} passed**, **${failed.length} failed**, ${skipped.length} skipped (both themes).`,
  ...(failed.length ? ['', 'Failing:', ...failed.map((t) => `- [${t.project}] ${t.title}`)] : []),
  '',
  runUrl ? `Full traces and artifacts: [workflow run](${runUrl})` : 'Full traces and artifacts are attached to the workflow run.',
].join('\n')
const bodyFile = join(tmp, 'body.md')
writeFileSync(bodyFile, body)

// Replace any prior bot evidence comment.
try {
  const ids = execFileSync(
    'gh',
    ['api', `repos/${GITHUB_REPOSITORY}/issues/${PR_NUMBER}/comments`, '--paginate', '--jq', `.[] | select((.body | contains("${MARKER}")) and (.user.type == "Bot")) | .id`],
    { encoding: 'utf8' },
  )
    .trim()
    .split('\n')
    .filter(Boolean)
  for (const id of ids) {
    execFileSync('gh', ['api', '--method', 'DELETE', `repos/${GITHUB_REPOSITORY}/issues/comments/${id}`], { stdio: 'inherit' })
  }
} catch (e) {
  console.error('ci-post-evidence: could not prune prior evidence comments:', e.message)
}

const args = ['pr', 'comment', PR_NUMBER, '--repo', GITHUB_REPOSITORY, '--body-file', bodyFile]
for (const a of attachments) args.push('--attach', a)
execFileSync('gh', args, { stdio: 'inherit' })
console.log(`ci-post-evidence: posted to PR #${PR_NUMBER} with ${attachments.length} attachment(s)`)
