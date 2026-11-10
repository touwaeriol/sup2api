/* global process, document */
// Phone layout regression of the console, run against the mock app (no
// backend). next/e2e is the API-level Go suite and drives no browser, so this
// is a node:test file using Playwright from its global install:
//
//   npx vite --mode mock --port 5185 &
//   NODE_PATH=$(npm root -g) E2E_WEB_URL=http://localhost:5185 node --test e2e/phone-layout.test.mjs
//
// On an iPhone 13 and a Pixel 7 it signs in (admin@example.com / admin), opens
// every page of the console menu and of system settings, and the pages only
// another page links to, and checks that
// - the page does not scroll sideways: documentElement.scrollWidth stays within
//   the device width (innerWidth itself grows with the content under device
//   emulation, so it is not the yardstick);
// - list pages show their rows as STable cards, with no visible <table> in main;
// - on the main list pages the first card starts within the first screen.
import { createRequire } from 'node:module'
import { after, before, describe, test } from 'node:test'
import assert from 'node:assert/strict'

// createRequire honours NODE_PATH, so the global Playwright install resolves.
const { chromium, devices } = createRequire(import.meta.url)('playwright')

const BASE = process.env.E2E_WEB_URL || 'http://localhost:5185'
const DEVICES = ['iPhone 13', 'Pixel 7']
/** Pages whose rows are STable cards on a phone. */
const CARD_PAGES = [
  '/accounts',
  '/groups',
  '/proxies',
  '/prices',
  '/usage',
  '/ledger',
  '/users',
  '/plugins',
  '/publishers',
  '/nodes',
  '/system/tasks',
  '/system/audit',
  '/me/api-keys',
  '/me/usage',
  '/me/wallet'
]
/** List pages whose first card must show without scrolling. */
const FIRST_SCREEN_PAGES = [
  '/accounts',
  '/groups',
  '/proxies',
  '/prices',
  '/usage',
  '/ledger',
  '/users',
  '/publishers',
  '/system/audit',
  '/me/api-keys',
  '/me/usage'
]

/** Pages without a menu entry: the balance ledger is opened from a row of the users list. */
const LINKED_PAGES = ['/ledger']

/**
 * The pages of system settings (CONTRACTS §90): on the settings page the
 * sidebar shows the settings view's menu instead of the console's, with the
 * configuration pages (/settings?tab=<key>), groups, model prices and the
 * plugins' settings.
 */
async function settingsPages(page) {
  await page.goto(`${BASE}/settings`)
  await page.waitForSelector('[data-testid="nav-view-settings"]', { state: 'attached' })
  await page.waitForLoadState('networkidle')
  return page.$$eval('[data-testid="nav-view-settings"] a[href]:not([data-testid="nav-back"])', links =>
    links.map(a => a.getAttribute('href'))
  )
}

for (const name of DEVICES) {
  describe(`phone layout on ${name}`, () => {
    const device = devices[name]
    let browser
    let page
    let pages = []

    before(async () => {
      browser = await chromium.launch()
      const context = await browser.newContext({ ...device })
      page = await context.newPage()
      await page.goto(`${BASE}/login`)
      await page.fill('input[type=email]', 'admin@example.com')
      await page.fill('input[type=password]', 'admin')
      await page.click('button[type=submit]')
      await page.waitForURL(url => !url.pathname.startsWith('/login'))
      await page.waitForSelector('aside nav a[href]', { state: 'attached' })
      // The submenus (core directories and plugin sections) start folded; open them all to list every page.
      await page.$$eval('aside nav [aria-expanded="false"]', buttons => buttons.forEach(b => b.click()))
      const sidebar = await page.$$eval('aside nav a[href]', links => links.map(a => a.getAttribute('href')))
      pages = [...new Set([...sidebar, ...(await settingsPages(page))])]
    })
    after(() => browser?.close())

    test('the menu and system settings list the console pages', () => {
      assert.ok(pages.length > 10, `pages: ${pages.join(', ')}`)
      for (const path of CARD_PAGES.filter(p => !LINKED_PAGES.includes(p))) assert.ok(pages.includes(path), `${path} is not in the menu`)
    })

    test('no page scrolls sideways; list pages show cards, the main ones within the first screen', async t => {
      for (const path of [...pages, ...LINKED_PAGES]) {
        await t.test(path, async () => {
          await page.goto(`${BASE}${path}`)
          await page.waitForLoadState('networkidle')
          if (CARD_PAGES.includes(path)) await page.waitForSelector('main ul[role="list"] > li', { timeout: 10_000 })
          const m = await page.evaluate(() => {
            const main = document.querySelector('main')
            const card = main.querySelector('ul[role="list"] > li')
            return {
              scrollWidth: document.documentElement.scrollWidth,
              tables: Array.from(main.querySelectorAll('table')).filter(table => table.checkVisibility()).length,
              cardTop: card ? card.getBoundingClientRect().top : null
            }
          })
          assert.ok(m.scrollWidth <= device.viewport.width, `${path} is ${m.scrollWidth}px wide on a ${device.viewport.width}px screen`)
          if (!CARD_PAGES.includes(path)) return
          assert.equal(m.tables, 0, `${path} shows a table`)
          if (FIRST_SCREEN_PAGES.includes(path))
            assert.ok(m.cardTop < device.viewport.height, `${path}: the first card starts at ${m.cardTop}px`)
        })
      }
    })
  })
}
