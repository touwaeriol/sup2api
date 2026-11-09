/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import en from '@/i18n/locales/en/plugins'
import zh from '@/i18n/locales/zh/plugins'
import { CCGATEWAY_PAGE, HOST_PERMISSION_RISK, accountTypesOf, consoleSettings, consoleSettingsPath, endpointText, hasSettingsForm, hpKey, isPluginKey, nativeSettingsPath, platformsOf, pluginSettingsPath, pluginStatusTone, settingsFieldErrors } from './pluginUtil'

describe('plugin endpoint declarations', () => {
  it('preserves the same subset in manifests and installation reviews', () => {
    const declarations = [{ platform: 'anthropic', endpoints: ['messages'] }]
    const manifest = accountTypesOf({ accountTypes: [{ id: 'managed', platforms: declarations }] })
    const review = accountTypesOf({ account_types: [{ id: 'managed', platforms: ['anthropic'], platform_declarations: declarations }] })
    expect(manifest).toEqual(review)
    expect(manifest[0].platformDeclarations).toEqual(declarations)
  })
  it('distinguishes omitted endpoint selection from an explicit empty list', () => {
    const result = accountTypesOf({
      accountTypes: [{ id: 'key', platforms: [{ platform: 'anthropic' }, { platform: 'openai', endpoints: [] }] }]
    })
    expect(result[0].platformDeclarations).toEqual([
      { platform: 'anthropic', endpoints: undefined },
      { platform: 'openai', endpoints: [] }
    ])
  })
  it('retains platform-local endpoint IDs for resolving references', () => {
    expect(
      platformsOf({
        platforms: [{ id: 'video', endpoints: [{ id: 'generate', method: 'POST', path: '/video', protocol: 'video.generate' }] }]
      })[0].endpoints[0].id
    ).toBe('generate')
  })
})

describe('settingsFieldErrors', () => {
  it('maps JSON pointer and dotted paths under values, root errors apart', () => {
    expect(settingsFieldErrors({ 'values/a/0/b': 'x', 'values.c': 'y', values: 'missing property d' })).toEqual({
      fields: { 'a.0.b': 'x', c: 'y' },
      root: ['missing property d']
    })
  })
})

describe('endpointText', () => {
  it('joins method, path and protocol', () => {
    expect(endpointText({ method: 'POST', path: '/v1/messages', protocol: 'anthropic' })).toBe('POST /v1/messages (anthropic)')
    expect(endpointText({ route: '/x' })).toBe('/x')
  })
})

describe('HOST_PERMISSION_RISK', () => {
  // Vitest runs from next/web; the SDK's table is the source of truth.
  const goTable = readFileSync('../sdk/manifest/manifest.go', 'utf8').match(/var HostPermissionRisk = map\[string\]string\{([^}]*)\}/)
  const sdkRisk = Object.fromEntries(
    [...(goTable?.[1] ?? '').matchAll(/"([^"]+)":\s*Risk(\w+)/g)].map((m) => [m[1], m[2].toLowerCase()])
  )

  it('matches sdk/manifest HostPermissionRisk', () => {
    expect(Object.keys(sdkRisk).length).toBeGreaterThan(0)
    expect(HOST_PERMISSION_RISK).toEqual(sdkRisk)
  })
  it('labels every permission in en and zh', () => {
    for (const id of Object.keys(sdkRisk)) {
      expect(en.hp, id).toHaveProperty(hpKey(id))
      expect(zh.hp, id).toHaveProperty(hpKey(id))
    }
  })
})

describe('settings modes (§75.5)', () => {
  const schema = { type: 'object' }
  it('renders only schema settings in the console', () => {
    expect(consoleSettings({ schema, values: {} })).not.toBeNull()
    expect(consoleSettings({ mode: 'schema', schema, values: {} })).not.toBeNull()
    expect(consoleSettings({ mode: 'native', page: 'dashboard', schema, values: {} })).toBeNull()
    expect(consoleSettings({ mode: 'schema', schema: null, values: {} })).toBeNull()
  })
  it('points native settings to the plugin page', () => {
    expect(nativeSettingsPath('moderation', { mode: 'native', page: 'dashboard', schema, values: {} })).toBe('/p/moderation/dashboard?tab=settings')
    expect(nativeSettingsPath('moderation', { mode: 'native', schema, values: {} })).toBeNull()
    expect(nativeSettingsPath('guard', { mode: 'schema', schema, values: {} })).toBeNull()
  })
})

