import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { SFormModal } from '@sub2api/ui'
import { i18n } from '@/i18n'
import type { PromoCode } from '../topup'
import PromoEditModal from './PromoEditModal.vue'

const api = vi.hoisted(() => ({ post: vi.fn(), patch: vi.fn() }))
vi.mock('@sub2api/host', async orig => ({ ...(await orig<object>()), api }))
enableAutoUnmount(afterEach)

const PROMO: PromoCode = {
  id: 7,
  code: 'SPRING',
  bonus_amount: '5',
  min_amount: '20',
  max_uses: 100,
  used_count: 3,
  status: 'active',
  state: 'active',
  expires_at: '2026-12-31T16:00:00Z',
  notes: 'spring',
  created_at: '2026-11-01T00:00:00Z'
}

/** Opens the modal for `promo` (null: a new code), lets `fill` type, and submits it. */
async function save(promo: PromoCode | null, fill: () => Promise<void> = async () => {}) {
  const w = mount(PromoEditModal, { attachTo: document.body, props: { open: false, promo }, global: { plugins: [i18n] } })
  await w.setProps({ open: true })
  await flushPromises()
  await fill()
  w.findComponent(SFormModal).vm.$emit('submit')
  await flushPromises()
  return w
}

const input = (i: number) => document.body.querySelectorAll<HTMLInputElement>('[role="dialog"] input')[i]

/** Types into the i-th field; a date input reports on change. */
async function type(i: number, value: string) {
  const el = input(i)
  el.value = value
  el.dispatchEvent(new Event(el.type === 'datetime-local' ? 'change' : 'input'))
  await flushPromises()
}

describe('promo code editor', () => {
  beforeEach(() => {
    i18n.global.locale.value = 'en'
    api.post.mockResolvedValue({})
    api.patch.mockResolvedValue({})
  })

  it('creates a code in upper case with no expiry (null) and trimmed notes', async () => {
    const w = await save(null, async () => {
      await type(0, 'save5')
      await type(5, '  launch  ')
    })
    expect(api.post).toHaveBeenCalledWith('/promo-codes', {
      code: 'SAVE5',
      bonus_amount: '5',
      min_amount: '20',
      max_uses: 0,
      expires_at: null,
      notes: 'launch'
    })
    expect(w.emitted('saved')).toHaveLength(1)
  })

  it('leaves the code to the server when none is typed', async () => {
    await save(null)
    expect(api.post.mock.calls[0][1].code).toBeUndefined()
  })

  it('edits without the code, keeping the expiry', async () => {
    await save(PROMO, () => type(1, '8'))
    expect(api.patch).toHaveBeenCalledWith('/promo-codes/7', {
      code: undefined,
      bonus_amount: '8',
      min_amount: '20',
      max_uses: 100,
      expires_at: '2026-12-31T16:00:00Z',
      notes: 'spring'
    })
  })

  it('sends null to take an expiry away', async () => {
    await save({ ...PROMO, expires_at: undefined })
    expect(api.patch.mock.calls[0][1].expires_at).toBeNull()
  })

  it('keeps an expiry that has passed when it is left as it was', async () => {
    const past = '2020-01-01T00:00:00Z'
    await save({ ...PROMO, expires_at: past })
    expect(api.patch.mock.calls[0][1].expires_at).toBe(past)
  })

  it('refuses a new expiry in the past', async () => {
    await save(null, () => type(4, '2020-01-01T08:00'))
    expect(api.post).not.toHaveBeenCalled()
  })
})
