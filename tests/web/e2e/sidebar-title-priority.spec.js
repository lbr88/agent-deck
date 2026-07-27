import { test, expect } from '@playwright/test'

test('long hub and provider metadata cannot collapse the session title', async ({ page }) => {
  const hubName = 'very-long-hub-node-name-that-must-yield-to-the-session-title'
  const providerName = 'opencode'
  let targetTitle = ''

  await page.addInitScript(() => {
    window.EventSource = class {
      addEventListener() {}
      close() {}
    }
  })
  await page.route('**/api/menu', async route => {
    const response = await route.fetch()
    const snapshot = await response.json()
    const item = snapshot.items.find(candidate => candidate.type === 'session')
    targetTitle = item.session.title
    item.session.source = 'hub'
    item.session.hubNodeId = 'node-long-name'
    item.session.hubNodeName = hubName
    item.session.tool = providerName
    await route.fulfill({ response, json: snapshot })
  })

  await page.goto('/')
  expect(targetTitle).not.toBe('')
  const row = page.locator('.sess').filter({ hasText: targetTitle }).first()
  await expect(row).toBeVisible()
  await expect(row.locator('.tag')).toHaveCount(2)
  await expect(row.locator('.tt')).toHaveAttribute('title', targetTitle)
  await expect(row.locator('.hub-tag')).toHaveAttribute('title', `Hub: ${hubName}`)
  await expect(row.locator('.tool-tag')).toHaveAttribute('title', `Provider: ${providerName}`)

  const dimensions = await row.evaluate(element => {
    const title = element.querySelector('.tt').getBoundingClientRect()
    const metadata = element.querySelector('.meta').getBoundingClientRect()
    const details = element.querySelector('.row-chev').getBoundingClientRect()
    return {
      titleWidth: title.width,
      metadataWidth: metadata.width,
      detailsWidth: details.width,
      detailsRight: details.right,
      metadataRight: metadata.right,
    }
  })

  expect(dimensions.metadataWidth).toBeLessThanOrEqual(84)
  expect(dimensions.titleWidth).toBeGreaterThanOrEqual(120)
  expect(dimensions.detailsWidth).toBeGreaterThan(0)
  expect(dimensions.detailsRight).toBeLessThanOrEqual(dimensions.metadataRight)
})
