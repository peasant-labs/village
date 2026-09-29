/* Where the evidence job finds each screenshot and clip that report.json records,
 * and which screenshot and clip each journey posts.
 *
 * The journey job runs inside the Playwright container, so report.json records
 * container paths (/__w/<repo>/<repo>/frontend/scripts/journey/.artifacts/...).
 * The evidence job runs on the host and downloads the same artifact into its own
 * checkout, where those paths do not exist. A recorded path that exists (a local
 * run) is used as it is. One that does not is re-rooted: the part after the last
 * segment named like the local artifacts directory is joined to that directory.
 *
 * This trusts report.json: any existing path it names is posted. That holds while
 * the evidence job runs under pull_request, where report.json and this script come
 * from the same pull request. Before it runs under pull_request_target or
 * workflow_run, confine resolved paths to the artifacts directory (realpath both
 * sides) and reject recorded paths outside it.
 *
 * The cases live in testdata/evidence-paths.yaml.
 */
import { existsSync } from 'node:fs'
import { basename, join } from 'node:path'

const IMAGE = 'image/png'
const VIDEO = 'video/webm'
// Playwright records the test's own page as video.webm and each later page, such
// as the blank page axe opens, as video-1.webm, video-2.webm and so on.
const PAGE_CLIP = 'video.webm'

// The local file a recorded attachment path names, or null when there is none.
export const resolveAttachmentPath = (recorded, artifactsDir, exists) => {
  if (exists(recorded)) return recorded
  const segment = `/${basename(artifactsDir)}/`
  const at = recorded.lastIndexOf(segment)
  if (at < 0) return null
  const local = join(artifactsDir, recorded.slice(at + segment.length))
  return exists(local) ? local : null
}

// One record per test, in report order: the test, its first screenshot that
// resolves, and the clip of its own page (else the first clip that resolves).
// `listed` counts the media attachments that name a file and `resolved` how many
// of those are on disk. `problem` is the note for the job annotations and the
// comment when the report lists media and none of it resolves.
export const collectMedia = (tests, artifactsDir, exists = existsSync) => {
  let listed = 0
  let resolved = 0
  const media = tests.map((test) => {
    const found = { [IMAGE]: [], [VIDEO]: [] }
    for (const a of test.attachments || []) {
      if (!Object.hasOwn(found, a.contentType) || !a.path) continue
      listed++
      const local = resolveAttachmentPath(a.path, artifactsDir, exists)
      if (!local) continue
      resolved++
      found[a.contentType].push(local)
    }
    const clips = found[VIDEO]
    return {
      test,
      image: found[IMAGE][0] ?? null,
      video: clips.find((p) => basename(p) === PAGE_CLIP) ?? clips[0] ?? null,
    }
  })
  const problem =
    listed && !resolved ? `no screenshots or clips: the report lists ${listed}, but none of them is in the downloaded artifact.` : null
  return { listed, resolved, problem, media }
}
