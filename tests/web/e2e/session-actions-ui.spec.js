// e2e/session-actions-ui.spec.js -- UI wiring of the sidebar row action
// buttons (Start / Stop / Restart / Fork / Delete / Worktree-finish).
//
// The REST endpoints themselves are covered by parity-actions.spec.js and
// worktree-finish.spec.js; close (Shift+D) is covered by close-undo.spec.js.
// THIS spec covers the click → API → SSE round-trip through the buttons that
// Sidebar.js SessionItem renders.
//
// Grounded in the fixture seed (tests/web/fixtures/cmd/web-fixture/main.go):
//   sess-001 "agent-deck"    status=idle    worktreeBranch=feat/fixture (worktree row)
//   sess-002 "frontend"      status=running
//   sess-003 "innotrade-api" status=idle
//   sess-004 "scratch"       status=idle
//
// Source audit notes (assertions pinned to these):
//   - Row actions are intentionally collapsed behind the More button so the
//     hover affordance never covers the clickable title area.
//   - Status renders as `<span class="dot <status>">` (icons.js Dot); the
//     fixture transitions are start/restart→running, stop→stopped.
//   - Fork button only renders when `s.canFork` (Sidebar.js). The fixture
//     seeds sess-001 with CanFork=true and leaves the other rows false.
//     Fork now opens the web parity dialog before POSTing options.
//   - Delete + worktree-finish route through ConfirmDialog.js whose confirm
//     button is always labeled "Delete" and cancel is "Cancel".
//
// Phone (<768px) skips: sidebar rows are desktop/tablet-only (same pattern
// as keyboard-parity.spec.js / skills.spec.js).

import { test, expect } from '@playwright/test'

// The app registers a service worker (sw.js) whose fetch handler takes over
// page requests once active. Block SWs for this file so mutation requests and
// SSE assertions remain deterministic.
test.use({ serviceWorkers: 'block' })

const SEEDED_COUNT = 4

function rowFor(page, title) {
  return page.locator('.sess', { has: page.locator('.tt', { hasText: title }) })
}

async function openMore(row) {
  await row.hover()
  const more = row.locator('[data-testid="session-more-btn"]')
  await more.focus()
  await expect(row.locator('[data-testid="session-more-menu"]')).toBeVisible()
}

async function clickMoreAction(row, testId) {
  await openMore(row)
  await row.locator(`[data-testid="${testId}"]`).click()
}

async function gotoSidebar(page) {
  await page.goto('/')
  await expect(page.locator('.sess')).toHaveCount(SEEDED_COUNT, { timeout: 5000 })
}

