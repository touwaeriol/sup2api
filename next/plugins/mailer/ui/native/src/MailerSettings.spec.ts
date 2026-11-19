/// <reference types="vite/client" />
import { afterEach, describe, expect, it } from 'vitest'
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { ApiError } from '@sub2api/host'
import { SECRET_MASK } from '@sub2api/ui'
import { checkMessages, cleanup, mountSettings as mount } from '@sub2api/ui/testing'
import MailerSettings from './MailerSettings.vue'
import messages from './messages'
import { setHost } from './host'

type M = Record<string, any>

/** The value the form fills into empty settings (the plugin's own defaults, mailer.parseSettings). */
const DEFAULTS = { host: '', port: 587, security: 'starttls', username: '', password: '', from_address: '', from_name: 'Sub2API', timeout_seconds: 20 }

/** MailerSettings over a fake core holding `values`; the password is a secret (manifest ui.settings.secrets). */
const mountSettings = (values: M = {}, granted = ['plugin:manage']) => mount(MailerSettings, { plugin: 'mailer', setHost, values, granted, secrets: ['password'] })

afterEach(cleanup)

const input = (w: VueWrapper, id: string) => w.get(`[data-testid="mailer-${id}"]`)

describe('MailerSettings', () => {
  it('fills the defaults into unset settings and saves the whole value', async () => {
    const { w, core, settings } = await mountSettings({})
    expect(settings.value()).toEqual(DEFAULTS)
    expect(settings.dirty.value).toBe(false)
    await input(w, 'host').setValue('smtp.example.com')
    await input(w, 'from-address').setValue('noreply@example.com')
    expect(await settings.save()).toBe(true)
    expect(core.puts).toEqual([{ ...DEFAULTS, host: 'smtp.example.com', from_address: 'noreply@example.com' }])
  })

  it('keeps stored keys and fills only the absent ones (older settings)', async () => {
    const { settings } = await mountSettings({ host: 'smtp.example.com', port: 465, security: 'tls', from_address: 'a@b.co' })
    expect(settings.value()).toEqual({ ...DEFAULTS, host: 'smtp.example.com', port: 465, security: 'tls', from_address: 'a@b.co' })
  })

  it('labels every field, its help and the example values; names each security mode', async () => {
    const { w, settings } = await mountSettings({})
    for (const key of ['host.label', 'host.help', 'port.label', 'port.help', 'security.label', 'username.label', 'username.help', 'password.label', 'password.help', 'fromAddress.label', 'fromName.label', 'timeout.label', 'timeout.help', 'timeout.unit']) {
      expect(w.text()).toContain(key)
    }
    expect(input(w, 'host').attributes('placeholder')).toBe('smtp.example.com')
    expect(input(w, 'from-address').attributes('placeholder')).toBe('noreply@example.com')
    expect(w.findAll('[data-testid="mailer-security"] option').map((o) => o.text())).toEqual(['security.starttls', 'security.tls', 'security.none'])
    await input(w, 'security').setValue('1')
    expect(settings.get('security')).toBe('tls')
  })

  it('a cleared number box sends no key', async () => {
    const { w, core, settings } = await mountSettings({ host: 'smtp.example.com', from_address: 'a@b.co', port: 2525 })
    await input(w, 'port').setValue('')
    await input(w, 'timeout').setValue('')
    expect(await settings.save()).toBe(true)
    expect(core.puts[0]).not.toHaveProperty('port')
    expect(core.puts[0]).not.toHaveProperty('timeout_seconds')
    await input(w, 'port').setValue('25')
    expect(settings.get('port')).toBe(25)
  })

  describe('the password (a secret: manifest ui.settings.secrets)', () => {
    const SMTP = { host: 'smtp.example.com', from_address: 'a@b.co', username: 'ops', password: 'old-secret' }

    it('an untouched stored password goes back as "******" (kept)', async () => {
      const { w, core, settings } = await mountSettings(SMTP)
      expect(settings.get('password')).toBe(SECRET_MASK)
      expect((input(w, 'password').element as HTMLInputElement).value).toBe('')
      await input(w, 'from-name').setValue('Ops')
      expect(await settings.save()).toBe(true)
      expect(core.puts[0].password).toBe(SECRET_MASK)
      expect(core.stored().password).toBe('old-secret')
    })

    it('a typed password replaces it, and reads back masked after the save', async () => {
      const { w, core, settings } = await mountSettings(SMTP)
      await input(w, 'password').setValue('new-secret')
      expect(await settings.save()).toBe(true)
      expect(core.puts[0].password).toBe('new-secret')
      expect(core.stored().password).toBe('new-secret')
      expect(settings.get('password')).toBe(SECRET_MASK)
      expect(settings.dirty.value).toBe(false)
    })

    it('clearing it (a server without login) sends an empty password', async () => {
      const { w, core, settings } = await mountSettings(SMTP)
      await input(w, 'username').setValue('')
      await w.get('[data-testid="secret-clear"]').trigger('click')
      expect(await settings.save()).toBe(true)
      expect(core.puts[0]).toMatchObject({ username: '', password: '' })
      expect(core.stored().password).toBe('')
      expect(w.find('[data-testid="secret-clear"]').exists()).toBe(false)
    })
  })

  it("shows the plugin's field errors (ValidateSettings) at their rows", async () => {
    const { w, core, settings } = await mountSettings({})
    core.refuse(new ApiError(400, {
      code: 'invalid_argument',
      message: 'invalid',
      details: { fields: [{ field: 'values/from_address', code: 'invalid', message: 'a plain address such as noreply@example.com' }] }
    }))
    await input(w, 'host').setValue('smtp.example.com')
    expect(await settings.save()).toBe(false)
    await flushPromises()
    expect(settings.error('from_address')).toBe('a plain address such as noreply@example.com')
    expect(w.text()).toContain('a plain address such as noreply@example.com')
  })

  it('without edit rights every control is disabled and there is no save bar', async () => {
    const { w } = await mountSettings({}, [])
    expect(w.find('fieldset').attributes('disabled')).toBeDefined()
    expect(w.find('[data-testid="settings-save-bar"]').exists()).toBe(false)
  })
})

