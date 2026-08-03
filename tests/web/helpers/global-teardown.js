// helpers/global-teardown.js -- Playwright global teardown
//
// Reads the PID file written by global-setup.js and kills the fixture process.

import { readFileSync, existsSync, rmSync, unlinkSync } from 'node:fs'
import { resolve } from 'node:path'

const REPO_ROOT = resolve(import.meta.dirname, '..', '..', '..')
const PID_PATH = resolve(REPO_ROOT, 'tests/web/.tmp/web-fixture.pid')
const CLAUDE_CONFIG_DIR = resolve(REPO_ROOT, 'tests/web/.tmp/claude-config')

export default async function globalTeardown() {
  if (!existsSync(PID_PATH)) {
    rmSync(CLAUDE_CONFIG_DIR, { recursive: true, force: true })
    return
  }
  const pid = parseInt(readFileSync(PID_PATH, 'utf8').trim(), 10)
  if (!Number.isFinite(pid)) {
    unlinkSync(PID_PATH)
    rmSync(CLAUDE_CONFIG_DIR, { recursive: true, force: true })
    return
  }
  try {
    process.kill(pid, 'SIGTERM')
    console.log(`[playwright] sent SIGTERM to web-fixture pid ${pid}`)
  } catch (err) {
    if (err && err.code !== 'ESRCH') {
      console.warn(`[playwright] could not signal web-fixture pid ${pid}:`, err.message)
    }
  }
  unlinkSync(PID_PATH)
  rmSync(CLAUDE_CONFIG_DIR, { recursive: true, force: true })
}
