import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { SFormModal, SSelect, type SelectOption } from '@sub2api/ui'
import { i18n } from '@/i18n'
import type { PluginGateway } from '../topup'
import { useGatewayCatalog } from './gatewayCatalog'
import MethodDialog from './MethodDialog.vue'
import { newMethod, type MethodModel } from './settingsForm'
import type { TopupSettingsForm } from './useTopupSettings'

const api = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('@sub2api/host', async orig => ({ ...(await orig<object>()), api }))
enableAutoUnmount(afterEach)

const PLUGIN: PluginGateway = {
  id: '_example__alipay_direct.alipay',
  plugin_key: '_example__alipay_direct',
  plugin_name: 'Alipay direct',
  name: 'Alipay (official)',
  ready: true,
  methods: [{ type: 'alipay_qr', name: 'Face-to-face QR' }],
  in_use: 0,
  notify_url: 'https://site.example/api/v1/payment/notify/_example__alipay_direct.alipay'
}
const DOWN: PluginGateway = { ...PLUGIN, id: 'gone.pay', name: 'gone.pay', ready: false, methods: [] }
/** The settings being edited: Epay on, Stripe off. */
const form = { get: (path: string) => ({ 'epay.enabled': true, 'stripe.enabled': false })[path] } as unknown as TopupSettingsForm

async function catalog() {
  const c = useGatewayCatalog(form)
  await flushPromises()
  return c
}

describe('payment gateways of a method', () => {
  beforeEach(() => {
    i18n.global.locale.value = 'en'
    api.get.mockResolvedValue([PLUGIN, DOWN])
  })

  it('lists the built-in and the plugin gateways, on by the settings or by the plugin answering', async () => {
    const c = await catalog()
    expect(api.get).toHaveBeenCalledWith('/topup/gateways')
    expect(c.options.value.map(o => o.value)).toEqual(['epay', 'stripe', PLUGIN.id, DOWN.id])
    expect(c.label(PLUGIN.id)).toBe('Alipay (official)')
    expect(c.label('epay')).toBe('Epay')
    expect([c.isOn('epay'), c.isOn('stripe'), c.isOn(PLUGIN.id), c.isOn(DOWN.id), c.isOn('unknown.x')]).toEqual([
      true,
      false,
      true,
      false,
      false
    ])
    expect(c.types(PLUGIN.id)).toEqual(['alipay_qr'])
    expect(c.typeName(PLUGIN.id, 'alipay_qr')).toBe('Face-to-face QR')
  })

  it('adds a method served by a plugin gateway from its template', async () => {
    const gateways = await catalog()
    const method: MethodModel = newMethod(k => k)
    const w = mount(MethodDialog, {
      attachTo: document.body,
      props: { open: false, method, editing: false, taken: [], gateways },
      global: { plugins: [i18n] }
    })
    await w.setProps({ open: true })
    const [gateway] = w.findAllComponents(SSelect)
    expect(gateway.props('options').map((o: SelectOption) => o.value)).toContain(PLUGIN.id)

    // One known type: picking the gateway picks it, with its name and icon.
    gateway.vm.$emit('update:modelValue', PLUGIN.id)
    await flushPromises()
    w.findComponent(SFormModal).vm.$emit('submit')
    await flushPromises()
    expect(w.emitted('submit')?.[0][0]).toMatchObject({ gateway: PLUGIN.id, type: 'alipay_qr', name: 'Face-to-face QR', icon: 'alipay_qr' })
  })

  it("refuses type keys the core would refuse and names beyond the core's length", async () => {
    const gateways = await catalog()
    const method = { ...newMethod(k => k), type: 'ali-pay', name: 'x'.repeat(101) }
    const w = mount(MethodDialog, {
      attachTo: document.body,
      props: { open: false, method, editing: true, taken: [], gateways },
      global: { plugins: [i18n] }
    })
    await w.setProps({ open: true })
    w.findComponent(SFormModal).vm.$emit('submit')
    await flushPromises()
    expect(w.emitted('submit')).toBeUndefined()
    expect(document.body.textContent).toContain(i18n.global.t('topup.settings.methods.rule.typeKey'))
  })
})
