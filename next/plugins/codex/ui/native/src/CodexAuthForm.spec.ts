import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { ref } from 'vue'
import { ApiError, type PluginHost } from '@sub2api/host'
import CodexAuthForm from './CodexAuthForm.vue'
import { setHost } from './host'

// A plugin host whose t() returns the key, and whose plugin API answers
// POSTs from `answer` (calls are recorded).
type Answer = (path: string, body: any) => unknown
let answer: Answer
let calls: Array<{ path: string; body: any }>

function fakeHost(): PluginHost {
  const post = vi.fn(async (path: string, body?: unknown) => {
    calls.push({ path, body })
    const r = answer(path, body)
    if (r instanceof Error) throw r
    return r
  })
  const notUsed = vi.fn(async () => {
    throw new Error('not used')
  })
  return {
    t: (key: string) => key,
    i18n: { locale: ref('en'), formatDateTime: (v: unknown) => `T(${Number(v)})`, t: (k: string) => k, text: (v: unknown) => String(v ?? '') },
    pluginApi: { get: notUsed, post, put: notUsed, patch: notUsed, delete: notUsed }
  } as unknown as PluginHost
}

const DEVICE_CREDS = { access_token: 'at', refresh_token: 'rt', id_token: 'it', expires_at: 1800000000, email: 'me@example.com', chatgpt_account_id: 'acc-1', plan_type: 'plus' }
const OAUTH_CREDS = { access_token: 'at2', refresh_token: 'rt2', id_token: 'it2', expires_at: 1800000000, email: 'me@example.com', chatgpt_account_id: 'acc-2', plan_type: 'pro' }
const CALLBACK = 'http://localhost:1455/auth/callback'
/** The client headers a call of a form without its own carries. */
const NO_AGENT = { user_agent: '', originator: '' }

/** The editor's proxyTicket: a ticket of its current proxy choice. */
let proxyTicket: ReturnType<typeof vi.fn<() => Promise<string>>>

let wrapper: VueWrapper | null = null
function mountForm(props: { accountType: { type: string } } & Record<string, unknown>) {
  wrapper = mount(CodexAuthForm, { props: { modelValue: {}, mode: 'create', proxyTicket, ...props, accountType: { plugin_key: 'codex', ...props.accountType } }, attachTo: document.body })
  return wrapper
}
/** The last v-model value the form emitted. */
const lastValue = (w: VueWrapper) => (w.emitted('update:modelValue') as unknown[][] | undefined)?.at(-1)?.[0] as Record<string, unknown> | undefined

beforeEach(() => {
  vi.useFakeTimers()
  proxyTicket = vi.fn(async () => 'sealed-ticket')
  calls = []
  answer = () => {
    throw new Error('unexpected call')
  }
  setHost(fakeHost())
})
afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  setHost(null)
  vi.useRealTimers()
})

describe('CodexAuthForm: flows by account type', () => {
  it('shows only the device flow for codex_device', () => {
    const w = mountForm({ accountType: { type: 'codex_device' } })
    expect(w.find('[data-testid="codex-device-flow"]').exists()).toBe(true)
    // Where to turn device code sign-in on, shown before the flow starts.
    expect(w.get('[data-testid="codex-device-guide-link"]').attributes('href')).toBe('https://chatgpt.com/settings/security')
    expect(w.find('[data-testid="codex-oauth-flow"]').exists()).toBe(false)
  })

  it('shows only the sign-in flow for codex_oauth', () => {
    const w = mountForm({ accountType: { type: 'codex_oauth' } })
    expect(w.find('[data-testid="codex-oauth-flow"]').exists()).toBe(true)
    expect(w.find('[data-testid="codex-device-flow"]').exists()).toBe(false)
  })
})

