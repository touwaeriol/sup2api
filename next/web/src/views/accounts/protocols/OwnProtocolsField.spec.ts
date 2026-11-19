import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { h } from 'vue'
import { enableAutoUnmount, type VueWrapper } from '@vue/test-utils'
import type { Account, AccountType } from '@/api/types'
import { i18n } from '@/i18n'
import { codex, mountEditor, openai, type } from './fixtures'
import OwnProtocolsField from './OwnProtocolsField.vue'

// The platform list falls back to the built-in catalog.
vi.mock('@sub2api/host', async original => ({ ...(await original<typeof import('@sub2api/host')>()), api: { get: vi.fn().mockRejectedValue(new Error('offline')) } }))
enableAutoUnmount(afterEach)

const render = (saved: Pick<Account, 'protocols' | 'model_protocols'> | null = null, accountType: AccountType = openai, errors?: string[]) =>
  mountEditor(editor => h(OwnProtocolsField, { editor, errors }), { saved, accountType })
const rows = (w: VueWrapper) => w.findAll('li[data-protocol]')
const box = (w: VueWrapper, protocol: string) => w.get(`li[data-protocol="${protocol}"] input`)
const state = (w: VueWrapper) => rows(w).map(r => [(r.get('input').element as HTMLInputElement).checked, r.get('input').attributes('disabled') !== undefined])
const mode = (w: VueWrapper, label: string) => w.get('[data-testid="own-protocols-mode"]').findAll('label').find(l => l.text() === label)!.get('input')

describe('own protocols of an account', () => {
  beforeEach(() => {
    i18n.global.locale.value = 'zh'
  })

  it('lists the own protocols of the type with their endpoint, all ticked and greyed under "all"', () => {
    const { w } = render()
    expect(rows(w).map(r => r.text())).toEqual([
      'Chat Completions原生POST /openai.chat',
      'Responses原生POST /openai.responses',
      'Responses WebSocket原生POST /openai.responses_ws',
      'Embeddings原生POST /openai.embeddings'
    ])
    expect(state(w)).toEqual([[true, true], [true, true], [true, true], [true, true]])
    expect(w.find('[data-testid="own-protocols-closed"]').exists()).toBe(false)
  })

  it('lets each protocol be unticked under "chosen" and says how many stay closed', async () => {
    const { w, editor } = render()
    await mode(w, '指定').setValue(true)
    expect(state(w)).toEqual([[true, false], [true, false], [true, false], [true, false]])
    await box(w, 'openai.responses_ws').setValue(false)
    await box(w, 'openai.embeddings').setValue(false)
    expect(editor.binding().protocols).toEqual(['openai.chat', 'openai.responses'])
    expect(w.get('[data-testid="own-protocols-closed"]').text()).toBe('已指定：有 2 个本平台协议未开放。账号类型以后新增的协议也不会自动开放。')
    await mode(w, '全部').setValue(true)
    expect(editor.binding().protocols).toBeNull()
    expect(w.find('[data-testid="own-protocols-closed"]').exists()).toBe(false)
  })

  it('loads the protocols an account chose', () => {
    const { w } = render({ protocols: ['openai.chat'] })
    expect(state(w).map(s => s[0])).toEqual([true, false, false, false])
    expect(w.get('[data-testid="own-protocols-closed"]').text()).toContain('有 3 个')
  })

  it('shows a chosen id the type no longer serves as a warning chip that can only be removed', async () => {
    const { w, editor } = render({ protocols: ['openai.chat', 'openai.legacy'] })
    expect(rows(w)).toHaveLength(4)
    const unknown = w.get('[data-testid="own-protocols-unknown"]')
    expect(unknown.text()).toContain('以下协议不是此账号类型的本平台协议，保存时会被拒绝，请移除：')
    expect(editor.issues.value).toEqual([])
    await unknown.get('[data-protocol="openai.legacy"] button').trigger('click')
    expect(editor.binding().protocols).toEqual(['openai.chat'])
    expect(w.find('[data-testid="own-protocols-unknown"]').exists()).toBe(false)
  })

  it('names what an adapted protocol is sent as', () => {
    const { w } = render(null, codex)
    expect(rows(w).map(r => r.text())).toEqual(['Responses原生POST /openai.responses', 'Chat Completions适配 → ResponsesPOST /openai.chat'])
  })

  it('shows the server errors of the field, and says so when the type has no own protocol', () => {
    expect(render(null, openai, ['openai.images: 账号类型不能服务该协议']).w.get('[data-testid="own-protocols-error"]').text()).toBe('openai.images: 账号类型不能服务该协议')
    expect(render(null, type(['foo'], [])).w.get('[data-testid="own-protocols-none"]').text()).toBe('此账号类型当前没有可服务的本平台协议。')
  })
})
