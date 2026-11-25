<script setup lang="ts">
// Add / edit one payment method (a draft; the page saves the whole list).
// The type key is picked from the gateway's known keys or typed in, and must
// be unique: users' orders name their method by it.
import { computed, ref, watch } from 'vue'
import { SField, SForm, SFormModal, SGrid, SInput, SSelect, SSwitch, useForm, type Rules, type SelectOption } from '@sub2api/ui'
import { usePaymentHost, type PaymentMethod } from '../host'
import MethodIcon from './MethodIcon.vue'
import { GATEWAY_TYPES, GATEWAYS, isKnownType, METHOD_ICONS } from './settingsForm'

const props = defineProps<{ open: boolean; method: PaymentMethod; editing: boolean; taken: string[] }>()
const emit = defineEmits<{ (e: 'update:open', v: boolean): void; (e: 'submit', m: PaymentMethod): void }>()
const host = usePaymentHost()
const t = host.t

type Draft = PaymentMethod & Record<string, unknown>
const draft = ref<Draft>({ ...props.method })
const rules: Rules<Draft> = {
  name: { required: true, whitespace: true, max: 32 },
  gateway: { required: true, enum: GATEWAYS },
  type: [
    { required: true, max: 32, pattern: /^[a-z0-9_-]+$/, message: () => t('settings.methods.rule.typeKey') },
    { validator: (v: string) => !props.taken.includes(v) || t('settings.methods.rule.typeTaken') }
  ],
  color: { pattern: /^#[0-9a-fA-F]{6}$/, message: () => t('settings.methods.rule.color') },
  min_topup: { type: 'number', min: 0 }
}
const form = useForm<Draft>({ model: () => draft.value, update: (v) => (draft.value = v), rules })

watch(
  () => props.open,
  (open) => {
    if (!open) return
    draft.value = { ...props.method }
    form.clearValidate()
  }
)

const gatewayOptions = computed(() => GATEWAYS.map((g) => ({ value: g, label: t(`gateway.${g}`) })))

const typeLabel = (k: string) => (isKnownType(k) ? t(`settings.methods.typeName.${k}`) : k)
const iconLabel = (k: string) => (k in METHOD_ICONS ? t(`settings.methods.iconName.${k}`) : k)

/** Known keys plus the current one when typed in. */
function keyOptions(keys: string[], current: string, label: (k: string) => string): SelectOption[] {
  const all = current && !keys.includes(current) ? [...keys, current] : keys
  return all.map((k) => ({ value: k, label: label(k), hint: label(k) === k ? undefined : k }))
}
/** Keys of the other methods show as taken. */
const typeOptions = computed(() =>
  keyOptions(GATEWAY_TYPES[draft.value.gateway] ?? [], draft.value.type, typeLabel).map((o) =>
    props.taken.includes(String(o.value)) ? { ...o, disabled: true, tag: t('settings.methods.taken') } : o
  )
)
const iconOptions = computed(() => keyOptions(Object.keys(METHOD_ICONS), draft.value.icon, iconLabel))

/** A known type brings its name, icon and color along when those are still blank or the previous type's. */
function pickType(type: string) {
  const prev = draft.value.type
  form.set('type', type)
  if (!isKnownType(type)) return
  if (!draft.value.name || draft.value.name === typeLabel(prev)) form.set('name', typeLabel(type))
  if (!draft.value.icon || draft.value.icon === prev) form.set('icon', type)
  if (!draft.value.color || draft.value.color === METHOD_ICONS[prev]) form.set('color', METHOD_ICONS[type])
}

/** Another gateway drops a type key it does not know (epay keeps custom ones). */
function pickGateway(gateway: PaymentMethod['gateway']) {
  form.set('gateway', gateway)
  if (isKnownType(draft.value.type) && !GATEWAY_TYPES[gateway].includes(draft.value.type)) form.set('type', '')
  if (gateway === 'stripe' && !props.taken.includes('stripe')) pickType('stripe')
}

async function submit() {
  if (!(await form.validate())) return
  const d = draft.value
  emit('submit', { ...d, name: d.name.trim(), min_topup: d.min_topup || 0 })
}
</script>

<template>
  <SFormModal
    :open="open"
    :title="editing ? t('settings.methods.editTitle') : t('settings.methods.addTitle')"
    :submit-text="editing ? t('action.save') : t('action.add')"
    width="lg"
    data-testid="payment-method-dialog"
    @update:open="emit('update:open', $event)"
    @submit="submit"
  >
    <SForm :form="form">
      <div class="payment-dialog">
        <div class="payment-preview">
          <MethodIcon :icon="draft.icon" :color="draft.color" :name="draft.name" size="md" />
          <div class="payment-preview-text">
            <strong>{{ draft.name || t('settings.methods.unnamed') }}</strong>
            <span class="payment-muted">{{ t('settings.methods.previewHint') }}</span>
          </div>
        </div>
        <SGrid :cols="2" :gap="4">
          <SField prop="gateway" :label="t('settings.methods.gateway')">
            <SSelect :model-value="draft.gateway" :options="gatewayOptions" data-autofocus @update:model-value="pickGateway($event as PaymentMethod['gateway'])" />
          </SField>
          <SField prop="type" :label="t('settings.methods.type')" :hint="t('settings.methods.typeHint')">
            <SSelect
              :model-value="draft.type || undefined"
              :options="typeOptions"
              searchable
              creatable
              :placeholder="t('settings.methods.typePlaceholder')"
              @update:model-value="pickType(String($event))"
              @create="pickType($event)"
            />
          </SField>
        </SGrid>
        <SField prop="name" :label="t('settings.methods.name')" :hint="t('settings.methods.nameHint')">
          <SInput v-bind="form.bind('name')" :maxlength="32" :placeholder="t('settings.methods.namePlaceholder')" />
        </SField>
        <SGrid :cols="2" :gap="4">
          <SField prop="icon" :label="t('settings.methods.icon')">
            <SSelect :model-value="draft.icon || undefined" :options="iconOptions" searchable creatable :placeholder="t('settings.methods.iconPlaceholder')" @update:model-value="form.set('icon', $event)" @create="form.set('icon', $event)" />
          </SField>
          <SField prop="color" :label="t('settings.methods.color')">
            <div class="payment-color">
              <SInput type="color" :model-value="draft.color || '#64748b'" class="payment-color-swatch" :aria-label="t('settings.methods.color')" @update:model-value="form.set('color', $event)" />
              <SInput v-bind="form.bind('color')" mono placeholder="#1677ff" />
            </div>
          </SField>
          <SField prop="min_topup" :label="t('settings.methods.minTopup')" :hint="t('settings.methods.minTopupHint')">
            <SInput type="number" v-bind="form.bindNumber('min_topup')" :min="0" :step="0.01" :suffix="t('settings.unit.usd')" />
          </SField>
          <SField :label="t('settings.methods.enabled')" :hint="t('settings.methods.enabledHint')">
            <SSwitch :model-value="draft.enabled" @update:model-value="form.set('enabled', $event)" />
          </SField>
        </SGrid>
      </div>
    </SForm>
  </SFormModal>
</template>
