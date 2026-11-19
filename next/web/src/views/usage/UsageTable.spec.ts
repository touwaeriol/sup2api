import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { i18n } from '@/i18n'
import UsageTable from './UsageTable.vue'
import UsageStatus from './UsageStatus.vue'
import UsageError from './UsageError.vue'
import UsageTokens from './UsageTokens.vue'
import { errorText, type UsageRow } from './usage'
import { mountPinned } from '@sub2api/ui/testing'

vi.mock('@/composables/platforms', () => ({ usePlatforms: () => ({ load: vi.fn(), label: (id: string) => id }) }))
enableAutoUnmount(afterEach)
const row = {
  id: 1,
  created_at: '2026-10-05T15:00:00Z',
  model: 'claude-opus-5-5',
  group_id: 7,
  group_name: 'Long group name',
  user_id: 3,
  account_id: 9,
  account_name: 'Account nine',
  protocol: 'anthropic.messages',
  client_request_id: 'client-request-one',
  api_key_name: 'my-laptop',
  client_ip: '2001:db8::7',
  success: false,
  status_code: 403,
  error_type: 'model_not_allowed',
  error_message: 'model not allowed for this group',
  input_tokens: 0,
  output_tokens: 0,
  cache_read_tokens: 0,
  cache_creation_tokens: 0,
  total_cost: '0',
  billing_status: 'free',
  latency_ms: 0,
  stream: false
} as UsageRow
const global = {
  plugins: [i18n],
  stubs: { UsageDetail: { props: ['row', 'path', 'view'], template: '<div data-testid="usage-detail" :data-view="view">{{ path }}</div>' } }
}