describe('CodexAuthForm: client headers', () => {
  it('edits the account\'s own User-Agent and originator in the credentials', async () => {
    const loaded = { access_token: '******', email: 'me@example.com', originator: 'or-acct' }
    const w = mountForm({ mode: 'edit', account: { id: 1, type: 'codex_device' }, accountType: { type: 'codex_device' }, modelValue: loaded })
    expect((w.get('[data-testid="codex-originator"]').element as HTMLInputElement).value).toBe('or-acct')
    expect((w.get('[data-testid="codex-user-agent"]').element as HTMLInputElement).value).toBe('')
    await w.get('[data-testid="codex-user-agent"]').setValue('ua-acct')
    expect(lastValue(w)).toEqual({ ...loaded, user_agent: 'ua-acct' })
  })

  it('refuses a control character but tab at its field, as the plugin does (paramoverride.ValidHeaderValue)', async () => {
    const loaded = { access_token: '******', email: 'me@example.com' }
    const w = mountForm({ mode: 'edit', account: { id: 1, type: 'codex_device' }, accountType: { type: 'codex_device' }, modelValue: { ...loaded, user_agent: 'ua\tok' } })
    expect(await (w.vm as unknown as { validate(): Promise<boolean> }).validate()).toBe(true)
    await w.setProps({ modelValue: { ...loaded, originator: 'or\nbad' } })
    expect(await (w.vm as unknown as { validate(): Promise<boolean> }).validate()).toBe(false)
    expect(w.get('[data-testid="codex-agent"]').text()).toContain('agent.invalid')
    expect(w.find('[data-testid="codex-validate-error"]').exists()).toBe(false)
  })

  it('sends the form\'s current values with every call of a login', async () => {
    answer = (path) => {
      if (path === '/auth/device/start') return { session_id: 's1', verification_url: 'https://x', user_code: 'C', interval: 5, expires_in: 900 }
      if (path === '/auth/device/poll') return { status: 'pending' }
      if (path === '/auth/codex/start') return { session_id: 'w1', authorize_url: 'https://auth.openai.com/a' }
      if (path === '/auth/codex/exchange') return { credentials: OAUTH_CREDS }
      throw new Error(`unexpected ${path}`)
    }
    const w = mountForm({ accountType: { type: 'codex_device' }, modelValue: { user_agent: 'ua-1' } })
    await w.get('[data-testid="codex-device-start"]').trigger('click')
    await flushPromises()
    expect(calls.at(-1)?.body).toEqual({ proxy_ticket: 'sealed-ticket', user_agent: 'ua-1', originator: '' })
    // Changed while the login runs: the next call has the new values.
    await w.setProps({ modelValue: { user_agent: 'ua-2', originator: 'or-2' } })
    await vi.advanceTimersByTimeAsync(5000)
    expect(calls.at(-1)?.body).toEqual({ session_id: 's1', proxy_ticket: 'sealed-ticket', user_agent: 'ua-2', originator: 'or-2' })

    // The other auth method picked: its login sends them too.
    await w.setProps({ accountType: { type: 'codex_oauth' } })
    await w.get('[data-testid="codex-oauth-start"]').trigger('click')
    await flushPromises()
    expect(calls.at(-1)?.body).toEqual({ proxy_ticket: 'sealed-ticket', user_agent: 'ua-2', originator: 'or-2' })
    await w.setProps({ modelValue: { originator: 'or-3' } })
    await w.get('[data-testid="codex-oauth-callback"]').setValue(`${CALLBACK}?code=c&state=s`)
    await w.get('[data-testid="codex-oauth-complete"]').trigger('click')
    await flushPromises()
    expect(calls.at(-1)?.body).toEqual({ session_id: 'w1', callback_url: `${CALLBACK}?code=c&state=s`, proxy_ticket: 'sealed-ticket', user_agent: '', originator: 'or-3' })
  })

  it('shows the fields while creating, before any authorization', () => {
    const w = mountForm({ accountType: { type: 'codex_oauth' } })
    expect(w.find('[data-testid="codex-user-agent"]').exists()).toBe(true)
    expect(w.find('[data-testid="codex-originator"]').exists()).toBe(true)
  })
})

