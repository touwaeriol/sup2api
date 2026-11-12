// Screenshots of the moderation redesign prototype against the web mock (vite --mode mock).
import { chromium } from 'playwright-core'
const BASE = process.env.BASE || 'http://127.0.0.1:5199'
const OUT = process.env.OUT || '/out'
const page_ = (q) => `${BASE}/p/moderation/dashboard?${q}`

const SHOTS = [
  // [file, query, {viewport, theme, action}]
  ['desktop-overview-sidebar', 'tab=overview', {}],
  ['desktop-events-list', 'tab=events', {}],
  ['desktop-record-match-fallback', 'tab=events&record=498', { wait: '[data-testid="mod-match-fallback"]' }],
  ['desktop-record-match-default', 'tab=events&record=497', { scrollTo: 'text=匹配来源' }],
  ['desktop-record-match-fields', 'tab=events&record=498', { wait: '[data-testid="mod-match-fallback"]', scrollTo: 'text=匹配来源' }],
  ['desktop-flow-scope', 'tab=flows&flow=1', { wait: '[data-testid="mod-flow-scope"]', scrollTo: '[data-testid="mod-flow-scope"]' }],
  ['desktop-settings-policy', 'tab=policy', {}],
  ['desktop-settings-cache', 'tab=cache', {}],
  ['desktop-settings-lists', 'tab=lists', {}],
  ['desktop-settings-analysis', 'tab=analysis', {}],
  ['desktop-settings-jev', 'tab=jev', {}],
  ['desktop-settings-response', 'tab=response', {}],
  ['desktop-settings-records', 'tab=records', {}],
  ['desktop-settings-advanced', 'tab=advanced', {}],
  ['desktop-legacy-tab-settings', 'tab=settings', {}],
  ['dark-overview-sidebar', 'tab=overview', { theme: 'dark' }],
  ['dark-settings-policy', 'tab=policy', { theme: 'dark' }],
  ['dark-flow-scope', 'tab=flows&flow=1', { theme: 'dark', wait: '[data-testid="mod-flow-scope"]', scrollTo: '[data-testid="mod-flow-scope"]' }],
  ['dark-record-match', 'tab=events&record=498', { theme: 'dark', wait: '[data-testid="mod-match-fallback"]' }],
  ['mobile-sidebar-drawer', 'tab=policy', { mobile: true, drawer: true }],
  ['mobile-settings-policy', 'tab=policy', { mobile: true }],
  ['mobile-settings-cache', 'tab=cache', { mobile: true, full: true }],
  ['mobile-flow-scope', 'tab=flows&flow=1', { mobile: true, wait: '[data-testid="mod-flow-scope"]', scrollTo: '[data-testid="mod-flow-scope"]' }],
  ['mobile-record-match', 'tab=events&record=498', { mobile: true, wait: '[data-testid="mod-match-fallback"]' }],
  ['mobile-record-match-fields', 'tab=events&record=498', { mobile: true, wait: '[data-testid="mod-match-fallback"]', scrollTo: 'text=匹配来源' }],
  ['mobile-dark-drawer', 'tab=overview', { mobile: true, drawer: true, theme: 'dark' }]
]

const browser = await chromium.launch()
const login = await (await fetch(`${BASE}/api/v1/auth/login`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ email: 'admin@example.com', password: 'admin' }) })).json()
const tok = login.data ?? login
const session = { access_token: tok.access_token, refresh_token: tok.refresh_token, expires_at: Date.now() + 7200e3 }

for (const [file, q, o] of SHOTS) {
  const ctx = await browser.newContext(
    o.mobile
      ? { viewport: { width: 390, height: 844 }, deviceScaleFactor: 3, isMobile: true, hasTouch: true, colorScheme: o.theme || 'light' }
      : { viewport: { width: 1440, height: 900 }, colorScheme: o.theme || 'light' }
  )
  await ctx.addInitScript(([s, theme]) => {
    localStorage.setItem('s2a.session', s)
    localStorage.setItem('s2a.theme', theme)
    localStorage.setItem('s2a.locale', 'zh')
  }, [JSON.stringify(session), o.theme || 'light'])
  const p = await ctx.newPage()
  const errors = []
  p.on('pageerror', (e) => errors.push(e.message))
  await p.goto(page_(q), { waitUntil: 'networkidle' })
  await p.waitForSelector('[data-testid="mod-sections"]', { timeout: 20000 })
  if (o.wait) await p.waitForSelector(o.wait, { timeout: 20000 })
  await p.waitForTimeout(800)
  if (o.drawer) {
    await p.click('button[aria-label="打开导航菜单"]').catch(() => p.locator('header button').first().click())
    await p.waitForTimeout(500)
  }
  if (o.scrollTo) await p.locator(o.scrollTo).first().scrollIntoViewIfNeeded()
  await p.screenshot({ path: `${OUT}/${file}.png`, fullPage: !!o.full && !o.wait })
  console.log(file, errors.length ? 'ERR ' + errors.join(' | ') : 'ok')
  await ctx.close()
}
await browser.close()
