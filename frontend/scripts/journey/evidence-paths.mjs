/* Where the evidence job finds each screenshot and clip that report.json records.
 *
 * The journey job runs inside the Playwright container, so report.json records
 * container paths (/__w/<repo>/<repo>/frontend/scripts/journey/.artifacts/...).
 * The evidence job runs on the host and downloads the same artifact into its own
 * checkout, where those paths do not exist. A recorded path that does not exist
 * is re-rooted: the part after the last /.artifacts/ segment is joined to the
 * local artifacts directory. Only files inside that directory are ever returned.
 *
 * The cases live in testdata/evidence-paths.yaml.
 */
import { existsSync } from 'node:fs'
import { isAbsolute, join, relative, sep } from 'node:path'

const SEGMENT = '/.artifacts/'
const IMAGE = 'image/png'
const VIDEO = 'video/webm'

// The local file a recorded attachment path names, or null when there is none.
export const resolveAttachmentPath = (recorded, artifactsDir, exists = existsSync) => {
  if (!recorded) return null
  if (exists(recorded)) return recorded
  const at = recorded.lastIndexOf(SEGMENT)
  if (at < 0) return null
  const local = join(artifactsDir, recorded.slice(at + SEGMENT.length))
  const inside = relative(artifactsDir, local)
  if (!inside || inside === '..' || inside.startsWith(`..${sep}`) || isAbsolute(inside)) return null
  return exists(local) ? local : null
}

// Per test, in report order: the first screenshot and the first clip that
// resolve. `listed` counts the media attachments the report records and
// `resolved` how many of them are on disk. `problem` is the note for the job
// annotations and the comment when the report lists media and none resolves.
export const collectMedia = (tests, artifactsDir, exists = existsSync) => {
  let listed = 0
  let resolved = 0
  const media = tests.map((test) => {
    const found = { [IMAGE]: null, [VIDEO]: null }
    for (const a of test.attachments || []) {
      if (!(a.contentType in found) || !a.path) continue
      listed++
      const local = resolveAttachmentPath(a.path, artifactsDir, exists)
      if (!local) continue
      resolved++
      found[a.contentType] ??= local
    }
    return { image: found[IMAGE], video: found[VIDEO] }
  })
  const problem =
    listed && !resolved ? `no screenshots or clips: the report lists ${listed}, but none of them is in the downloaded artifact.` : null
  return { listed, resolved, problem, media }
}
