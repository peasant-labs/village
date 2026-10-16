import { spawn } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { existsSync, mkdirSync, readFileSync, readdirSync, renameSync, rmSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'

export async function bounded(promise, ms, operation) {
  let timer
  try {
    return await Promise.race([promise, new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error(`${operation} exceeded ${ms}ms; inspect retained build ownership before retrying`)), ms)
    })])
  } finally { clearTimeout(timer) }
}

/** Exactly one settlement per spawn, also used by cleanup to reap direct children. */
function start(command, args, options) {
  const child = spawn(command, args, options)
  const settled = new Promise((resolve) => {
    let finished = false
    const finish = (result) => { if (!finished) { finished = true; resolve(result) } }
    child.once('error', (error) => finish({ error }))
    child.once('exit', (code, signal) => finish({ code, signal }))
  })
  const done = settled.then(({ code, signal, error }) => {
    if (error) throw error
    if (code !== 0) throw new Error(`${command} ${args.join(' ')} exited ${code ?? signal}`)
  })
  done.catch(() => {})
  return { child, settled, done }
}

const stat = (pid) => readFileSync(`/proc/${pid}/stat`, 'utf8').split(') ')[1].split(' ')
const startTime = (pid) => stat(pid)[19]
function liveIdentity(identity) {
  try { const fields = stat(identity.pid); return fields[0] !== 'Z' && fields[19] === identity.start } catch { return false }
}

/** One bounded /proc snapshot; only the random inherited marker proves ownership. */
export function ownedGroups(owner) {
  const groups = new Set()
  for (const pid of readdirSync('/proc').filter((value) => /^\d+$/.test(value))) {
    try {
      const fields = stat(pid)
      if (fields[0] !== 'Z' && readFileSync(`/proc/${pid}/environ`, 'utf8').split('\0').includes(`COLLECTIVES_BUILD_OWNER=${owner}`)) {
        groups.add(Number(fields[2]))
      }
    } catch { /* process exited during the snapshot */ }
  }
  return groups
}

function signalGroups(groups, signal) {
  for (const group of groups) {
    try { process.kill(-group, signal) } catch (error) { if (error.code !== 'ESRCH') throw error }
  }
}

async function waitAbsent(owner, milliseconds) {
  const deadline = Date.now() + milliseconds
  do {
    if (ownedGroups(owner).size === 0) return true
    await delay(50)
  } while (Date.now() < deadline)
  return ownedGroups(owner).size === 0
}

async function reap(owner, children, { termMs, killMs }) {
  signalGroups(ownedGroups(owner), 'SIGTERM')
  if (!await waitAbsent(owner, termMs)) {
    signalGroups(ownedGroups(owner), 'SIGKILL')
    if (!await waitAbsent(owner, killMs)) throw new Error('Collective build cleanup could not prove all marker-owned groups exited after KILL; ownership metadata retained for recovery; inspect those groups before retrying')
  }
  await bounded(Promise.all(children.map((child) => child.settled)), killMs, 'collective build child reaping')
  if (ownedGroups(owner).size) throw new Error('Collective build cleanup still sees owned processes after child reaping; metadata retained; inspect the owned groups before retrying')
}

/** Linux util-linux flock serializes the ENTIRE recovery/acquisition lifetime.
 * The gate inode is never unlinked. A pipe EOF releases it after owner SIGKILL.
 */
export async function acquireBuildLifecycle(directory, { termMs = 2000, killMs = 2000 } = {}) {
  const gate = start('flock', ['--wait', '0.5', '--conflict-exit-code', '73', `${directory}.gate`, process.execPath, '-e',
    "process.stdout.write('locked\\n'); process.stdin.resume(); process.stdin.on('end', () => process.exit(0))"], { stdio: ['pipe', 'pipe', 'inherit'] })
  let announced = false
  const ready = new Promise((resolve) => gate.child.stdout.once('data', (data) => { announced = data.toString().trim() === 'locked'; resolve() }))
  try {
    await bounded(Promise.race([ready, gate.settled.then(() => { throw new Error('Collective production build ownership is busy or flock is unavailable; no metadata was changed; wait for the owner or install util-linux flock') })]), 3000, 'collective build flock acquisition')
    if (!announced) throw new Error('Collective build flock did not confirm ownership; no build may start')
  } catch (error) {
    gate.child.stdin.destroy(); gate.child.kill('SIGTERM')
    await bounded(gate.settled, 2000, 'failed lock keeper exit')
    throw error
  }
  const identity = Object.freeze({ pid: process.pid, start: startTime(process.pid), owner: randomUUID() })
  const children = []
  let stopping
  try {
    if (existsSync(directory)) {
      const path = join(directory, 'owner.json')
      if (existsSync(path)) {
        const previous = JSON.parse(readFileSync(path, 'utf8'))
        if (!Number.isSafeInteger(previous.pid) || typeof previous.start !== 'string' || !/^[a-f0-9-]{36}$/.test(previous.owner)) throw new Error('Collective build recovery identity is invalid; metadata retained; inspect owner.json before retrying')
        if (liveIdentity(previous)) throw new Error('A live collective build still owns .next; metadata retained; wait for its completion')
        await reap(previous.owner, [], { termMs, killMs })
      }
      // With flock held, no contender can replace this directory while reaping.
      // Missing owner.json means identity publication was interrupted BEFORE any
      // workload could spawn. A temporary file is never a published identity.
      rmSync(directory, { recursive: true })
    }
    mkdirSync(directory)
    const temporary = join(directory, `owner-${identity.owner}.tmp`)
    writeFileSync(temporary, JSON.stringify(identity), { flag: 'wx', mode: 0o600 })
    renameSync(temporary, join(directory, 'owner.json'))
  } catch (error) {
    gate.child.stdin.end()
    await bounded(gate.settled, 2000, 'recovery lock keeper exit')
    throw error
  }
  return {
    identity,
    launch(command, args, options = {}) {
      if (stopping || gate.child.exitCode !== null || gate.child.signalCode !== null) throw new Error('Collective build ownership is stopping or its flock keeper exited; no new process may start')
      const child = start(command, args, { ...options, detached: true, env: { ...process.env, ...options.env, COLLECTIVES_BUILD_OWNER: identity.owner } })
      children.push(child)
      return child
    },
    cleanup() {
      if (stopping) return stopping
      stopping = (async () => {
        try {
          await reap(identity.owner, children, { termMs, killMs })
          rmSync(directory, { recursive: true })
        } finally {
          // On failure leave immutable metadata, but let the next invocation
          // acquire the gate and retry recovery after this process exits.
          gate.child.stdin.end()
          await bounded(gate.settled, 2000, 'collective lock keeper exit')
        }
      })()
      return stopping
    },
  }
}