describe('CodexAuthForm: device code', () => {
  function deviceAnswers(polls: unknown[]) {
    answer = (path) => {
      if (path === '/auth/device/start') return { session_id: 's1', verification_url: 'https://auth.openai.com/codex/device', user_code: 'ABCD-1234', interval: 5, expires_in: 900 }
      if (path === '/auth/device/poll') return polls.length > 1 ? polls.shift() : polls[0]
      throw new Error(`unexpected ${path}`)
    }
  }

  it('starts, polls every interval and writes the credentials when done', async () => {
    deviceAnswers([{ status: 'pending' }, { status: 'done', credentials: DEVICE_CREDS }])
    const w = mountForm({ accountType: { type: 'codex_device' } })
    await w.get('[data-testid="codex-device-start"]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid="codex-device-code"]').text()).toBe('ABCD-1234')
    expect(w.get('[data-testid="codex-device-url"]').attributes('href')).toBe('https://auth.openai.com/codex/device')
    expect(w.get('[data-testid="codex-device-url"]').attributes('target')).toBe('_blank')
    expect(w.find('[data-testid="codex-device-url-copy"]').exists()).toBe(true)
    expect(w.get('[data-testid="codex-device-countdown"]').text()).toBe('device.expiresIn')
    expect(calls.filter((c) => c.path === '/auth/device/poll')).toHaveLength(0)
    // Every call carries a fresh ticket of the editor's current proxy choice.
    expect(calls[0]).toEqual({ path: '/auth/device/start', body: { proxy_ticket: 'sealed-ticket', ...NO_AGENT } })
    expect(proxyTicket).toHaveBeenCalledTimes(1)

    // The operator picks another proxy after starting: the next poll has it.
    proxyTicket.mockResolvedValue('changed-ticket')
    await vi.advanceTimersByTimeAsync(5000)
    expect(calls.filter((c) => c.path === '/auth/device/poll')).toEqual([{ path: '/auth/device/poll', body: { session_id: 's1', proxy_ticket: 'changed-ticket', ...NO_AGENT } }])
    expect(proxyTicket).toHaveBeenCalledTimes(2)
    expect(lastValue(w)).toBeUndefined()

    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(lastValue(w)).toEqual(DEVICE_CREDS)
    expect(w.get('[data-testid="codex-authorized"]').text()).toContain('done.detail')
    expect(w.find('[data-testid="codex-device-flow"]').exists()).toBe(false)

    // Nothing polls after done.
    const n = calls.length
    await vi.advanceTimersByTimeAsync(30000)
    expect(calls).toHaveLength(n)
  })

  it('stops polling when unmounted', async () => {
    deviceAnswers([{ status: 'pending' }])
    const w = mountForm({ accountType: { type: 'codex_device' } })
    await w.get('[data-testid="codex-device-start"]').trigger('click')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(5000)
    const n = calls.length
    w.unmount()
    wrapper = null
    await vi.advanceTimersByTimeAsync(60000)
    expect(calls).toHaveLength(n)
  })

  it('reports an expired code and starts again', async () => {
    deviceAnswers([{ status: 'expired' }])
    const w = mountForm({ accountType: { type: 'codex_device' } })
    await w.get('[data-testid="codex-device-start"]').trigger('click')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(w.find('[data-testid="codex-device-expired"]').exists()).toBe(true)
    await w.get('[data-testid="codex-device-retry"]').trigger('click')
    await flushPromises()
    expect(calls.filter((c) => c.path === '/auth/device/start')).toHaveLength(2)
    expect(w.find('[data-testid="codex-device-code"]').exists()).toBe(true)
  })

  it('shows the error of a failed poll with its message', async () => {
    deviceAnswers([{ status: 'error', message: 'device code login is not enabled' }])
    const w = mountForm({ accountType: { type: 'codex_device' } })
    await w.get('[data-testid="codex-device-start"]').trigger('click')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(w.get('[data-testid="codex-device-error"]').text()).toContain('device code login is not enabled')
  })

  it('shows why a pending poll could not ask OpenAI and polls on', async () => {
    deviceAnswers([{ status: 'pending', message: 'the request was not sent; check the proxy in the account form and try again' }, { status: 'pending' }, { status: 'done', credentials: DEVICE_CREDS }])
    const w = mountForm({ accountType: { type: 'codex_device' } })
    await w.get('[data-testid="codex-device-start"]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid="codex-device-notice"]').exists()).toBe(false)
    await vi.advanceTimersByTimeAsync(5000)
    expect(w.get('[data-testid="codex-device-notice"]').text()).toContain('the request was not sent')
    expect(w.find('[data-testid="codex-device-error"]').exists()).toBe(false)
    // The operator fixed the proxy: the next pending answer has no message.
    await vi.advanceTimersByTimeAsync(5000)
    expect(w.find('[data-testid="codex-device-notice"]').exists()).toBe(false)
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(lastValue(w)).toEqual(DEVICE_CREDS)
  })

  it('shows a refused proxy ticket and polls on without counting a failure', async () => {
    deviceAnswers([{ status: 'done', credentials: DEVICE_CREDS }])
    const w = mountForm({ accountType: { type: 'codex_device' } })
    await w.get('[data-testid="codex-device-start"]').trigger('click')
    await flushPromises()
    // More refusals in a row than a dropped request may fail.
    const refused = new ApiError(400, { code: 'invalid_argument', message: 'invalid', details: { fields: [{ field: 'proxy_id', code: 'disabled', message: 'the proxy is disabled' }] } })
    for (let i = 0; i < 4; i++) proxyTicket.mockRejectedValueOnce(refused)
    for (let i = 0; i < 4; i++) await vi.advanceTimersByTimeAsync(5000)
    expect(calls.filter((c) => c.path === '/auth/device/poll')).toHaveLength(0)
    expect(w.get('[data-testid="codex-device-notice"]').text()).toContain('the proxy is disabled')
    expect(w.find('[data-testid="codex-device-error"]').exists()).toBe(false)
    expect(w.find('[data-testid="codex-device-code"]').exists()).toBe(true)

    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(calls.filter((c) => c.path === '/auth/device/poll')).toHaveLength(1)
    expect(lastValue(w)).toEqual(DEVICE_CREDS)
  })

  it('drops the notice of a refused ticket when the next poll request fails', async () => {
    answer = (path) => {
      if (path === '/auth/device/start') return { session_id: 's1', verification_url: 'https://x', user_code: 'C', interval: 5, expires_in: 900 }
      if (path === '/auth/device/poll') return new Error('network down')
      throw new Error(`unexpected ${path}`)
    }
    const w = mountForm({ accountType: { type: 'codex_device' } })
    await w.get('[data-testid="codex-device-start"]').trigger('click')
    await flushPromises()
    proxyTicket.mockRejectedValueOnce(new Error('the proxy is disabled'))
    await vi.advanceTimersByTimeAsync(5000)
    expect(w.get('[data-testid="codex-device-notice"]').text()).toContain('the proxy is disabled')
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(calls.filter((c) => c.path === '/auth/device/poll')).toHaveLength(1)
    expect(w.find('[data-testid="codex-device-notice"]').exists()).toBe(false)
    expect(w.find('[data-testid="codex-device-code"]').exists()).toBe(true)
  })

  it('treats a code past its lifetime as expired without asking', async () => {
    answer = (path) => (path === '/auth/device/start' ? { session_id: 's1', verification_url: 'https://x', user_code: 'C', interval: 5, expires_in: 4 } : { status: 'pending' })
    const w = mountForm({ accountType: { type: 'codex_device' } })
    await w.get('[data-testid="codex-device-start"]').trigger('click')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(calls.filter((c) => c.path === '/auth/device/poll')).toHaveLength(0)
    expect(w.find('[data-testid="codex-device-expired"]').exists()).toBe(true)
  })
})

