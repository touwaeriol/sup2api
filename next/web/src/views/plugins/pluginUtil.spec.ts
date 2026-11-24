/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import en from '@/i18n/locales/en/plugins'
import zh from '@/i18n/locales/zh/plugins'
import { i18n } from '@/i18n'
import {
  HOST_PERMISSION_RISK,
  OFFICIAL_PUBLISHER,
  accountTypesOf,
  consoleSettings,
  consoleSettingsPath,
  displayPluginId,
  endpointText,
  hpKey,
  isPluginKey,
  nativeSettingsPath,
  parsePluginId,
  platformsOf,
  pluginSettingsPath,
  pluginStatusTone,
  refusalText,
  tablePrefix
} from './pluginUtil'

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
  it('shows native settings without a page on the settings tab', () => {
    expect(consoleSettings({ mode: 'native', component: 'MailerSettings' })).not.toBeNull()
    expect(consoleSettings({ mode: 'native', page: 'dashboard' })).toBeNull()
    expect(consoleSettings({ mode: 'native', page: 'dashboard', component: 'X' })).toBeNull()
    expect(consoleSettings({ mode: 'native' })).toBeNull()
    expect(consoleSettings({ mode: 'iframe', component: 'X' })).toBeNull()
    expect(consoleSettings({ mode: 'none' })).toBeNull()
  })
  it('points native settings to the plugin page', () => {
    expect(nativeSettingsPath('moderation', { mode: 'native', page: 'dashboard' })).toBe('/p/moderation/dashboard?tab=settings')
    expect(nativeSettingsPath('moderation', { mode: 'native' })).toBeNull()
    expect(nativeSettingsPath('guard', { mode: 'iframe', page: 'x' })).toBeNull()
  })
})

describe('where the installed plugins list opens settings', () => {
  it('is the settings tab of the detail page for a plugin with settings', () => {
    expect(consoleSettingsPath({ key: 'mailer', has_settings: true })).toBe('/plugins/mailer?tab=settings')
    expect(consoleSettingsPath({ key: 'relay' })).toBeUndefined()
  })
})

describe('console addresses', () => {
  it("gives a plugin's settings form a page of its own", () => {
    expect(pluginSettingsPath('mailer')).toBe('/plugins/mailer/settings')
  })
})

describe('isPluginKey', () => {
  it('is the key rule of sdk/manifest/check', () => {
    const rule = readFileSync('../sdk/manifest/check/validate.go', 'utf8').match(/\bpluginKeyRe\s*=\s*regexp\.MustCompile\(`([^`]+)`\)/)?.[1]
    expect(rule).toBe('^[a-z](_?[a-z0-9]){1,29}$')
    for (const key of ['mailer', 'azure_openai', 'a1', 'x'.repeat(30)]) expect([key, isPluginKey(key)]).toEqual([key, true])
    for (const key of ['', 'a', '..', 'a/b', 'Mailer', 'guard-on', '1st', 'a__b', 'guard_', 'x'.repeat(31), 'a_'.repeat(15) + 'b']) expect([key, isPluginKey(key)]).toEqual([key, false])
  })
  it('takes a third-party plugin id: a valid publisher and key, at most 30 bytes in all', () => {
    const rule = readFileSync('../sdk/manifest/check/publisher.go', 'utf8').match(/\bpublisherIDRe\s*=\s*regexp\.MustCompile\(`([^`]+)`\)/)?.[1]
    expect(rule).toBe('^[a-z0-9](_?[a-z0-9]){0,26}$')
    for (const id of ['_erio__codex', '_a1__x1', '_my_team__billing_bridge']) expect([id, isPluginKey(id)]).toEqual([id, true])
    const bad = ['_erio__', '__codex', '_erio_codex', '_Erio__codex', '_erio-x__codex', '_sub2api__codex', '_erio__Codex', '_er__io__codex', `_erio__${'x'.repeat(24)}`]
    for (const id of bad) expect([id, isPluginKey(id)]).toEqual([id, false])
  })
})

describe('plugin ids (manifest.ParsePluginID, DisplayID)', () => {
  it('reads both parts back and shows another publisher as publisher/key', () => {
    expect(parsePluginId('codex')).toEqual({ publisher: OFFICIAL_PUBLISHER, key: 'codex' })
    expect(parsePluginId('_erio__codex')).toEqual({ publisher: 'erio', key: 'codex' })
    expect(parsePluginId('_my_team__a_b')).toEqual({ publisher: 'my_team', key: 'a_b' })
    for (const id of ['', '_', '_erio', '_erio__', '___codex']) expect([id, parsePluginId(id)]).toEqual([id, null])
    expect(['codex', '_erio__codex', '_sub2api__codex', '_broken'].map(displayPluginId)).toEqual(['codex', 'erio/codex', '_sub2api__codex', '_broken'])
  })
  it('prefixes the tables of a plugin with its id', () => {
    expect(tablePrefix('codex')).toBe('plg_codex__')
    expect(tablePrefix('_erio__codex')).toBe('plg__erio__codex__')
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

describe('refusalText', () => {
  const refusal = { code: 'outdated_contract', version: '0.1.0', host_version: '0.2.3', protocol_version: 2, first_core_version: '0.2.0', message: 'plugin video@0.1.0 was built for an older plugin protocol' }
  it('localises a refusal of the older plugin contract', () => {
    i18n.global.locale.value = 'zh'
    expect(refusalText(i18n.global.t, 'video', refusal)).toBe('插件 video@0.1.0 是按旧的插件协议构建的，本核心（0.2.3，插件协议 2）不能加载它。请安装为核心 0.2.0 及以上构建的版本。')
    i18n.global.locale.value = 'en'
    expect(refusalText(i18n.global.t, 'video', refusal)).toBe(
      'Plugin video@0.1.0 was built for an older plugin protocol and cannot be loaded by this core (0.2.3, plugin protocol 2). Install a version built for core 0.2.0 or later.'
    )
  })
  it('shows the runtime text of a refusal it does not know', () => {
    expect(refusalText(i18n.global.t, 'video', { ...refusal, code: 'other' })).toBe(refusal.message)
  })
})
