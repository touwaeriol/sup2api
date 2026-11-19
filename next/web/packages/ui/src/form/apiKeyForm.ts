// createApiKeyForm: the native account form of an API key type (api_key,
// an optional base_url, a few extra fields) as one call:
//   host.registerComponent('AnthropicKeyForm', createApiKeyForm({
//     baseUrl: { presets: ['https://api.anthropic.com'], placeholder: () => host.t('baseUrl.placeholder') }
//   }))
// The component takes the §23.5 props and exposes validate(). base_url
// follows useGuardedSettings: without account:settings:custom it offers the
// type's official values only.
import { defineComponent, h, type Component, type VNode } from 'vue'
import { useGuardedSettings, type GuardedSettings } from '@sub2api/host'
import SDateTimeInput from '../SDateTimeInput.vue'
import SField from '../SField.vue'
import SInput from '../SInput.vue'
import SSecretInput from '../SSecretInput.vue'
import SSelect from '../SSelect.vue'
import SSwitch from '../SSwitch.vue'
import STextarea from '../STextarea.vue'
import SUrlPresetInput from '../SUrlPresetInput.vue'
import { useUiT } from '../i18n'
import SCredentialForm from './SCredentialForm.vue'
import SFormSection from './SFormSection.vue'
import { nativeAccountFormProps, useCredentialForm } from './credential'
import { toList, type Rule, type Rules } from './rules'
import type { FormApi } from './useForm'

type Model = Record<string, unknown>
type Text = () => string

export interface ApiKeyFormSpec {
  /** By default the key only has to be non-blank; min, max and pattern apply only when given. */
  apiKey?: { min?: number; max?: number; pattern?: RegExp; placeholder?: Text; help?: Text }
  /** false: the type has no base_url. */
  baseUrl?:
    | false
    | {
        required?: boolean
        presets?: string[]
        /** Default /^(https?:\/\/[^\s?#]+)?$/, required: /^https?:\/\/[^\s?#]+$/. */
        pattern?: RegExp
        /** The message of a value the pattern refuses (default: the generic "invalid format"). */
        message?: Text
        /** Default: this package's "Base URL". */
        label?: Text
        placeholder?: Text
        help?: Text
        /** Shown above the API key (a site's address before its key). */
        first?: boolean
      }
  /** Further fields, in order; each is one SField. */
  extra?: ExtraField[]
  /** Filled into a new account's absent keys. */
  defaults?: Model
}

export interface ExtraField {
  key: string
  kind: 'text' | 'secret' | 'url' | 'select' | 'textarea' | 'date-time' | 'switch'
  label: Text
  help?: Text
  placeholder?: Text
  /** select: the choices. */
  options?: { value: string; label: Text }[]
  /** url: suggestions; a field in the type's guarded_settings offers its official values only, as base_url does. */
  presets?: string[]
  rules?: Rule | Rule[]
  /**
   * Starts a section (SFormSection) at this field: its title, or the title
   * with a help line and `collapsed`. The fields after it with the same
   * title or none belong to it too.
   */
  section?: Text | FieldSection
  /** Shown and validated only while true. */
  when?: (m: Model) => boolean
  /** text, textarea: a monospaced box (keys, ids, paths), as SInput's `mono`. */
  mono?: boolean
}

/** A section of extra fields (SFormSection): `collapsed` starts it closed unless one of its fields holds a value or an error. */
export interface FieldSection {
  title: Text
  help?: Text
  collapsed?: boolean
}