describe('CodexAuthForm: Sign in with Codex', () => {
  it('generates a link, exchanges the pasted callback and writes the credentials', async () => {
    answer = (path, body) => {
      if (path === '/auth/codex/start') return { session_id: 'w1', authorize_url: 'https://auth.openai.com/oauth/authorize?x=1' }
      if (path === '/auth/codex/exchange') return body.callback_url.includes('code=') ? { credentials: OAUTH_CREDS } : new Error('bad callback')
      throw new Error(`unexpected ${path}`)
    }
    const w = mountForm({ accountType: { type: 'codex_oauth' } })
    await w.get('[data-testid="codex-oauth-start"]').trigger('click')
    await flushPromises()
    expect(calls[0]).toEqual({ path: '/auth/codex/start', body: { proxy_ticket: 'sealed-ticket', ...NO_AGENT } })
    expect((w.get('[data-testid="codex-oauth-url"]').element as HTMLInputElement).value).toBe('https://auth.openai.com/oauth/authorize?x=1')
    expect(w.get('[data-testid="codex-oauth-open"]').attributes('target')).toBe('_blank')
    expect(w.find('[data-testid="codex-oauth-note"]').exists()).toBe(true)

    await w.get('[data-testid="codex-oauth-callback"]').setValue(`${CALLBACK}?state=s`)
    expect(w.find('[data-testid="codex-oauth-shape"]').text()).toBe('oauth.shape.no_code')
    await w.get('[data-testid="codex-oauth-complete"]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid="codex-oauth-error"]').text()).toContain('bad callback')

    proxyTicket.mockResolvedValue('changed-ticket')
    await w.get('[data-testid="codex-oauth-callback"]').setValue(`${CALLBACK}?code=c&state=s`)
    // The error of the previous paste goes away with the new one.
    expect(w.find('[data-testid="codex-oauth-error"]').exists()).toBe(false)
    await w.get('[data-testid="codex-oauth-complete"]').trigger('click')
    await flushPromises()
    // The exchange carries the ticket of the proxy chosen since the start.
    expect(calls.at(-1)).toEqual({ path: '/auth/codex/exchange', body: { session_id: 'w1', callback_url: `${CALLBACK}?code=c&state=s`, proxy_ticket: 'changed-ticket', ...NO_AGENT } })
    expect(lastValue(w)).toEqual(OAUTH_CREDS)
    expect(w.find('[data-testid="codex-authorized"]').exists()).toBe(true)
  })

  it('offers to try again when generating the link fails', async () => {
    let starts = 0
    answer = (path) => {
      if (path !== '/auth/codex/start') throw new Error(`unexpected ${path}`)
      return ++starts === 1 ? new Error('issuer down') : { session_id: 'w1', authorize_url: 'https://auth.openai.com/oauth/authorize?x=1' }
    }
    const w = mountForm({ accountType: { type: 'codex_oauth' } })
    await w.get('[data-testid="codex-oauth-start"]').trigger('click')
    await flushPromises()
    expect(w.get('[data-testid="codex-oauth-error"]').text()).toContain('issuer down')
    expect(w.get('[data-testid="codex-oauth-retry"]').text()).toBe('oauth.generate')
    await w.get('[data-testid="codex-oauth-retry"]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid="codex-oauth-error"]').exists()).toBe(false)
    expect(w.find('[data-testid="codex-oauth-url"]').exists()).toBe(true)
  })

  for (const code of ['session_expired', 'access_denied']) {
    it(`offers a new link inside the error when the exchange answers ${code}, and the new link clears the callback`, async () => {
      let starts = 0
      answer = (path) => {
        if (path === '/auth/codex/start') return { session_id: `w${++starts}`, authorize_url: `https://auth.openai.com/oauth/authorize?x=${starts}` }
        if (path === '/auth/codex/exchange') return new ApiError(code === 'session_expired' ? 404 : 400, { code, message: `${code} message` })
        throw new Error(`unexpected ${path}`)
      }
      const w = mountForm({ accountType: { type: 'codex_oauth' } })
      await w.get('[data-testid="codex-oauth-start"]').trigger('click')
      await flushPromises()
      await w.get('[data-testid="codex-oauth-callback"]').setValue(`${CALLBACK}?code=c&state=s`)
      await w.get('[data-testid="codex-oauth-complete"]').trigger('click')
      await flushPromises()
      expect(w.get('[data-testid="codex-oauth-error"]').text()).toContain(`${code} message`)
      expect(w.get('[data-testid="codex-oauth-retry"]').text()).toBe('oauth.regenerate')
      await w.get('[data-testid="codex-oauth-retry"]').trigger('click')
      await flushPromises()
      expect(w.find('[data-testid="codex-oauth-error"]').exists()).toBe(false)
      expect((w.get('[data-testid="codex-oauth-url"]').element as HTMLInputElement).value).toBe('https://auth.openai.com/oauth/authorize?x=2')
      expect((w.get('[data-testid="codex-oauth-callback"]').element as HTMLInputElement).value).toBe('')
    })
  }

  for (const code of ['state_mismatch', 'proxy_refused']) {
    it(`keeps a plain exchange error (${code}) without a new-link button`, async () => {
      answer = (path) => {
        if (path === '/auth/codex/start') return { session_id: 'w1', authorize_url: 'https://auth.openai.com/oauth/authorize?x=1' }
        return new ApiError(400, { code, message: `${code} message` })
      }
      const w = mountForm({ accountType: { type: 'codex_oauth' } })
      await w.get('[data-testid="codex-oauth-start"]').trigger('click')
      await flushPromises()
      await w.get('[data-testid="codex-oauth-callback"]').setValue(`${CALLBACK}?code=c&state=s`)
      await w.get('[data-testid="codex-oauth-complete"]').trigger('click')
      await flushPromises()
      expect(w.get('[data-testid="codex-oauth-error"]').text()).toContain(`${code} message`)
      expect(w.find('[data-testid="codex-oauth-retry"]').exists()).toBe(false)
    })
  }
})

