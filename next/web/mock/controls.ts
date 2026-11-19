// Mock of a plugin that declares common controls (docs/COMMON-CONTROLS.md):
// one account type whose editor hides the priority and the parameter
// override, locks the weight, fixes the RPM limit, prefills header
// passthrough, a header override and a model mapping and needs a proxy; and an editor section
// (account.editor.section) whose native component edits a value of its own
// with the host's HeaderPassthroughEditor (host.components, UI 3.7). Its
// native credential form also picks entities with the host's pickers
// (docs/COMMON-CONTROLS.md §6): EntityPicker (one user), EntityMultiPicker
// (up to three groups) and ProxyPicker.

export const controlsAccountTypes = [
  {
    plugin_key: 'controls_demo',
    plugin_name: { en: 'Common controls demo', zh: '通用配置演示' },
    plugin_version: '0.1.0',
    asset_base: '/plugin-ui/controls_demo/0.1.0-dev',
    trust: 'official',
    type: 'apikey',
    label: { en: 'API Key (declared controls)', zh: 'API Key（声明通用配置）' },
    description: { en: 'Hides, locks and prefills core fields through commonControls', zh: '通过 commonControls 隐藏、锁定、预填核心字段' },
    form: { mode: 'native', component: 'ControlsDemoForm' },
    sensitive_fields: ['api_key'],
    supports: ['openai'],
    default_limits: { weight: 3, tpm_limit: 100000 },
    common_controls: {
      priority: { visible: false },
      weight: { locked: true, default: 5 },
      rpm_limit: { configurable: false, default: 60 },
      param_override: { visible: false },
      header_passthrough: { default: ['regex:^x-demo-'] },
      header_override: { default: { 'x-demo-client': 'sub2api' } },
      model_mapping: { default: { 'gpt-demo': 'gpt-4.1-mini' } },
      proxy: { required: true }
    }
  }
]

export const controlsUIPlugin = {
  key: 'controls_demo',
  version: '0.1.0',
  name: { en: 'Common controls demo', zh: '通用配置演示' },
  asset_base: '/plugin-ui/controls_demo/0.1.0-dev',
  menus: [],
  pages: {},
  slots: [
    {
      slot: 'account.editor.section',
      component: 'ControlsDemoSection',
      label: { en: 'Demo section', zh: '演示分区' },
      icon: 'puzzle',
      accountTypes: ['apikey']
    }
  ],
  native_entry: 'ui/native/entry.js',
  trust: 'official',
  host_ui_compat: '^4.0'
}

// A hand-written native entry (no build step): the section keeps its value in
// memory; the credential form stores the picked ids as numbers.
const ENTRY = `
import { defineComponent, h, ref } from 'vue'
import { nativeAccountFormProps, SCredentialForm, SField, SSecretInput, useCredentialForm } from '@sub2api/ui'
export function register(host) {
  const { EntityPicker, EntityMultiPicker, ProxyPicker } = host.components
  host.registerComponent('ControlsDemoForm', defineComponent({
    props: nativeAccountFormProps,
    emits: ['update:modelValue'],
    setup(props, { emit, expose }) {
      const form = useCredentialForm(props, emit, { rules: { api_key: { required: true } } })
      expose({ validate: form.validate })
      const num = (id) => (id ? Number(id) : null)
      return () => h(SCredentialForm, { form }, () => [
        h(SField, { prop: 'api_key', label: 'API Key' }, () => h(SSecretInput, form.bind('api_key'))),
        h(SField, { prop: 'billing_user_id', label: 'Billed user', hint: 'Usage of this account is billed to this user' }, () =>
          h(EntityPicker, { kind: 'user', id: String(form.get('billing_user_id') ?? ''), 'onUpdate:id': (id) => form.set('billing_user_id', num(id)) })),
        h(SField, { prop: 'fallback_group_ids', label: 'Fallback groups', hint: 'Up to three' }, () =>
          h(EntityMultiPicker, { kind: 'group', max: 3, ids: (form.get('fallback_group_ids') ?? []).map(String), 'onUpdate:ids': (ids) => form.set('fallback_group_ids', ids.map(Number)) })),
        h(SField, { prop: 'probe_proxy_id', label: 'Probe proxy' }, () => h(ProxyPicker, form.bind('probe_proxy_id')))
      ])
    }
  }))
  host.registerComponent('ControlsDemoSection', defineComponent({
    props: { account: Object, accountType: Object, readonly: Boolean },
    setup(props) {
      const rules = ref(['x-demo-trace'])
      return () => h('div', { class: 'card space-y-3 p-4' }, [
        h('p', { class: 'text-sm text-fg-muted' }, 'Account #' + props.account?.id + ' · ' + (props.accountType?.type || '')),
        h(host.components.HeaderPassthroughEditor, { modelValue: rules.value, disabled: props.readonly, 'onUpdate:modelValue': v => (rules.value = v) })
      ])
    }
  }))
}
`

export function controlsAsset(path: string): { type: string; body: string } | null {
  return path === 'ui/native/entry.js' ? { type: 'text/javascript', body: ENTRY } : null
}

/** The server's common control checks (handlers.go applyCommonControls), on a create or patch body. */
export function checkControls(controls: Record<string, any> | undefined, body: Record<string, any>, current?: Record<string, any>) {
  for (const [field, c] of Object.entries(controls || {})) {
    if (field === 'proxy') continue
    const fixed = c.locked || c.configurable === false
    const expected = c.locked || !current ? c.default : current[field]
    if (fixed && body[field] !== undefined && JSON.stringify(body[field]) !== JSON.stringify(expected)) return { field, code: 'read_only' }
    if (body[field] === undefined && !current && c.default !== undefined) body[field] = c.default
  }
  const proxy = 'proxy_id' in body || 'proxy_url' in body ? (body.proxy_id ?? body.proxy_url) : current?.proxy_id
  if (controls?.proxy?.required && proxy == null) return { field: 'proxy_id', code: 'required' }
  return null
}
