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
  ['mobile-dark-drawer', 'tab=overview', { mobile: true, drawer: true, theme: 'dark' }],
  // AI review redesign (design §9)
  ['ai-desktop-review', 'tab=ai', { wait: '[data-testid="mod-ai-pending"]', full: true }],
  ['ai-desktop-apply-confirm', 'tab=ai', { wait: '[data-testid="mod-proposal"] button', click: '[data-testid="mod-proposal"] button' }],
  ['ai-desktop-user-pending', 'tab=watch&user=44', { wait: '[data-testid="mod-user-ai"]' }],
  ['ai-desktop-user-auto-applied', 'tab=watch&user=7', { wait: '[data-testid="mod-user-ai"]' }],
  ['ai-desktop-user-queued', 'tab=watch&user=12', { wait: '[data-testid="mod-user-ai"]' }],
  ['ai-desktop-settings', 'tab=analysis', { full: true }],
  ['ai-desktop-advanced-resources', 'tab=advanced', { scrollTo: 'text=AI 复核资源' }],
  ['ai-dark-review', 'tab=ai', { theme: 'dark', wait: '[data-testid="mod-ai-pending"]' }],
  ['ai-dark-user-pending', 'tab=watch&user=44', { theme: 'dark', wait: '[data-testid="mod-user-ai"]' }],
  ['ai-dark-settings', 'tab=analysis', { theme: 'dark' }],
  ['ai-mobile-review', 'tab=ai', { mobile: true, wait: '[data-testid="mod-ai-pending"]', full: true }],
  ['ai-mobile-user-pending', 'tab=watch&user=44', { mobile: true, wait: '[data-testid="mod-user-ai"]' }],
  ['ai-mobile-settings', 'tab=analysis', { mobile: true, full: true }],
  ['ai-mobile-sidebar', 'tab=ai', { mobile: true, drawer: true }],
  // Against today's backend: no proposals and no cross-user list (oldBackend strips them from the mock's answers).
  ['ai-desktop-unsupported', 'tab=ai', { oldBackend: true, wait: '[data-testid="mod-ai-unsupported"]' }],
  ['ai-desktop-user-legacy', 'tab=watch&user=44', { oldBackend: true, wait: '[data-testid="mod-user-ai"]' }]
]

const browser = await chromium.launch()
const login = await (await fetch(`${BASE}/api/v1/auth/login`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ email: 'admin@example.com', password: 'admin' }) })).json()
const tok = login.data ?? login
const session = { access_token: tok.access_token, refresh_token: tok.refresh_token, expires_at: Date.now() + 7200e3 }

// ONLY=<prefix> takes only the shots whose name starts with it.
for (const [file, q, o] of SHOTS.filter(([f]) => !process.env.ONLY || f.startsWith(process.env.ONLY))) {
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
  if (o.oldBackend) {
    // Today's backend: the status has no feature flags, analyses have no risk or proposal.
    const strip = (v) => {
      if (Array.isArray(v)) return v.forEach(strip)
      if (v && typeof v === 'object') {
        delete v.features
        delete v.review_at
        if ('proposal' in v && 'suggestion' in v) delete v.proposal, delete v.risk
        Object.values(v).forEach(strip)
      }
    }
    await p.route(/\/api\/v1\/p\/moderation\/(status|users\/)/, async (route) => {
      const res = await route.fetch()
      const body = await res.json()
      strip(body)
      await route.fulfill({ response: res, json: body })
    })
  }
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
  if (o.click) {
    await p.locator(o.click).first().click()
    await p.waitForTimeout(500)
  }
  if (o.scrollTo) await p.locator(o.scrollTo).first().scrollIntoViewIfNeeded()
  await p.screenshot({ path: `${OUT}/${file}.png`, fullPage: !!o.full })
  console.log(file, errors.length ? 'ERR ' + errors.join(' | ') : 'ok')
  await ctx.close()
}
await browser.close()