describe('CodexAuthForm: editing', () => {
  const masked = { access_token: '******', refresh_token: '******', id_token: '******', email: 'old@example.com', expires_at: 1700000000, chatgpt_account_id: 'acc-2' }

  it('shows the current authorization and keeps the masked credentials until re-authorized', async () => {
    const w = mountForm({ mode: 'edit', account: { id: 7, type: 'codex_oauth' }, accountType: { type: 'codex_oauth' }, modelValue: { ...masked } })
    expect(w.get('[data-testid="codex-current"]').text()).toContain('old@example.com')
    expect(w.find('[data-testid="codex-oauth-flow"]').exists()).toBe(false)
    expect(await (w.vm as unknown as { validate: () => Promise<boolean> }).validate()).toBe(true)
    expect(w.emitted('update:modelValue')).toBeUndefined()
  })

  it('re-authorizes with the same flow', async () => {
    answer = (path) => {
      if (path === '/auth/codex/start') return { session_id: 'w2', authorize_url: 'https://auth.openai.com/a' }
      if (path === '/auth/codex/exchange') return { credentials: { ...OAUTH_CREDS, email: 'old@example.com' } }
      throw new Error(`unexpected ${path}`)
    }
    const w = mountForm({ mode: 'edit', account: { id: 7, type: 'codex_oauth' }, accountType: { type: 'codex_oauth' }, modelValue: { ...masked, proxy_note: 'kept', originator: 'or-acct' } })
    await w.get('[data-testid="codex-reauth"]').trigger('click')
    await w.get('[data-testid="codex-oauth-start"]').trigger('click')
    await flushPromises()
    // The account's own client headers, as the form holds them, go along.
    expect(calls[0]).toEqual({ path: '/auth/codex/start', body: { proxy_ticket: 'sealed-ticket', user_agent: '', originator: 'or-acct' } })
    await w.get('[data-testid="codex-oauth-callback"]').setValue(`${CALLBACK}?code=c&state=s`)
    await w.get('[data-testid="codex-oauth-complete"]').trigger('click')
    await flushPromises()
    expect(lastValue(w)).toEqual({ ...OAUTH_CREDS, email: 'old@example.com', proxy_note: 'kept', originator: 'or-acct' })
    expect(w.get('[data-testid="codex-authorized"]').text()).toContain('done.titleReauth')

    // "Authorize again" puts the loaded credentials back.
    await w.get('[data-testid="codex-again"]').trigger('click')
    expect(lastValue(w)).toEqual({ ...masked, proxy_note: 'kept', originator: 'or-acct' })
  })

  // Cancelling unmounts the flow; the answer of its call in flight is dropped.
  it('drops a device poll answered after the re-authorization was cancelled', async () => {
    let reply!: (v: unknown) => void
    answer = (path) => {
      if (path === '/auth/device/start') return { session_id: 's1', verification_url: 'https://x', user_code: 'C', interval: 5, expires_in: 900 }
      return new Promise((resolve) => (reply = resolve))
    }
    const w = mountForm({ mode: 'edit', account: { id: 7, type: 'codex_device' }, accountType: { type: 'codex_device' }, modelValue: { ...masked } })
    await w.get('[data-testid="codex-reauth"]').trigger('click')
    await w.get('[data-testid="codex-device-start"]').trigger('click')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(5000)
    await w.get('[data-testid="codex-reauth-cancel"]').trigger('click')
    reply({ status: 'done', credentials: DEVICE_CREDS })
    await flushPromises()
    expect(lastValue(w)).toBeUndefined()
    expect(w.find('[data-testid="codex-authorized"]').exists()).toBe(false)
  })

  it('drops a sign-in exchange answered after the re-authorization was cancelled', async () => {
    let reply!: (v: unknown) => void
    answer = (path) => {
      if (path === '/auth/codex/start') return { session_id: 'w1', authorize_url: 'https://auth.openai.com/a' }
      return new Promise((resolve) => (reply = resolve))
    }
    const w = mountForm({ mode: 'edit', account: { id: 7, type: 'codex_oauth' }, accountType: { type: 'codex_oauth' }, modelValue: { ...masked } })
    await w.get('[data-testid="codex-reauth"]').trigger('click')
    await w.get('[data-testid="codex-oauth-start"]').trigger('click')
    await flushPromises()
    await w.get('[data-testid="codex-oauth-callback"]').setValue(`${CALLBACK}?code=c&state=s`)
    await w.get('[data-testid="codex-oauth-complete"]').trigger('click')
    await flushPromises()
    await w.get('[data-testid="codex-reauth-cancel"]').trigger('click')
    reply({ credentials: OAUTH_CREDS })
    await flushPromises()
    expect(lastValue(w)).toBeUndefined()
    expect(w.find('[data-testid="codex-authorized"]').exists()).toBe(false)
  })

  it('cancelling a re-authorization hides the flow', async () => {
    const w = mountForm({ mode: 'edit', account: { id: 7, type: 'codex_device' }, accountType: { type: 'codex_device' }, modelValue: { ...masked } })
    await w.get('[data-testid="codex-reauth"]').trigger('click')
    expect(w.find('[data-testid="codex-device-flow"]').exists()).toBe(true)
    await w.get('[data-testid="codex-reauth-cancel"]').trigger('click')
    expect(w.find('[data-testid="codex-device-flow"]').exists()).toBe(false)
  })
})

