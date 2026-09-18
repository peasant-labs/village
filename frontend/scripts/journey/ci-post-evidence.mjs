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

// Tokens to try, most capable first: the machine-account PAT (can upload inline
// media) and the App installation token (can post text). Distinct values only.
const TOKENS = [...new Set([process.env.GH_TOKEN, process.env.JOURNEY_APP_TOKEN].filter(Boolean))]
const canAttach = (token) => /^(gh[opu]_|github_pat_)/.test(token)
const runGh = (args, token, opts = {}) =>
  execFileSync('gh', args, { ...opts, env: { ...process.env, GH_TOKEN: token } })

// Replace prior evidence comments authored by either identity. Prefer the App
// token: it authored its own comments and reliably has Issues/Pull requests
// write, so cleanup works even when the PAT cannot reach the repo.
const pruneTokens = [...new Set([process.env.JOURNEY_APP_TOKEN, ...TOKENS].filter(Boolean))]
for (const token of pruneTokens) {
  try {
    // Installation tokens cannot call /user; match bot-authored comments instead.
    const isInstallation = token.startsWith('ghs_')
    const login = isInstallation ? null : runGh(['api', 'user', '--jq', '.login'], token, { encoding: 'utf8' }).trim()
    const authorMatch = login ? `((.user.login == "${login}") or (.user.type == "Bot"))` : '(.user.type == "Bot")'
    const ids = runGh(
      ['api', `repos/${GITHUB_REPOSITORY}/issues/${PR_NUMBER}/comments`, '--paginate', '--jq', `.[] | select((.body | contains("${MARKER}")) and ${authorMatch}) | .id`],
      token,
      { encoding: 'utf8' },
    )
      .trim()
      .split('\n')
      .filter(Boolean)
    let failed = 0
    for (const id of ids) {
      try {
        runGh(['api', '--method', 'DELETE', `repos/${GITHUB_REPOSITORY}/issues/comments/${id}`], token, { stdio: 'inherit' })
      } catch (e) {
        failed++
        console.error(`ci-post-evidence: could not delete prior comment ${id}: ${e.message}`)
      }
    }
    if (failed === 0) break
  } catch (e) {
    console.error(`ci-post-evidence: prune with ${token.slice(0, 4)}… failed: ${e.message}`)
  }
}

const postAttach = (token) => {
  const args = ['pr', 'comment', PR_NUMBER, '--repo', GITHUB_REPOSITORY, '--body-file', bodyFile]
  for (const a of attachments) args.push('--attach', a)
  return runGh(args, token, { stdio: 'inherit' })
}

const postRest = (token) =>
  runGh(
    ['api', '--method', 'POST', `repos/${GITHUB_REPOSITORY}/issues/${PR_NUMBER}/comments`, '-F', `body=@${bodyFile}`],
    token,
    { stdio: 'inherit' },
  )

// Try each token: inline attachments where the credential allows, then a text
// comment. Falls through so a misconfigured PAT never suppresses the report.
let posted = false
for (const token of TOKENS) {
  if (attachments.length && canAttach(token)) {
    try {
      postAttach(token)
      posted = true
      break
    } catch (e) {
      console.error(`ci-post-evidence: attach with ${token.slice(0, 4)}… failed: ${e.message}`)
    }
  }
  try {
    postRest(token)
    posted = true
    break
  } catch (e) {
    console.error(`ci-post-evidence: REST post with ${token.slice(0, 4)}… failed: ${e.message}`)
  }
}
if (!posted) {
  console.error('ci-post-evidence: could not post evidence with any configured token')
  process.exit(1)
}
console.log('ci-post-evidence: posted evidence comment')