describe('usage records layout', () => {
  it('uses the SDK picker for desktop and mobile fields and persists separately for self-service', async () => {
    localStorage.clear()
    const w = mount(UsageTable, { props: { view: 'all', rows: [row], columnSettingsKey: 'core.usage.all' }, global, attachTo: document.body })
    await w.get('[data-testid="table-column-settings"]').trigger('click')
    const ip = Array.from(document.querySelectorAll<HTMLButtonElement>('[role="menuitemcheckbox"]'))
      .find(button => button.textContent?.trim() === i18n.global.t('usage.request.clientIP'))!
    ip.click()
    await nextTick()
    expect(w.find('[data-testid="ip-address-filter"]').exists()).toBe(false)
    w.unmount()
    const restored = mount(UsageTable, { props: { view: 'all', rows: [row], columnSettingsKey: 'core.usage.all' }, global })
    expect(restored.find('[data-testid="ip-address-filter"]').exists()).toBe(false)
    const card = mountPinned(UsageTable, { props: { view: 'all', rows: [row], columnSettingsKey: 'core.usage.all' }, global })
    expect(card.get('[data-testid="mobile-usage-record"]').text()).not.toContain(row.client_ip)
    const mine = mount(UsageTable, { props: { view: 'all', rows: [row], showUser: false, showAccount: false, columnSettingsKey: 'core.usage.self' }, global })
    expect(mine.get('[data-testid="ip-address-filter"]').text()).toBe(row.client_ip)
  })
  it('keeps model, routing and error readable while preserving request-id filtering', async () => {
    const w = mount(UsageTable, { props: { view: 'all', rows: [row], showClientRequestId: true }, global })
    expect(w.findAll('th').length).toBe(11)
    expect(w.text()).toContain(row.model)
    expect(w.text()).toContain(row.group_name)
    expect(w.get('[data-testid="usage-client"]').text()).toContain('my-laptop')
    expect(w.get('[data-testid="ip-address-filter"]').text()).toBe('2001:db8::7')
    expect(w.find('[data-testid="mobile-usage-record"]').exists()).toBe(false)
    await w.get('[data-testid="ip-address-filter"]').trigger('click')
    expect(w.emitted('filter-client-ip')).toEqual([['2001:db8::7']])
    expect(w.text()).toContain('403')
    expect(w.text()).not.toContain('usage.errors.model_not_allowed')
    expect(w.get('[data-testid="usage-error-summary"]').element.closest('td')?.querySelector('time')).not.toBeNull()
    const headers = w.findAll('th').map(th => th.text())
    expect(headers.indexOf(i18n.global.t('usage.request.clientIP'))).toBeLessThan(headers.indexOf(i18n.global.t('common.model')))
    expect(headers.indexOf(i18n.global.t('common.account'))).toBe(headers.indexOf(i18n.global.t('common.model')) + 1)
    await w.get('[data-testid="client-request-id"]').trigger('click')
    expect(w.emitted('filter-client-request-id')).toEqual([['client-request-one']])
  })
  it('marks a converted request with its grade, protocol chain and losses (CONVERT-MATRIX §7)', () => {
    i18n.global.locale.value = 'zh'
    const conversion = { v: 1, grade: 'fair', hops: ['anthropic.messages', 'openai.responses'], lost: { tool: ['tools[3]'] }, response_lost: { reasoning: ['response.output[0]'] } } as const
    const w = mount(UsageTable, { props: { view: 'all', rows: [
      { ...row, upstream_protocol: 'openai.responses', conversion },
      { ...row, id: 2, conversion: { v: 1, grade: 'local', hops: ['anthropic.count_tokens'] } },
      { ...row, id: 3, upstream_protocol: 'openai.chat' },
      { ...row, id: 4, conversion: { v: 1, grade: 'native', rejected: [{ type: 'x/y', error: 'no' }] } }
    ] as UsageRow[] }, global })
    const grades = w.findAll('[data-testid="conversion-grade"]')
    expect(grades.map(g => g.text())).toEqual(['转换·一般', '本地估算'])
    expect(grades[0].attributes('title')).toBe('anthropic.messages → openai.responses')
    expect(w.findAll('[data-testid="usage-conversion-loss"]').map(l => l.attributes('title'))).toEqual(['转换有丢失：工具、推理'])
    // a record without a conversion trace keeps the plain badge; a native route shows none
    expect(w.findAll('tbody tr')[2].text()).toContain('已转换')
    expect(w.findAll('tbody tr')[3].text()).not.toContain('转换')
  })
  it('shows the upstream account as "#id name" and filters by it on click', async () => {
    const w = mount(UsageTable, { props: { view: 'all', rows: [row, { ...row, id: 2, account_id: null, account_name: '' }] }, global })
    const accounts = w.findAll('[data-testid="usage-account"]')
    expect(accounts.length).toBe(1)
    expect(accounts[0].findAll('span').map(x => x.text()).filter(Boolean)).toEqual(['#9', 'Account nine'])
    expect(accounts[0].attributes('title')).toContain('#9 Account nine')
    await accounts[0].trigger('click')
    expect(w.emitted('filter-account-id')).toEqual([[9]])
  })
  it('marks the model by its vendor and the account by the platform the request reached it on', () => {
    const rows = [
      { ...row, platform: 'openai', protocol: 'openai.chat', upstream_protocol: 'anthropic.messages' },
      { ...row, id: 2, model: '', protocol: 'anthropic.count_tokens', upstream_protocol: 'local' }
    ] as UsageRow[]
    const [converted, local] = mount(UsageTable, { props: { view: 'all', rows }, global }).findAll('tbody tr')
    expect(converted.get('[data-testid="model-badge"] [data-brand]').attributes('data-brand')).toBe('anthropic')
    const account = converted.get('[data-testid="usage-account"]')
    expect(account.get('[data-platform]').attributes('data-platform')).toBe('anthropic')
    expect(account.classes()).toContain('brand-tint')
    // no model, and an upstream protocol that names no platform: neither is guessed
    expect(local.find('[data-testid="model-badge"]').exists()).toBe(false)
    expect(local.get('[data-testid="usage-account"]').find('[data-platform]').exists()).toBe(false)
    expect(local.get('[data-testid="usage-account"]').classes()).not.toContain('brand-tint')
    const card = mountPinned(UsageTable, { props: { view: 'all', rows: rows.slice(0, 1) }, global }).get('[data-testid="mobile-usage-record"]')
    expect(card.get('[data-testid="model-badge"] [data-brand]').attributes('data-brand')).toBe('anthropic')
    expect(card.get('[data-platform]').attributes('data-platform')).toBe('anthropic')
  })
  it('keeps the account and its platform out of the self-service list', () => {
    const w = mount(UsageTable, { props: { view: 'all', rows: [{ ...row, upstream_protocol: 'anthropic.messages' }], showUser: false, showAccount: false }, global })
    expect(w.find('[data-testid="model-badge"]').exists()).toBe(true)
    expect(w.find('[data-platform]').exists()).toBe(false)
  })
  it('shows the account view no API key or client, and the cost at the list price (CONTRACTS §103.5)', () => {
    const w = mount(UsageTable, { props: { rows: [{ ...row, success: true, billing_status: undefined, total_cost: '777', standard_cost: '1.5' } as unknown as UsageRow], showUser: false, view: 'account' }, global })
    expect(w.text()).not.toContain(i18n.global.t('usage.request.apiKey'))
    expect(w.find('[data-testid="ip-address-filter"]').exists()).toBe(false)
    expect(w.text()).toContain('1.5')
    expect(w.text()).not.toContain('777')
    expect(w.text()).toContain(i18n.global.t('usage.live.listPrice'))
    expect(w.text()).not.toContain(i18n.global.t('usage.cols.cost'))
    expect(w.get('[data-testid="usage-account"]').text()).toContain('Account nine')
  })
  it('points at the upstream model only when the account mapped the request to another', () => {
    const w = mount(UsageTable, { props: { view: 'all', rows: [
      row,
      { ...row, id: 2, upstream_model: row.model },
      { ...row, id: 3, upstream_model: 'claude-opus-5-5-20261001' }
    ] }, global })
    const mapped = w.findAll('tbody tr').map(tr => tr.find('[title$="claude-opus-5-5-20261001"]'))
    expect(mapped.map(m => m.exists())).toEqual([false, false, true])
    expect(mapped[2].attributes('title')).toBe('claude-opus-5-5 → claude-opus-5-5-20261001')
    expect(mapped[2].get('[data-testid="model-badge"]').text()).toContain('claude-opus-5-5')
    // Hovering the model name shows the mapping too, not just the name: the badge carries the same tooltip.
    const badges = w.findAll('tbody tr').map(tr => tr.get('[data-testid="model-badge"]'))
    expect(badges.map(b => b.attributes('title'))).toEqual(['claude-opus-5-5', 'claude-opus-5-5', 'claude-opus-5-5 → claude-opus-5-5-20261001'])
    expect(badges[2].find('[title]').exists()).toBe(false)
  })
  it('loads owner-scoped mobile detail only when expanded and keeps admin fields hidden', async () => {
    const w = mountPinned(UsageTable, { props: { view: 'all', rows: [row], showUser: false, showAccount: false, detailPath: id => `/me/usage/${id}` }, global })
    expect(w.find('[data-testid="usage-detail"]').exists()).toBe(false)
    expect(w.text()).not.toContain('Account nine')
    expect(w.text()).not.toContain('#9')
    expect(w.findAll('[data-testid="usage-account"]')).toHaveLength(0)
    const expand = w.get('[data-testid="mobile-usage-record"] button[aria-expanded]')
    await expand.trigger('click')
    expect(expand.attributes('aria-expanded')).toBe('true')
    expect(w.get('[data-testid="usage-detail"]').text()).toBe('/me/usage/1')
    expect(w.get('[data-testid="usage-detail"]').attributes('data-view')).toBe('all')
    await expand.trigger('click')
    expect(w.find('[data-testid="usage-detail"]').exists()).toBe(false)
  })
  it('marks a retried request beside its outcome and leads its account with the accounts tried before (CONTRACTS §98)', async () => {
    const retried = { ...row, id: 2, success: true, status_code: 200, attempts: 3, attempt_trail: { v: 1, steps: [
      { account_id: 16, status: 502, error_type: 'upstream_error', ms: 640, next: 'same' },
      { account_id: 16, status: 502, error_type: 'upstream_error', ms: 702, next: 'switch' },
      { account_id: 9, status: 200, ms: 2950 }
    ] } } as UsageRow
    const [once, again] = mount(UsageTable, { props: { view: 'all', rows: [row, retried] }, global }).findAll('tbody tr')
    expect(once.find('[data-testid="usage-retry-badge"]').exists()).toBe(false)
    expect(once.find('[data-testid="usage-attempt-chain"]').exists()).toBe(false)
    expect(again.get('[data-testid="usage-retry-badge"]').element.closest('td')?.querySelector('time')).not.toBeNull()
    const cell = again.get('[data-testid="usage-account"]').element.closest('td')!
    expect(cell.querySelector('[data-testid="usage-attempt-chain"]')?.textContent?.replace(/\s+/g, '')).toBe('#16×2→')
    // a user's own records: the badge, but no account column for the chain, and the detail told so
    const mine = mountPinned(UsageTable, { props: { rows: [retried], showAccount: false, view: 'self' }, global })
    expect(mine.find('[data-testid="usage-retry-badge"]').exists()).toBe(true)
    expect(mine.find('[data-testid="usage-attempt-chain"]').exists()).toBe(false)
    await mine.get('[data-testid="mobile-usage-record"] button[aria-expanded]').trigger('click')
    expect(mine.get('[data-testid="usage-detail"]').attributes('data-view')).toBe('self')
  })
  it('preserves cache-only usage on failed requests', () => {
    const w = mount(UsageTokens, { props: { row: { ...row, cache_read_tokens: 1234 } }, global })
    expect(w.text()).toContain('1,234')
    expect(w.attributes('title')).toContain('1,234')
  })
  it('shows the HTTP status and a translated error with the raw message on hover', () => {
    expect(mount(UsageStatus, { props: { row }, global }).text()).toContain('403')
    const w = mount(UsageError, { props: { row }, global })
    expect(w.text()).not.toContain('model_not_allowed')
    expect(w.get('p').attributes('title')).toContain('model not allowed for this group')
  })
  it('names an unreported execution (usage_unreported) in both languages (design §8.5)', () => {
    const unreported = { ...row, status_code: 200, error_type: 'usage_unreported', error_message: 'plugin returned no usage' }
    const locale = i18n.global.locale.value
    for (const [lang, text] of [['zh', '未上报用量（未计费）'], ['en', 'Usage not reported (not billed)']]) {
      i18n.global.locale.value = lang
      expect(mount(UsageError, { props: { row: unreported }, global }).text()).toBe(text)
    }
    i18n.global.locale.value = locale
  })
  it('shows a failed request as "status_code=…, message" in the detail column, whole text on hover', async () => {
    const w = mount(UsageTable, { props: { view: 'all', rows: [row, { ...row, id: 2, success: true, status_code: 200, matched_tier: 'tier-a' }] }, global, attachTo: document.body })
    const texts = w.findAll('[data-testid="usage-error-text"]')
    expect(texts.map(x => x.text())).toEqual(['status_code=403, model not allowed for this group'])
    expect(texts[0].element.closest('td')).not.toBeNull()
    expect(w.text()).toContain('tier-a')
    await texts[0].trigger('focusin')
    expect(document.querySelector('[role="tooltip"]')?.textContent).toContain('status_code=403, model not allowed for this group')
  })
  it('shows each request as a card on a phone, with the same fields and filters', async () => {
    const w = mountPinned(UsageTable, { props: { view: 'all', rows: [row], showClientRequestId: true }, global })
    expect(w.find('table').exists()).toBe(false)
    const card = w.get('[data-testid="mobile-usage-record"]')
    expect(card.text()).toContain(row.model)
    expect(card.text()).toContain('#9 Account nine')
    expect(card.text()).toContain('status_code=403, model not allowed for this group')
    await card.get('[data-testid="ip-address-filter"]').trigger('click')
    expect(w.emitted('filter-client-ip')).toEqual([['2001:db8::7']])
    await card.findAll('button').find(b => b.text() === 'client-request-one')!.trigger('click')
    expect(w.emitted('filter-client-request-id')).toEqual([['client-request-one']])
    await card.findAll('button').find(b => b.text() === '#9 Account nine')!.trigger('click')
    expect(w.emitted('filter-account-id')).toEqual([[9]])
  })
  it('falls back to the error type when there is no message', () => {
    expect(errorText({ ...row, status_code: 503, error_type: 'model_availability', error_message: '' })).toBe('status_code=503, model_availability')
    expect(errorText({ ...row, status_code: 0, error_type: '', error_message: 'boom' })).toBe('boom')
    expect(errorText({ ...row, success: true })).toBe('')
  })
})