describe('CodexAuthForm: validate()', () => {
  it('refuses to create without an access token', async () => {
    const w = mountForm({ accountType: { type: 'codex_device' } })
    expect(await (w.vm as unknown as { validate: () => Promise<boolean> }).validate()).toBe(false)
    expect(w.get('[data-testid="codex-validate-error"]').text()).toBe('validate.required')
  })

  it('says the flow is unfinished while it runs, and drops the hint when a flow starts', async () => {
    answer = (path) => (path === '/auth/device/start' ? { session_id: 's1', verification_url: 'https://x', user_code: 'C', interval: 5, expires_in: 900 } : { status: 'pending' })
    const w = mountForm({ accountType: { type: 'codex_device' } })
    const vm = w.vm as unknown as { validate: () => Promise<boolean> }
    await vm.validate()
    await w.get('[data-testid="codex-device-start"]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid="codex-validate-error"]').exists()).toBe(false)
    expect(await vm.validate()).toBe(false)
    expect(w.get('[data-testid="codex-validate-error"]').text()).toBe('validate.inProgress')
  })

  it('accepts once the editor holds the token', async () => {
    const w = mountForm({ accountType: { type: 'codex_device' }, modelValue: { ...DEVICE_CREDS } })
    expect(await (w.vm as unknown as { validate: () => Promise<boolean> }).validate()).toBe(true)
  })

  it('lists a server field error without a field of the form above it', () => {
    const w = mountForm({ accountType: { type: 'codex_device' }, errors: { access_token: 'token rejected' } })
    expect(w.get('[data-testid="credential-form-errors"]').text()).toContain('token rejected')
  })

  it('shows a server error of a client header at its field', async () => {
    const w = mountForm({ accountType: { type: 'codex_device' }, errors: { user_agent: 'bad user agent' } })
    await flushPromises()
    const input = w.get('[data-testid="codex-user-agent"]')
    expect(input.attributes('aria-invalid')).toBe('true')
    expect(document.getElementById(input.attributes('aria-describedby')!)?.textContent).toBe('bad user agent')
    expect(w.find('[data-testid="credential-form-errors"]').exists()).toBe(false)
  })
})