describe('where the plugin list opens settings', () => {
  const all = () => true
  it('is the form on the detail page for a plugin that has one', () => {
    expect(hasSettingsForm({ key: 'mailer', has_settings: true })).toBe(true)
    expect(consoleSettingsPath({ key: 'mailer', has_settings: true }, all)).toBe('/plugins/mailer?tab=settings')
    expect(hasSettingsForm({ key: 'relay' })).toBe(false)
    expect(consoleSettingsPath({ key: 'relay' }, all)).toBeUndefined()
  })
  it("is ccgateway's own page, for settings:read, whatever its manifest says", () => {
    expect(hasSettingsForm({ key: 'ccgateway', has_settings: true })).toBe(false)
    expect(consoleSettingsPath({ key: 'ccgateway', has_settings: true }, all)).toBe(CCGATEWAY_PAGE)
    expect(consoleSettingsPath({ key: 'ccgateway' }, perm => perm !== 'settings:read')).toBeUndefined()
  })
})

describe('console addresses', () => {
  it("keeps the CCGateway page under a segment the SDK reserves for the console", () => {
    const reserved = readFileSync('../sdk/manifest/routes.go', 'utf8').match(/var ConsoleSegments = \[\]string\{([^}]*)\}/)?.[1] ?? ''
    expect(CCGATEWAY_PAGE).toBe('/system/ccgateway')
    expect([...reserved.matchAll(/"([^"]+)"/g)].map(m => m[1])).toContain(CCGATEWAY_PAGE.split('/')[1])
  })
  it("gives a plugin's settings form a page of its own", () => {
    expect(pluginSettingsPath('mailer')).toBe('/plugins/mailer/settings')
  })
})

describe('isPluginKey', () => {
  it('is the key rule of sdk/manifest/check', () => {
    const rule = readFileSync('../sdk/manifest/check/validate.go', 'utf8').match(/\bkeyRe\s*=\s*regexp\.MustCompile\(`([^`]+)`\)/)?.[1]
    expect(rule).toBe('^[a-z][a-z0-9_]{1,29}$')
    for (const key of ['mailer', 'claude_oauth', 'a1']) expect([key, isPluginKey(key)]).toEqual([key, true])
    for (const key of ['', 'a', '..', 'a/b', 'Mailer', 'guard-on', '1st', 'x'.repeat(31)]) expect([key, isPluginKey(key)]).toEqual([key, false])
  })
})

describe('plugin status tones', () => {
  it('are the plugin pages\' own, independent of the SDK statusTone', () => {
    const expected: Record<string, string> = {
      enabled: 'success', active: 'success', ready: 'success', running: 'success', ok: 'success', success: 'success', succeeded: 'success', approved: 'success',
      enabling: 'primary', upgrading: 'primary', preparing: 'primary', activating: 'primary', pending: 'primary', starting: 'primary',
      awaiting_consent: 'warning', installed: 'warning',
      failed: 'danger', error: 'danger', crashed: 'danger', rolled_back: 'danger', rejected: 'danger', revoked: 'danger', unavailable: 'danger', abnormal: 'danger',
      disabled: 'gray', stopped: 'gray', removed: 'gray', healthy: 'gray', expired: 'gray', draining: 'gray', loading: 'gray', stopping: 'gray', banned: 'gray', unknown: 'gray'
    }
    for (const [status, tone] of Object.entries(expected)) expect([status, pluginStatusTone(status)]).toEqual([status, tone])
    expect(pluginStatusTone(null)).toBe('gray')
  })
})
