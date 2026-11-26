<script setup lang="ts">
// Add / edit one payment method (a draft; the page saves the whole list).
// The type key is picked from the gateway's known keys or typed in, and must
// be unique: users' orders name their method by it. Its mark is a built-in
// icon on a color or a custom image; only the chosen one is saved.
import { computed, ref, watch } from 'vue'
import { SField, SForm, SFormModal, SGrid, SInput, SSegmented, SSelect, SSwitch, useForm, type Rules, type SelectOption } from '@sub2api/ui'
import { useTopup, type PaymentMethod } from '../topup'
import MethodIcon from '../components/MethodIcon.vue'
import { BUILTIN_ICONS, brandColor, builtinIcon } from '../components/methodIcon'
import LogoUpload from './LogoUpload.vue'
import { GATEWAY_TYPES, GATEWAYS, isKnownType, logoValidator } from './settingsForm'

const props = defineProps<{ open: boolean; method: PaymentMethod; editing: boolean; taken: string[] }>()
const emit = defineEmits<{ (e: 'update:open', v: boolean): void; (e: 'submit', m: PaymentMethod): void }>()
const host = useTopup()
const t = host.t

type Draft = PaymentMethod & Record<string, unknown>
type IconMode = 'builtin' | 'custom'
const draft = ref<Draft>({ ...props.method })
const mode = ref<IconMode>(props.method.logo ? 'custom' : 'builtin')
const custom = () => mode.value === 'custom'
const rules: Rules<Draft> = {
  name: { required: true, whitespace: true, max: 32 },
  gateway: { required: true, enum: GATEWAYS },
  type: [
    { required: true, max: 32, pattern: /^[a-z0-9_-]+$/, message: () => t('topup.settings.methods.rule.typeKey') },
    { validator: (v: string) => !props.taken.includes(v) || t('topup.settings.methods.rule.typeTaken') }
  ],
  color: { pattern: /^#[0-9a-fA-F]{6}$/, message: () => t('topup.settings.methods.rule.color') },
  logo: [
    { required: true, when: custom, message: () => t('topup.settings.methods.logo.rule.required') },
    { validator: logoValidator(t), when: custom }
  ],
  min_topup: { type: 'number', min: 0 }
}
const form = useForm<Draft>({ model: () => draft.value, update: (v) => (draft.value = v), rules })

watch(
  () => props.open,
  (open) => {
    if (!open) return
    draft.value = { ...props.method }
    mode.value = props.method.logo ? 'custom' : 'builtin'
    form.clearValidate()
  }
)
// A message of the hidden mode would come back stale.
watch(mode, () => form.clearValidate('logo'))

const gatewayOptions = computed(() => GATEWAYS.map((g) => ({ value: g, label: t(`topup.gateway.${g}`) })))
const modeOptions = computed(() => (['builtin', 'custom'] as const).map((m) => ({ value: m, label: t(`topup.settings.methods.iconMode.${m}`) })))
/** The method as users will see it: the image only in custom mode. */
const look = computed(() => (custom() ? draft.value : { ...draft.value, logo: undefined }))

const typeLabel = (k: string) => (isKnownType(k) ? t(`topup.settings.methods.typeName.${k}`) : k)
const iconLabel = (k: string) => (builtinIcon(k) ? t(`topup.settings.methods.iconName.${k}`) : k)

/** Known keys plus the current one when typed in. */
function keyOptions(keys: string[], current: string, label: (k: string) => string): SelectOption[] {
  const all = current && !keys.includes(current) ? [...keys, current] : keys
  return all.map((k) => ({ value: k, label: label(k), hint: label(k) === k ? undefined : k }))
}
/** Keys of the other methods show as taken. */
const typeOptions = computed(() =>
  keyOptions(GATEWAY_TYPES[draft.value.gateway] ?? [], draft.value.type, typeLabel).map((o) =>
    props.taken.includes(String(o.value)) ? { ...o, disabled: true, tag: t('topup.settings.methods.taken') } : o
  )
)
const iconOptions = computed(() => keyOptions(Object.keys(BUILTIN_ICONS), draft.value.icon, iconLabel))

/** A known type brings its name, icon and color along when those are still blank or the previous type's. */
function pickType(type: string) {
  const prev = draft.value.type
  form.set('type', type)
  if (!isKnownType(type)) return
  if (!draft.value.name || draft.value.name === typeLabel(prev)) form.set('name', typeLabel(type))
  if (!draft.value.icon || draft.value.icon === prev) form.set('icon', type)
  if (!draft.value.color || draft.value.color === brandColor(prev)) form.set('color', brandColor(type))
}

/** Another gateway drops a type key it does not know (epay keeps custom ones). */
function pickGateway(gateway: PaymentMethod['gateway']) {
  form.set('gateway', gateway)
  if (isKnownType(draft.value.type) && !GATEWAY_TYPES[gateway].includes(draft.value.type)) form.set('type', '')
  if (gateway === 'stripe' && !props.taken.includes('stripe')) pickType('stripe')
}

async function submit() {
  if (!(await form.validate())) return
  const { logo, ...d } = draft.value
  const method: PaymentMethod = { ...d, name: d.name.trim(), min_topup: d.min_topup || 0 }
  emit('submit', custom() ? { ...method, logo } : method)
}
</script>

<template>
  <SFormModal
    :open="open"
    :title="editing ? t('topup.settings.methods.editTitle') : t('topup.settings.methods.addTitle')"
    :submit-text="editing ? t('topup.action.save') : t('topup.action.add')"
    width="lg"
    data-testid="payment-method-dialog"
    @update:open="emit('update:open', $event)"
    @submit="submit"
  >
    <SForm :form="form">
      <div class="payment-dialog">
        <div class="payment-preview">
          <MethodIcon :method="look" size="lg" />
          <div class="payment-preview-text">
            <strong>{{ draft.name || t('topup.settings.methods.unnamed') }}</strong>
            <span class="payment-muted">{{ t('topup.settings.methods.previewHint') }}</span>
          </div>
        </div>
        <SGrid :cols="2" :gap="4">
          <SField prop="gateway" :label="t('topup.settings.methods.gateway')">
            <SSelect :model-value="draft.gateway" :options="gatewayOptions" data-autofocus @update:model-value="pickGateway($event as PaymentMethod['gateway'])" />
          </SField>
          <SField prop="type" :label="t('topup.settings.methods.type')" :hint="t('topup.settings.methods.typeHint')">
            <SSelect
              :model-value="draft.type || undefined"
              :options="typeOptions"
              searchable
              creatable
              :placeholder="t('topup.settings.methods.typePlaceholder')"
              @update:model-value="pickType(String($event))"
              @create="pickType($event)"
            />
          </SField>
        </SGrid>
        <SField prop="name" :label="t('topup.settings.methods.name')" :hint="t('topup.settings.methods.nameHint')">
          <SInput v-bind="form.bind('name')" :maxlength="32" :placeholder="t('topup.settings.methods.namePlaceholder')" />
        </SField>
        <SField :label="t('topup.settings.methods.icon')">
          <SSegmented v-model="mode" :options="modeOptions" size="sm" data-testid="payment-icon-mode" />
        </SField>
        <LogoUpload v-if="mode === 'custom'" :model-value="draft.logo" :name="draft.name" @update:model-value="form.set('logo', $event)" />
        <SGrid v-else :cols="2" :gap="4">
          <SField prop="icon" :label="t('topup.settings.methods.iconKey')">
            <SSelect :model-value="draft.icon || undefined" :options="iconOptions" searchable creatable :placeholder="t('topup.settings.methods.iconPlaceholder')" @update:model-value="form.set('icon', $event)" @create="form.set('icon', $event)" />
          </SField>
          <SField prop="color" :label="t('topup.settings.methods.color')">
            <div class="payment-color">
              <SInput type="color" :model-value="draft.color || '#64748b'" class="payment-color-swatch" :aria-label="t('topup.settings.methods.color')" @update:model-value="form.set('color', $event)" />
              <SInput v-bind="form.bind('color')" mono placeholder="#1677ff" />
            </div>
          </SField>
        </SGrid>
        <SGrid :cols="2" :gap="4">
          <SField prop="min_topup" :label="t('topup.settings.methods.minTopup')" :hint="t('topup.settings.methods.minTopupHint')">
            <SInput type="number" v-bind="form.bindNumber('min_topup')" :min="0" :step="0.01" :suffix="t('topup.settings.unit.usd')" />
          </SField>
          <SField :label="t('topup.settings.methods.enabled')" :hint="t('topup.settings.methods.enabledHint')">
            <SSwitch :model-value="draft.enabled" @update:model-value="form.set('enabled', $event)" />
          </SField>
        </SGrid>
      </div>
    </SForm>
  </SFormModal>
</template>