const BASE_URL = /^(https?:\/\/[^\s?#]+)?$/
const BASE_URL_REQUIRED = /^https?:\/\/[^\s?#]+$/

/** The rules of the spec: api_key, base_url, then each extra field's (gated by its `when`). */
function specRules(spec: ApiKeyFormSpec): Rules<Model> {
  const k = spec.apiKey ?? {}
  const rules: Rules<Model> = { api_key: { required: true, whitespace: true, min: k.min, max: k.max, pattern: k.pattern } }
  const b = spec.baseUrl ?? {}
  if (b) rules.base_url = [{ required: b.required }, { pattern: b.pattern ?? (b.required ? BASE_URL_REQUIRED : BASE_URL), message: b.message }]
  for (const f of spec.extra ?? []) {
    const when = f.when
    rules[f.key] = toList(f.rules).map((r) => (when ? { ...r, when: (m: Model) => when(m) && (!r.when || r.when(m)) } : r))
  }
  return rules
}

/** The control of an extra field, bound to the form. */
function control(f: ExtraField, form: FormApi<Model>, guard: GuardedSettings): VNode {
  const bound = form.bind(f.key)
  const set = (v: unknown) => form.set(f.key, v)
  const placeholder = f.placeholder?.()
  switch (f.kind) {
    case 'secret':
      // An optional secret can be cleared; a required one only replaced.
      return h(SSecretInput, { ...bound, placeholder, clearable: !toList(f.rules).some((r) => r.required) })
    case 'url':
      return h(SUrlPresetInput, { ...bound, presets: f.presets, allowed: guard.allowed(f.key), placeholder })
    case 'select':
      return h(SSelect, {
        modelValue: bound.modelValue,
        options: (f.options ?? []).map((o) => ({ value: o.value, label: o.label() })),
        placeholder,
        'onUpdate:modelValue': (v: unknown) => set(v ?? undefined)
      })
    case 'textarea':
      return h(STextarea, { ...bound, placeholder, mono: f.mono })
    case 'date-time':
      return h(SDateTimeInput, bound)
    case 'switch':
      return h(SSwitch, { ...bound, modelValue: !!bound.modelValue })
    default:
      return h(SInput, { ...bound, placeholder, mono: f.mono })
  }
}

type Group = { section?: FieldSection; title?: string; fields: ExtraField[] }

/** The shown extra fields grouped by section: a field with a new title starts one, the others join the current. */
function sections(fields: ExtraField[]): Group[] {
  const out: Group[] = []
  for (const f of fields) {
    const section = typeof f.section === 'function' ? { title: f.section } : f.section
    const title = section?.title()
    const last = out.at(-1)
    if (last && (title === undefined || title === last.title)) last.fields.push(f)
    else out.push({ section, title, fields: [f] })
  }
  return out
}

export function createApiKeyForm(spec: ApiKeyFormSpec): Component {
  return defineComponent({
    name: 'ApiKeyForm',
    props: nativeAccountFormProps,
    emits: { 'update:modelValue': (_v: Model) => true },
    setup(props, { emit, expose }) {
      const t = useUiT()
      const form = useCredentialForm(props, (_e, v) => emit('update:modelValue', v), { rules: specRules(spec), defaults: spec.defaults })
      const guard = useGuardedSettings(() => props.accountType)
      expose({ validate: form.validate })

      const field = (key: string, label: string, help: Text | undefined, child: VNode) => h(SField, { key, prop: key, label, hint: help?.() }, () => child)
      return () => {
        const k = spec.apiKey ?? {}
        const b = spec.baseUrl ?? {}
        const nodes: VNode[] = [
          field('api_key', t('ui.form.apiKey'), k.help, h(SSecretInput, { ...form.bind('api_key'), placeholder: k.placeholder?.() }))
        ]
        if (b) {
          const url = field(
            'base_url',
            b.label?.() ?? t('ui.form.baseUrl'),
            b.help,
            h(SUrlPresetInput, { ...form.bind('base_url'), presets: b.presets, allowed: guard.allowed('base_url'), placeholder: b.placeholder?.() })
          )
          if (b.first) nodes.unshift(url)
          else nodes.push(url)
        }
        const extra = (f: ExtraField) => field(f.key, f.label(), f.help, control(f, form, guard))
        for (const g of sections((spec.extra ?? []).filter((f) => !f.when || f.when(form.value())))) {
          if (!g.section) nodes.push(...g.fields.map(extra))
          else
            nodes.push(
              h(
                SFormSection,
                { key: `section-${g.title}`, title: g.section.title(), help: g.section.help?.(), collapsed: g.section.collapsed, fields: g.fields.map((f) => f.key) },
                () => g.fields.map(extra)
              )
            )
        }
        return h(SCredentialForm, { form }, () => nodes)
      }
    }
  })
}