// Rules of the settings, one case per constraint: the value the form saves
// and whether it lets it be saved.
describe('MailerSettings rules', () => {
  const cases: Array<{ name: string; set: M; ok: boolean }> = [
    { name: 'defaults', set: {}, ok: true },
    { name: 'a full SMTP setup', set: { host: 'smtp.example.com', port: 465, security: 'tls', username: 'ops', password: 'secret', from_address: 'noreply@example.com', from_name: '运维', timeout_seconds: 5 }, ok: true },
    { name: 'host of 253 characters', set: { host: 'h'.repeat(253) }, ok: true },
    { name: 'host of 254 characters', set: { host: 'h'.repeat(254) }, ok: false },
    { name: 'port 0', set: { port: 0 }, ok: false },
    { name: 'port 65536', set: { port: 65536 }, ok: false },
    { name: 'port 1.5', set: { port: 1.5 }, ok: false },
    { name: 'port 65535', set: { port: 65535 }, ok: true },
    { name: 'security none', set: { security: 'none' }, ok: true },
    { name: 'unknown security', set: { security: 'ssl' }, ok: false },
    { name: 'username of 321 characters', set: { username: 'u'.repeat(321) }, ok: false },
    { name: 'password of 1001 characters', set: { password: 'p'.repeat(1001) }, ok: false },
    { name: 'password of 1000 characters', set: { password: 'p'.repeat(1000) }, ok: true },
    { name: 'from address of 321 characters', set: { from_address: 'f'.repeat(321) }, ok: false },
    { name: 'from name of 101 characters', set: { from_name: '名'.repeat(101) }, ok: false },
    { name: 'from name of 100 characters', set: { from_name: '名'.repeat(100) }, ok: true },
    { name: 'timeout 4 s', set: { timeout_seconds: 4 }, ok: false },
    { name: 'timeout 26 s', set: { timeout_seconds: 26 }, ok: false },
    { name: 'timeout 25 s', set: { timeout_seconds: 25 }, ok: true },
    // Kept as it is; the plugin's ValidateSettings refuses it (code unknown).
    { name: 'a key the form does not know', set: { other: 'x' }, ok: true }
  ]
  for (const c of cases) {
    it(c.name, async () => {
      const { settings } = await mountSettings({})
      for (const [k, v] of Object.entries(c.set)) settings.set(k, v)
      expect(settings.value()).toEqual({ ...DEFAULTS, ...c.set })
      expect(await settings.validate()).toBe(c.ok)
    })
  }
})

describe('messages', () =>
  checkMessages(messages, {
    sources: import.meta.glob<string>(['./*.vue', './*.ts', '!./*.spec.ts', '!./messages.ts'], { query: '?raw', import: 'default', eager: true }),
    // Looked up with the mode as suffix: t(`security.${value}`).
    dynamic: ['security.']
  }))
