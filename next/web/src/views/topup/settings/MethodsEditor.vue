<script setup lang="ts">
// Methods tab: the payment methods users pick from, in the order shown to
// them (drag to reorder), each served by one gateway. Methods are added
// from a template or the dialog; methods whose gateway is off are flagged.
import { computed, ref } from 'vue'
import { SAlert, SBadge, SButton, SDropdown, SEmpty, SRowAction, SSettingsGroup, SSwitch, STable, type MenuAction, type TableColumn } from '@sub2api/ui'
import { useTopup, type GatewayKey, type PaymentMethod } from '../topup'
import MethodDialog from './MethodDialog.vue'
import MethodName from '../components/MethodName.vue'
import { bindSwitch, GATEWAYS, newMethod, TEMPLATE_KEYS, templateType, type SettingsForm, type SettingsTab, type TemplateKey } from './settingsForm'

const props = defineProps<{ form: SettingsForm }>()
const emit = defineEmits<{ (e: 'go', tab: SettingsTab): void }>()
const host = useTopup()
const t = host.t

const methods = computed(() => props.form.get<PaymentMethod[]>('methods') ?? [])
const setMethods = (list: PaymentMethod[]) => props.form.set('methods', list)
const gatewayOn = (g: GatewayKey) => !!props.form.get(`${g}.enabled`)
const taken = computed(() => new Set(methods.value.map((m) => m.type)))

/** Gateways that are off while enabled methods use them. */
const idleGateways = computed(() =>
  GATEWAYS.map((gateway) => ({ gateway, count: methods.value.filter((m) => m.gateway === gateway && m.enabled).length })).filter((g) => g.count && !gatewayOn(g.gateway))
)
/** Top-up is on but users would see no method. */
const noneUsable = computed(() => !!props.form.get('enabled') && !methods.value.some((m) => m.enabled && gatewayOn(m.gateway)))

const columns = computed<TableColumn[]>(() => [
  { key: 'name', label: t('topup.settings.methods.name') },
  { key: 'type', label: t('topup.settings.methods.type'), nowrap: true },
  { key: 'gateway', label: t('topup.settings.methods.gateway'), nowrap: true },
  { key: 'min_topup', label: t('topup.settings.methods.minTopup'), nowrap: true },
  { key: 'enabled', label: t('topup.settings.methods.enabled'), width: '5rem' },
  { key: 'actions', label: '', width: '7rem' }
])

const templateActions = computed<MenuAction[]>(() =>
  TEMPLATE_KEYS.map((key) => {
    const type = templateType(key)
    const added = !!type && taken.value.has(type)
    return { key, label: t(`topup.settings.methods.template.${key}`) + (added ? ` · ${t('topup.settings.methods.taken')}` : ''), disabled: added, divider: !type }
  })
)

/** The method in the dialog: index -1 adds it. */
const editing = ref<{ index: number; method: PaymentMethod } | null>(null)
const dialogOpen = ref(false)
const otherTypes = computed(() => methods.value.filter((_, i) => i !== editing.value?.index).map((m) => m.type))

function open(index: number, method: PaymentMethod) {
  editing.value = { index, method }
  dialogOpen.value = true
}

function save(m: PaymentMethod) {
  const i = editing.value?.index ?? -1
  setMethods(i < 0 ? [...methods.value, m] : methods.value.map((x, j) => (j === i ? m : x)))
  dialogOpen.value = false
}

function remove(index: number) {
  setMethods(methods.value.filter((_, j) => j !== index))
}

/** Ready-made templates are added at once; the custom one opens the dialog. */
function fromTemplate(key: string) {
  const method = newMethod(t, key as TemplateKey)
  if (!method.type) return open(-1, method)
  setMethods([...methods.value, method])
  host.toast(t('topup.settings.methods.added', { name: method.name }), 'success')
}
</script>

<template>
  <SAlert v-for="g in idleGateways" :key="g.gateway" tone="warning" dense data-testid="payment-idle-gateway">
    {{ t('topup.settings.methods.gatewayOff', { gateway: t(`topup.gateway.${g.gateway}`), count: g.count }) }}
    <template #actions>
      <SButton size="sm" @click="emit('go', 'gateways')">{{ t('topup.settings.methods.toGateways') }}</SButton>
    </template>
  </SAlert>
  <SAlert v-if="noneUsable" tone="danger" dense>{{ t('topup.settings.methods.noneUsable') }}</SAlert>

  <SSettingsGroup :title="t('topup.settings.methods.title')" :description="t('topup.settings.methods.help')">
    <template #actions>
      <SDropdown :actions="templateActions" width="14rem" @select="fromTemplate">
        <template #trigger="{ toggle, attrs }">
          <SButton size="sm" icon-right="chevron-down" v-bind="attrs" data-testid="payment-method-templates" @click="toggle">{{ t('topup.settings.methods.templates') }}</SButton>
        </template>
      </SDropdown>
      <SButton size="sm" variant="primary" icon="plus" data-testid="payment-method-add" @click="open(-1, newMethod(t))">{{ t('topup.settings.methods.add') }}</SButton>
    </template>
    <div class="payment-group-body">
      <SEmpty v-if="!methods.length" :text="t('topup.settings.methods.empty')" icon="balance" />
      <STable v-else :rows="methods" :columns="columns" row-key="type" reorderable dense @update:rows="setMethods">
        <template #cell-name="{ row }">
          <MethodName :method="row" size="md" />
        </template>
        <template #cell-type="{ row }"><code class="payment-code">{{ row.type }}</code></template>
        <template #cell-gateway="{ row }">
          <span class="payment-method-name">
            {{ t(`topup.gateway.${row.gateway}`) }}
            <SBadge v-if="!gatewayOn(row.gateway)" tone="warning" size="xs">{{ t('topup.settings.methods.gatewayOffBadge') }}</SBadge>
          </span>
        </template>
        <template #cell-min_topup="{ row }">
          <span v-if="row.min_topup">{{ host.i18n.formatMoney(row.min_topup, 2) }}</span>
          <span v-else class="payment-muted">{{ t('topup.settings.methods.minGlobal') }}</span>
        </template>
        <template #cell-enabled="{ index }">
          <SSwitch size="sm" v-bind="bindSwitch(form, `methods.${index}.enabled`)" :aria-label="t('topup.settings.methods.enabled')" />
        </template>
        <template #cell-actions="{ row, index }">
          <SRowAction :label="t('topup.action.edit')" @click="open(index, { ...row })" />
          <SRowAction :label="t('topup.action.delete')" tone="danger" @click="remove(index)" />
        </template>
      </STable>
    </div>
  </SSettingsGroup>

  <MethodDialog v-if="editing" v-model:open="dialogOpen" :method="editing.method" :editing="editing.index >= 0" :taken="otherTypes" @submit="save" />
</template>
