import { computed, ref, type ComputedRef, type Ref } from 'vue'
import type { SelectOption } from '@sub2api/ui'
import { BUILTIN_GATEWAYS, isBuiltinGateway, paymentApi, useTopup, type PluginGateway } from '../topup'
import { GATEWAY_TYPES, isKnownType, type SettingsForm } from './settingsForm'

// The gateways a payment method can be served by: the built-in Epay and
// Stripe (on and off by the settings being edited) and the gateways plugins
// provide (GET /topup/gateways: on while the plugin answers, `ready`).

export interface GatewayCatalog {
  /** The plugin gateways, empty until loaded (or when the call fails). */
  plugins: Ref<PluginGateway[]>
  loaded: Ref<boolean>
  /** Every gateway as select options, built-in first. */
  options: ComputedRef<SelectOption[]>
  label(id: string): string
  /** Takes new orders: a built-in one switched on, a plugin one ready. */
  isOn(id: string): boolean
  /** The type keys the gateway knows (a plugin's templates). */
  types(id: string): string[]
  /** The display name of a type key: translated for built-in ones, the plugin's template name, else the key. */
  typeName(id: string, type: string): string
}

export function useGatewayCatalog(form: SettingsForm): GatewayCatalog {
  const t = useTopup().t
  const plugins = ref<PluginGateway[]>([])
  const loaded = ref(false)
  paymentApi
    .pluginGateways()
    .then((list) => (plugins.value = list ?? []))
    .catch(() => undefined)
    .finally(() => (loaded.value = true))

  const plugin = (id: string) => plugins.value.find((g) => g.id === id)
  const label = (id: string) => (isBuiltinGateway(id) ? t(`topup.gateway.${id}`) : (plugin(id)?.name ?? id))
  const options = computed(() => [...BUILTIN_GATEWAYS, ...plugins.value.map((g) => g.id)].map((id) => ({ value: id, label: label(id) })))
  return {
    plugins,
    loaded,
    options,
    label,
    isOn: (id) => (isBuiltinGateway(id) ? !!form.get(`${id}.enabled`) : !!plugin(id)?.ready),
    types: (id) => (isBuiltinGateway(id) ? GATEWAY_TYPES[id] : (plugin(id)?.methods.map((m) => m.type) ?? [])),
    typeName(id, type) {
      if (isBuiltinGateway(id)) return isKnownType(type) ? t(`topup.settings.methods.typeName.${type}`) : type
      return plugin(id)?.methods.find((m) => m.type === type)?.name ?? type
    }
  }
}
