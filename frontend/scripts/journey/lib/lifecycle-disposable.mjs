// Disposable direct lifecycle test peer, never an application or remote fixture.
import { acquireBuildLifecycle } from './built-lifecycle.mjs'

const [directory, mode] = process.argv.slice(2)
process.stdin.resume()
// Bound disposable peer lifetime even if the test runner itself is SIGKILLed.
const deadline = setTimeout(() => process.exit(3), 15_000)
deadline.unref()
await new Promise((resolve) => process.stdin.once('data', resolve))
try {
  const lifecycle = await acquireBuildLifecycle(directory, { termMs: 100, killMs: 2000 })
  if (mode === 'orphan') {
    const workload = lifecycle.launch(process.execPath, ['-e', "process.on('SIGTERM',()=>{}); process.stdout.write('ready\\n'); setTimeout(()=>process.exit(3),15000)"], { stdio: ['ignore', 'pipe', 'inherit'] })
    await new Promise((resolve) => workload.child.stdout.once('data', resolve))
  }
  process.stdout.write(`${JSON.stringify(lifecycle.identity)}\n`)
  await new Promise((resolve) => process.stdin.once('end', resolve))
  await lifecycle.cleanup()
} catch (error) {
  console.error(error.message)
  process.exitCode = 2
  process.stdin.destroy()
}
