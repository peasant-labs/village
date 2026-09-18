/* Post journey evidence to the pull request.
 *
 * Builds one table per journey case with four media columns, dark and light side
 * by side:
 *
 *   | journey | dark image | dark video | light image | light video |
 *
 * Images render inline; video cannot render inside a table cell on GitHub, so a
 * clip cell links to the uploaded clip.
 *
 * Media URLs come from GitHub's user-attachments endpoint (the one `gh --attach`
 * uses) uploaded directly with a user credential. That avoids `gh`'s 50-file
 * per-comment cap (14 journeys x 2 themes x image+video is 56 files), which would
 * otherwise force several comments. If a direct upload is unavailable, it falls
 * back to `gh --attach` tables, then to a text-only comment.
 *
 * env: GH_TOKEN (primary, must be a user token to upload), JOURNEY_APP_TOKEN
 *      (fallback), PR_NUMBER, GITHUB_REPOSITORY, GITHUB_SERVER_URL, GITHUB_RUN_ID
 */
import { execFileSync } from 'node:child_process'
import { existsSync, globSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import { homedir, tmpdir } from 'node:os'
import { basename, dirname, extname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const REPORT = join(HERE, '.artifacts', 'report.json')
const MARKER = 'journey-evidence'
const UPLOAD_ORIGIN = 'https://uploads.github.com'

const { PR_NUMBER, GITHUB_REPOSITORY, GITHUB_SERVER_URL = 'https://github.com', GITHUB_RUN_ID } = process.env
if (!PR_NUMBER || !GITHUB_REPOSITORY) {
  console.error('ci-post-evidence: PR_NUMBER and GITHUB_REPOSITORY are required')
  process.exit(1)
}
if (!existsSync(REPORT)) {
  console.error(`ci-post-evidence: no report at ${REPORT}; skipping evidence`)
  process.exit(0)
}

const TOKENS = [...new Set([process.env.GH_TOKEN, process.env.JOURNEY_APP_TOKEN].filter(Boolean))]
const canAttach = (token) => /^(gh[opu]_|github_pat_)/.test(token)
const runGh = (args, token, opts = {}) =>
  execFileSync('gh', args, { ...opts, env: { ...process.env, GH_TOKEN: token } })

const walkTests = (report) => {
  const tests = []
  const walk = (suite) => {
    for (const spec of suite.specs || []) {
      for (const t of spec.tests || []) {
        const result = t.results?.[0] || {}
        tests.push({ title: spec.title, file: spec.file, project: t.projectName, status: result.status, attachments: result.attachments || [] })
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

const pathFor = (test, contentType) => {
  const a = (test.attachments || []).find((x) => x.contentType === contentType && x.path && existsSync(x.path))
  return a ? a.path : null
}
const label = (t) => `${basename(t.file || 'journey').replace(/\.[^.]+$/, '')} › ${t.title}`
const media = tests.map((t) => ({ label: label(t), theme: t.project, image: pathFor(t, 'image/png'), video: pathFor(t, 'video/webm') }))
const labels = [...new Set(media.map((m) => m.label))]
const themes = [...new Set(media.map((m) => m.theme))].sort()
const cell = (l, theme) => media.find((m) => m.label === l && m.theme === theme)

// Convert clips to H.264 mp4 for the widest playback support (webm as fallback).
const ffmpeg = (() => {
  const roots = [process.env.PLAYWRIGHT_BROWSERS_PATH, join(homedir(), '.cache', 'ms-playwright')].filter(Boolean)
  for (const root of roots) {
    const found = [...globSync(`${root}/ffmpeg-*/ffmpeg-linux`), ...globSync(`${root}/ffmpeg-*/ffmpeg`)]
    if (found.length) return found[0]
  }
  return null
})()
const tmp = mkdtempSync(join(tmpdir(), 'journey-evidence-'))
const clipPath = (webm) => {
  if (!ffmpeg) return webm
  const out = join(tmp, `${basename(webm, extname(webm))}.mp4`)
  try {
    execFileSync(ffmpeg, ['-y', '-loglevel', 'error', '-i', webm, '-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-movflags', '+faststart', out])
    return out
  } catch {
    return webm
  }
}

const runUrl = GITHUB_RUN_ID ? `${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}/actions/runs/${GITHUB_RUN_ID}` : null
const heading = (suffix) => `## workflow ${GITHUB_RUN_ID || 'unknown'}: journey ${suffix}`

const summaryLines = () => [
  heading(`results, ${failed.length ? `${failed.length} failed` : 'all passed'}`),
  '',
  `**${passed.length} passed**, **${failed.length} failed**, ${skipped.length} skipped (both themes).`,
  ...(failed.length ? ['', 'Failing:', ...failed.map((f) => `- [${f.project}] ${f.title}`)] : []),
  '',
  runUrl ? `Full traces and artifacts: [workflow run](${runUrl})` : 'Full traces and artifacts are attached to the workflow run.',
]

// Upload a file to GitHub's user-attachments endpoint and return its URL.
const uploadAsset = async (path, contentType, token, repositoryId) => {
  const url = new URL('/user-attachments/assets', UPLOAD_ORIGIN)
  url.searchParams.set('name', basename(path))
  url.searchParams.set('content_type', contentType)
  url.searchParams.set('repository_id', String(repositoryId))
  const res = await fetch(url, {
    method: 'POST',
    headers: { authorization: `Bearer ${token}`, accept: 'application/vnd.github+json', 'content-type': 'application/octet-stream' },
    body: readFileSync(path),
  })
  if (!res.ok) throw new Error(`upload of ${basename(path)} failed: ${res.status} ${await res.text()}`)
  const json = await res.json()
  if (!json.url) throw new Error(`upload of ${basename(path)} returned no url`)
  return json.url
}

const buildFourColumnTable = (imageUrls, videoUrls) => {
  const header = '| journey | dark image | dark video | light image | light video |'
  const sep = '|---|---|---|---|---|'
  const rows = labels.map((l) => {
    const cells = ['dark', 'light'].map((theme) => {
      const m = cell(l, theme)
      const img = m?.image ? imageUrls.get(m.image) : null
      const vid = m?.video ? videoUrls.get(m.video) : null
      return [img ? `![dark](${img})` : '—', vid ? `[clip](${vid})` : '—'].join(' | ')
    })
    return `| ${l} | ${cells.join(' | ')} |`
  })
  return [`<!-- ${MARKER} -->`, ...summaryLines(), '', header, sep, ...rows].join('\n')
}

// Replace prior evidence comments (any marker variant) authored by a bot or the
// acting login.
const pruneTokens = [...new Set([process.env.JOURNEY_APP_TOKEN, ...TOKENS].filter(Boolean))]
for (const token of pruneTokens) {
  try {
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
    let failedDeletes = 0
    for (const id of ids) {
      try {
        runGh(['api', '--method', 'DELETE', `repos/${GITHUB_REPOSITORY}/issues/comments/${id}`], token, { stdio: 'inherit' })
      } catch (e) {
        failedDeletes++
        console.error(`ci-post-evidence: could not delete prior comment ${id}: ${e.message}`)
      }
    }
    if (failedDeletes === 0) break
  } catch (e) {
    console.error(`ci-post-evidence: prune with ${token.slice(0, 4)}… failed: ${e.message}`)
  }
}

const postRest = (token, bodyText) => {
  const bodyFile = join(tmp, `body-${Math.random().toString(36).slice(2)}.md`)
  writeFileSync(bodyFile, bodyText)
  runGh(['api', '--method', 'POST', `repos/${GITHUB_REPOSITORY}/issues/${PR_NUMBER}/comments`, '-F', `body=@${bodyFile}`], token, { stdio: 'inherit' })
}

const postComment = (token, bodyText, attachments) => {
  const bodyFile = join(tmp, `body-${Math.random().toString(36).slice(2)}.md`)
  writeFileSync(bodyFile, bodyText)
  const args = ['pr', 'comment', PR_NUMBER, '--repo', GITHUB_REPOSITORY, '--body-file', bodyFile]
  for (const a of attachments) args.push('--attach', a)
  runGh(args, token, { stdio: 'inherit' })
}

const attachToken = TOKENS.find(canAttach)
let posted = false

if (attachToken) {
  try {
    const repositoryId = runGh(['api', `repos/${GITHUB_REPOSITORY}`, '--jq', '.id'], attachToken, { encoding: 'utf8' }).trim()
    const imageUrls = new Map()
    for (const [l, theme] of labels.flatMap((l) => themes.map((t) => [l, t]))) {
      const m = cell(l, theme)
      if (m?.image) imageUrls.set(m.image, await uploadAsset(m.image, 'image/png', attachToken, repositoryId))
    }
    const videoUrls = new Map()
    for (const [l, theme] of labels.flatMap((l) => themes.map((t) => [l, t]))) {
      const m = cell(l, theme)
      if (m?.video) {
        const p = clipPath(m.video)
        videoUrls.set(m.video, await uploadAsset(p, p.endsWith('.mp4') ? 'video/mp4' : 'video/webm', attachToken, repositoryId))
      }
    }
    postRest(attachToken, buildFourColumnTable(imageUrls, videoUrls))
    console.log(`ci-post-evidence: posted a four-column table (${imageUrls.size} images, ${videoUrls.size} clips)`)
    posted = true
  } catch (e) {
    console.error(`ci-post-evidence: direct upload/table failed: ${e.message}`)
  }
}

// Fallback: gh --attach tables (images inline, clips as links), split across two
// comments to stay under gh's 50-file cap.
if (!posted && attachToken) {
  try {
    const header = `| journey | ${themes.join(' | ')} |`
    const sep = `|---|${themes.map(() => '---').join('|')}|`
    const imageRows = labels.map((l) => `| ${l} | ${themes.map((t) => { const m = cell(l, t); return m?.image ? `![${t}](${m.image})` : '—' }).join(' | ')} |`)
    const videoRows = labels.map((l) => `| ${l} | ${themes.map((t) => { const m = cell(l, t); return m?.video ? `[${t}](${clipPath(m.video)})` : '—' }).join(' | ')} |`)
    const imageAttachments = labels.flatMap((l) => themes.map((t) => cell(l, t)?.image).filter(Boolean))
    const videoAttachments = labels.flatMap((l) => themes.map((t) => cell(l, t)?.video).filter(Boolean)).map(clipPath)
    postComment(attachToken, [`<!-- ${MARKER} -->`, ...summaryLines(), '', header, sep, ...imageRows].join('\n'), imageAttachments)
    if (videoAttachments.length) {
      postComment(attachToken, [`<!-- ${MARKER}-videos -->`, heading('clips'), '', header, sep, ...videoRows].join('\n'), videoAttachments)
    }
    console.log('ci-post-evidence: posted two-table fallback evidence')
    posted = true
  } catch (e) {
    console.error(`ci-post-evidence: attachment fallback failed: ${e.message}`)
  }
}

if (!posted) {
  for (const token of TOKENS) {
    try {
      postRest(token, [`<!-- ${MARKER} -->`, ...summaryLines()].join('\n'))
      console.log('ci-post-evidence: posted text-only evidence')
      posted = true
      break
    } catch (e) {
      console.error(`ci-post-evidence: text post with ${token.slice(0, 4)}… failed: ${e.message}`)
    }
  }
}
if (!posted) {
  console.error('ci-post-evidence: could not post evidence with any configured token')
  process.exit(1)
}
