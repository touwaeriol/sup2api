import { describe, expect, expectTypeOf, it } from 'vitest'
import * as ui from './index'
import { HOST_COMPONENT_NAMES, HOST_UI_VERSION, MAX_ACCOUNT_BATCH, satisfiesRange } from '@sub2api/host'
import { HOST_UI_VERSION as PRESET_UI_VERSION } from '@sub2api/vite-preset'
import hostPackage from '../../host/package.json'
import presetPackage from '../../vite-preset/package.json'
import uiPackage from '../package.json'
import readme from '../README.md?raw'

describe('host UI 4 contract', () => {
  it('exposes only enhanced STabs without layout aliases', () => {
    expect(ui.STabs).toBeDefined()
    expect('STabLayout' in ui).toBe(false)
    expect('TabBar' in ui).toBe(false)
  })
  it('rejects plugins built for UI 3 or older and accepts ^4.0 to ^4.2', () => {
    expect(HOST_UI_VERSION).toBe('4.2.0')
    expect(satisfiesRange(HOST_UI_VERSION, '^4.1')).toBe(true)
    expect(satisfiesRange(HOST_UI_VERSION, '^4.2')).toBe(true)
    expect(satisfiesRange(HOST_UI_VERSION, '^4.3')).toBe(false)
    for (const range of ['^1.0', '^2.0', '^3.0', '^3.20']) expect(satisfiesRange(HOST_UI_VERSION, range), range).toBe(false)
    expect(satisfiesRange(HOST_UI_VERSION, '^4.0')).toBe(true)
    expect(satisfiesRange('3.20.0', '^4.0')).toBe(false)
    for (let n = 1; n <= 20; n++) {
      expect(satisfiesRange(`3.${n}.0`, `^3.${n}`)).toBe(true)
      expect(satisfiesRange(`3.${n - 1}.0`, `^3.${n}`)).toBe(false)
    }
  })
  it('declares ^4.0 in the README section of UI 4.0', () => {
    expect(readme).toContain('（UI SDK 4.0）')
    expect(readme).toContain('hostUICompat: "^4.0"')
  })
  it('declares in each README section of UI 3.N the range ^3.N and the UI it follows', () => {
    const sections = readme.split(/^## /m).flatMap((body) => {
      const minor = /^[^\n]*（UI SDK 3\.(\d+)）/.exec(body)?.[1]
      return minor ? [{ minor: Number(minor), body }] : []
    })
    expect(sections.map((s) => s.minor)).toContain(20)
    for (const { minor, body } of sections) {
      for (const m of body.matchAll(/hostUICompat: "\^3\.(\d+)"/g)) expect(Number(m[1]), `UI 3.${minor}`).toBe(minor)
      for (const m of body.matchAll(/(?:与 3\.(\d+) 相同|最后一个带 UI 3\.(\d+) 的核心)/g)) expect(Number(m[1] ?? m[2]), `UI 3.${minor}`).toBe(minor - 1)
    }
  })
  it('keeps the plugin preset and the shared packages on the host UI version', () => {
    expect(PRESET_UI_VERSION).toBe(HOST_UI_VERSION)
    expect([hostPackage.version, uiPackage.version, presetPackage.version]).toEqual([HOST_UI_VERSION, HOST_UI_VERSION, HOST_UI_VERSION])
  })
  it('has no JSON Schema form (UI 4.0): plugin UIs are code only', () => {
    for (const name of ['SchemaForm', 'SchemaField', 'SCHEMA_WIDGETS_KEY', 'validateSchema']) {
      expect(name in ui, name).toBe(false)
    }
    expect(ui.localizedText({ en: 'Name', zh: '名称' }, 'zh')).toBe('名称')
  })
  it('exports the form validation framework (UI 4.0)', async () => {
    for (const name of ['SForm', 'useForm', 'setPath', 'getPath', 'SECRET_MASK'] as const) expect(ui[name]).toBeDefined()
    expect(ui.SECRET_MASK).toBe('******')
    expectTypeOf<ui.Trigger>().toEqualTypeOf<'change' | 'blur'>()
    expectTypeOf<ReturnType<ui.FormApi['validate']>>().toEqualTypeOf<Promise<boolean>>()
    const host = await import('@sub2api/host')
    expect(host.toFieldPath('credentials.a.b')).toBe('a.b')
    expect(host.errorMessage(new TypeError('x'), (k) => k)).toBe('auth.errors.network')
    expect(host.settingsFieldErrors({ 'values/a/b': 'x', values: 'y' })).toEqual({ fields: { 'a.b': 'x' }, root: ['y'] })
  })
  it('exports the form building blocks (UI 4.0)', async () => {
    for (const name of ['SSecretInput', 'SUrlPresetInput', 'SCredentialForm', 'SFormSection', 'SDateTimeInput', 'useCredentialForm', 'nativeAccountFormProps', 'createApiKeyForm', 'usePluginSettings', 'SSettingsPage', 'SResourcePage'] as const) {
      expect(ui[name]).toBeDefined()
    }
    expect((await import('@sub2api/host')).useGuardedSettings).toBeTypeOf('function')
    expectTypeOf<ui.NativeAccountFormProps['mode']>().toEqualTypeOf<'create' | 'edit'>()
    expect(Object.keys(ui.nativeAccountFormProps)).toEqual(['modelValue', 'mode', 'account', 'accountType', 'errors', 'proxyTicket'])
    expectTypeOf<ReturnType<ui.FormApi['bindNumber']>>().toEqualTypeOf<ReturnType<ui.FormApi['bind']>>()
  })
  it('exports the tabbed form saved by one request (UI 4.1)', () => {
    for (const name of ['STabbedForm', 'useTabbedForm', 'useTabbedFormPart'] as const) expect(ui[name]).toBeDefined()
    expectTypeOf<ui.FormTab['summary']>().toEqualTypeOf<string | undefined>()
    expectTypeOf<ReturnType<ui.TabbedForm['submit']>>().toEqualTypeOf<Promise<unknown>>()
    expect(readme).toContain('（UI SDK 4.1）')
    expect(readme).toContain('hostUICompat: "^4.1"')
  })
  it('keeps page actions in the page: tab bar end, auto-refresh button (UI 4.2)', () => {
    expect(readme).toContain('（UI SDK 4.2）')
    expect(readme).toContain('hostUICompat: "^4.2"')
    expectTypeOf<ui.PageHeadSink>().not.toHaveProperty('actions')
  })
  it('exports the horizontal clip probe the permission matrix uses (UI 3.16)', () => {
    expect(ui.useScrollClip).toBeDefined()
  })
  it('exports more filters, applied filter chips and one-line chip lists (UI 3.10)', () => {
    expect(ui.SFilterChips).toBeDefined()
    expect(ui.SChipList).toBeDefined()
    expect(ui.ICON_NAMES).toContain('image')
    expectTypeOf<ui.FilterChipItem>().toEqualTypeOf<{ key: string; label: string; value: string }>()
    expectTypeOf<ui.TableColumn['grow']>().toEqualTypeOf<boolean | undefined>()
  })
  it('names the core editors (UI 3.7, the header override 3.19) and the entity pickers (UI 3.11, the multiple one 3.12) handed to plugins', () => {
    expect(HOST_COMPONENT_NAMES).toEqual([
      'ParamOverrideEditor',
      'RateLimitEditor',
      'ModelLimitsEditor',
      'HeaderPassthroughEditor',
      'ProxyPicker',
      'ModelMappingEditor',
      'EntityPicker',
      'EntityMultiPicker',
      'HeaderOverrideEditor'
    ])
  })
  it('locks the account batch size shared with the core (UI 3.6)', () => {
    expect(MAX_ACCOUNT_BATCH).toBe(500)
  })
  it('exports the phone forms (UI 3.5), keeping the media queries internal', () => {
    expect(ui.useViewport).toBeTypeOf('function')
    expect(ui.provideViewport).toBeTypeOf('function')
    expect(ui.dismissTopLayer).toBeTypeOf('function')
    expectTypeOf<ui.Viewport['compact']['value']>().toEqualTypeOf<boolean>()
    expectTypeOf<ui.ModalMobile>().toEqualTypeOf<'auto' | 'sheet' | 'fullscreen' | 'center'>()
    expect('WIDE_QUERY' in ui).toBe(false)
    expect('TOUCH_QUERY' in ui).toBe(false)
  })
  it('exports the shared page building blocks (UI 3.4)', () => {
    for (const name of [
      'SStatusBadge',
      'statusTone',
      'SFormModal',
      'SFilterBar',
      'SAutoRefresh',
      'useAutoRefresh',
      'useNow',
      'lazyPage'
    ] as const) {
      expect(ui[name]).toBeDefined()
    }
  })
  it('exports listAll from the host (UI 3.3)', async () => {
    expect((await import('@sub2api/host')).listAll).toBeTypeOf('function')
  })
  it('exports the bulk selection bar (UI 3.2)', () => {
    expect(ui.SBulkBar).toBeDefined()
  })
  it('exports column preferences for native plugins and custom layouts', () => {
    expect(ui.SIPAddress).toBeDefined()
    expect(ui.STableColumnSettings).toBeDefined()
    expect(ui.useTableColumns).toBeTypeOf('function')
    expect(ui.TABLE_COLUMN_SCOPE_KEY).toBeTypeOf('symbol')
  })
})