test.describe('sidebar session action buttons', () => {
  test.skip(({ viewport }) => (viewport?.width || 1280) < 768, 'phone viewport: sidebar action buttons are desktop/tablet only')

  test.beforeEach(async ({ request }) => {
    await request.post('/__fixture/reset')
  })

  test('Start button on an idle session flips the status pill to running (SSE round-trip)', async ({ page }) => {
    await gotoSidebar(page)
    const row = rowFor(page, 'scratch') // sess-004, seeded idle
    await expect(row.locator('.dot.idle')).toHaveCount(1)

    await clickMoreAction(row, 'session-start-btn')

    // Fixture StartSession → status=running; notifyMenuChanged pushes the
    // new snapshot over SSE and the Dot re-renders.
    await expect(row.locator('.dot.running')).toHaveCount(1, { timeout: 4000 })
    // The start/stop slot is status-driven: running rows render Stop.
    await expect(row.locator('[data-testid="session-stop-btn"]')).toHaveCount(1)
    await expect(row.locator('[data-testid="session-start-btn"]')).toHaveCount(0)
  })

  test('Stop button on the running session changes the status pill', async ({ page, request }) => {
    await gotoSidebar(page)
    const row = rowFor(page, 'frontend') // sess-002, the only seeded running session
    await expect(row.locator('.dot.running')).toHaveCount(1)

    await clickMoreAction(row, 'session-stop-btn')

    // Fixture StopSession → status=stopped (parity-actions pins the API).
    await expect(row.locator('.dot.stopped')).toHaveCount(1, { timeout: 4000 })
    // Non-running/waiting rows render Start again.
    await expect(row.locator('[data-testid="session-start-btn"]')).toHaveCount(1)

    const snap = await (await request.get('/__fixture/snapshot')).json()
    const sess = snap.items.find(i => i.session && i.session.id === 'sess-002')
    expect(sess.session.status).toBe('stopped')
  })

  test('Restart button renders on every row and sets status to running', async ({ page }) => {
    await gotoSidebar(page)
    // Source: Restart is unconditional in SessionItem — one per session row.
    await expect(page.locator('[data-testid="session-restart-btn"]')).toHaveCount(SEEDED_COUNT)

    const row = rowFor(page, 'innotrade-api') // sess-003, seeded idle
    await clickMoreAction(row, 'session-restart-btn')
    await expect(row.locator('.dot.running')).toHaveCount(1, { timeout: 4000 })
  })

  test('hover action affordance does not block clicking the session title', async ({ page }) => {
    await gotoSidebar(page)
    const row = rowFor(page, 'scratch')

    await row.hover()
    await expect(row.locator('[data-testid="session-more-btn"]')).toBeVisible()

    await row.locator('.titleline').click()
    await expect(row).toHaveClass(/\bsel\b/)
    await expect(page.locator('.work-head .cur')).toHaveText('scratch')
  })

  test('Fork button is hidden for non-forkable seeded sessions (canFork=false)', async ({ page }) => {
    await gotoSidebar(page)
    await expect(rowFor(page, 'scratch').locator('[data-testid="session-fork-btn"]')).toHaveCount(0)
  })

  test('Fork button (canFork=true) POSTs fork; child is titled "<title> (fork)" by the server', async ({ page, request }) => {
    await gotoSidebar(page)
    const row = rowFor(page, 'agent-deck') // sess-001
    await expect(row.locator('[data-testid="session-fork-btn"]')).toHaveCount(1)

    const forkResponse = page.waitForResponse(
      r => r.url().includes('/api/sessions/sess-001/fork') && r.request().method() === 'POST',
    )
    await clickMoreAction(row, 'session-fork-btn')
    const dialog = page.locator('[role="dialog"]', { hasText: 'Fork session' })
    await expect(dialog).toBeVisible()
    await dialog.getByRole('button', { name: /Fork session/ }).click()
    expect((await forkResponse).status()).toBe(200)

    // Server-side truth via fixture snapshot: the dialog's default title is
    // "agent-deck (fork)" and the options mutator preserves it.
    const snap = await (await request.get('/__fixture/snapshot')).json()
    const child = snap.items.find(i => i.session && i.session.parentSessionId === 'sess-001')
    expect(child).toBeTruthy()
    expect(child.session.title).toBe('agent-deck (fork)')
  })

  test('Delete button: ConfirmDialog message; Cancel keeps the row, Confirm removes it', async ({ page, request }) => {
    await gotoSidebar(page)
    const row = rowFor(page, 'scratch') // sess-004

    // Open the confirm dialog.
    await clickMoreAction(row, 'session-delete-btn')
    const dialog = page.locator('[role="dialog"]')
    await expect(dialog).toBeVisible()
    // Exact copy from Sidebar.js doAction('delete').
    await expect(dialog).toContainText('Delete session "scratch"? This stops the tmux session and removes metadata.')

    // Cancel → no mutation: row still there, fixture untouched.
    await dialog.getByRole('button', { name: 'Cancel' }).click()
    await expect(dialog).toHaveCount(0)
    await expect(page.locator('.sess')).toHaveCount(SEEDED_COUNT)
    let snap = await (await request.get('/__fixture/snapshot')).json()
    expect(snap.items.some(i => i.session && i.session.id === 'sess-004')).toBe(true)

    // Re-open and confirm (ConfirmDialog's confirm button is labeled "Delete").
    await clickMoreAction(row, 'session-delete-btn')
    await expect(dialog).toBeVisible()
    await dialog.getByRole('button', { name: 'Delete' }).click()

    // SSE refresh drops the row; fixture no longer has sess-004.
    await expect(page.locator('.sess')).toHaveCount(SEEDED_COUNT - 1, { timeout: 4000 })
    await expect(rowFor(page, 'scratch')).toHaveCount(0)
    snap = await (await request.get('/__fixture/snapshot')).json()
    expect(snap.items.some(i => i.session && i.session.id === 'sess-004')).toBe(false)
  })

  test('Worktree finish button shows merge confirm; confirm removes the session row', async ({ page, request }) => {
    await gotoSidebar(page)
    // Only sess-001 is seeded with worktreeBranch+worktreeRepoRoot, so
    // exactly one row carries the ⎇✓ button (dataModel.js worktree gate).
    const finishBtns = page.locator('[data-action="worktree-finish"]')
    await expect(finishBtns).toHaveCount(1)

    const row = rowFor(page, 'agent-deck')
    await clickMoreAction(row, 'session-worktree-finish-btn')

    const dialog = page.locator('[role="dialog"]')
    await expect(dialog).toBeVisible()
    // Exact copy from Sidebar.js doAction('worktreeFinish'), including the
    // seeded branch name — the dialog must mention the merge.
    await expect(dialog).toContainText('Finish worktree for "agent-deck"?')
    await expect(dialog).toContainText('Merges branch "feat/fixture" into default branch')

    // Confirm (generic ConfirmDialog confirm label is "Delete"); the fixture
    // FinishWorktree removes the session, SSE refresh drops the row.
    await dialog.getByRole('button', { name: 'Delete' }).click()
    await expect(page.locator('.sess')).toHaveCount(SEEDED_COUNT - 1, { timeout: 4000 })
    await expect(rowFor(page, 'agent-deck')).toHaveCount(0)
    const snap = await (await request.get('/__fixture/snapshot')).json()
    expect(snap.items.some(i => i.session && i.session.id === 'sess-001')).toBe(false)
  })
})