// The editor's proxy choice reaches the plugin as a proxy ticket asked for
// at every call of a login (CONTRACTS §117); a refused choice stops it.
describe('CodexAuthForm: proxy ticket', () => {
  it('asks for a ticket at every start and shows why it was refused', async () => {
    answer = (path) => {
      if (path === '/auth/codex/start') return { session_id: 'w1', authorize_url: 'https://auth.openai.com/a' }
      throw new Error(`unexpected ${path}`)
    }
    proxyTicket.mockRejectedValueOnce(new ApiError(400, { code: 'invalid_argument', message: 'invalid', details: { fields: [{ field: 'proxy_url', code: 'invalid', message: 'invalid proxy URL: missing port' }] } }))
    const w = mountForm({ accountType: { type: 'codex_oauth' } })
    await w.get('[data-testid="codex-oauth-start"]').trigger('click')
    await flushPromises()
    expect(calls).toHaveLength(0)
    expect(w.get('[data-testid="codex-oauth-error"]').text()).toContain('invalid proxy URL: missing port')

    proxyTicket.mockResolvedValueOnce('another-ticket')
    await w.get('[data-testid="codex-oauth-start"]').trigger('click')
    await flushPromises()
    expect(calls).toEqual([{ path: '/auth/codex/start', body: { proxy_ticket: 'another-ticket', ...NO_AGENT } }])
    expect(proxyTicket).toHaveBeenCalledTimes(2)
  })
})
