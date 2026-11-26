<script setup lang="ts">
// Add / edit one payment method (a draft; the page saves the whole list).
// It is served by a built-in gateway or a plugin's; the type key is picked
// from the gateway's known keys (a plugin's templates) or typed in, and must
// be unique: users' orders name their method by it. Its mark is a built-in
// icon on a color or a custom image; only the chosen one is saved.
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { SField, SForm, SFormModal, SGrid, SInput, SSegmented, SSelect, SSwitch, type Rules, type SelectOption } from '@sub2api/ui'
import { useModalForm } from '@/composables/useModalForm'
import MethodIcon from '../components/MethodIcon.vue'
import { BUILTIN_ICONS, brandColor, builtinIcon } from '../components/methodIcon'
import LogoUpload from './LogoUpload.vue'
import type { GatewayCatalog } from './gatewayCatalog'
import { logoValidator, METHOD_NAME_MAX, METHOD_TYPE_PATTERN, type MethodModel } from './settingsForm'

const props = defineProps<{ open: boolean; method: MethodModel; editing: boolean; taken: string[]; gateways: GatewayCatalog }>()
const emit = defineEmits<{ 'update:open': [v: boolean]; submit: [m: MethodModel] }>()
const { t } = useI18n()

type Draft = MethodModel & Record<string, unknown>
type IconMode = 'builtin' | 'custom'
const mode = ref<IconMode>(props.method.logo ? 'custom' : 'builtin')
const custom = () => mode.value === 'custom'
const rules: Rules<Draft> = {
  name: { required: true, whitespace: true, max: METHOD_NAME_MAX },
  gateway: { required: true },
  type: [
    { required: true, pattern: METHOD_TYPE_PATTERN, message: () => t('topup.settings.methods.rule.typeKey') },
    { validator: (v: string) => !props.taken.includes(v) || t('topup.settings.methods.rule.typeTaken') }
  ],
  color: { pattern: /^#[0-9a-fA-F]{6}$/, message: () => t('topup.settings.methods.rule.color') },
  logo: [
    { required: true, when: custom, message: () => t('topup.settings.methods.logo.rule.required') },
    { validator: logoValidator(t), when: custom }
  ],
  min_topup: { type: 'number', min: 0 }
}
const { model: draft, form, reset, submit: check } = useModalForm<Draft>(() => ({ ...props.method }), rules)

watch(
  () => props.open,
  open => {
    if (!open) return
    reset()
    mode.value = props.method.logo ? 'custom' : 'builtin'
  }
)
// A message of the hidden mode would come back stale.
watch(mode, () => form.clearValidate('logo'))

const modeOptions = computed(() =>
  (['builtin', 'custom'] as const).map(m => ({ value: m, label: t(`topup.settings.methods.iconMode.${m}`) }))
)
/** The method as users will see it: the image only in custom mode. */
const look = computed(() => (custom() ? draft.value : { ...draft.value, logo: undefined }))

const typeLabel = (k: string) => props.gateways.typeName(draft.value.gateway, k)
const iconLabel = (k: string) => (builtinIcon(k) ? t(`topup.settings.methods.iconName.${k}`) : k)

/** Known keys plus the current one when typed in. */
function keyOptions(keys: string[], current: string, label: (k: string) => string): SelectOption[] {
  const all = current && !keys.includes(current) ? [...keys, current] : keys
  return all.map(k => ({ value: k, label: label(k), hint: label(k) === k ? undefined : k }))
}
/** Every gateway, plus the method's own should it no longer be listed. */
const gatewayOptions = computed(() =>
  keyOptions(
    props.gateways.options.value.map(o => String(o.value)),
    draft.value.gateway,
    props.gateways.label
  )
)
/** Keys of the other methods show as taken. */
const typeOptions = computed(() =>
  keyOptions(props.gateways.types(draft.value.gateway), draft.value.type, typeLabel).map(o =>
    props.taken.includes(String(o.value)) ? { ...o, disabled: true, tag: t('topup.settings.methods.taken') } : o
  )
)
const iconOptions = computed(() => keyOptions(Object.keys(BUILTIN_ICONS), draft.value.icon, iconLabel))

/** A known type brings its name, icon and color along when those are still blank or the previous type's. */
function pickType(type: string) {
  const prev = draft.value.type
  form.set('type', type)
  if (!props.gateways.types(draft.value.gateway).includes(type)) return
  if (!draft.value.name || draft.value.name === typeLabel(prev)) form.set('name', typeLabel(type))
  if (!draft.value.icon || draft.value.icon === prev) form.set('icon', type)
  if (!draft.value.color || draft.value.color === brandColor(prev)) form.set('color', brandColor(type))
}

/** Another gateway drops a known type key it does not know (typed ones stay); one with a single known key picks it. */
function pickGateway(gateway: string) {
  const knew = props.gateways.types(draft.value.gateway).includes(draft.value.type)
  form.set('gateway', gateway)
  const types = props.gateways.types(gateway)
  if (knew && !types.includes(draft.value.type)) form.set('type', '')
  if (types.length === 1 && !props.taken.includes(types[0])) pickType(types[0])
}

// The page saves the whole list: a valid draft goes back to it.
const submit = () =>
  check(async ({ logo, ...d }) => {
    const method: MethodModel = { ...d, name: d.name.trim(), min_topup: d.min_topup || 0 }
    emit('submit', custom() ? { ...method, logo } : method)
  })
</script>

<template>
  <SFormModal
    :open="open"
    :title="editing ? t('topup.settings.methods.editTitle') : t('topup.settings.methods.addTitle')"
    :submit-text="editing ? t('common.save') : t('common.add')"
    width="lg"
    data-testid="payment-method-dialog"
    @update:open="emit('update:open', $event)"
    @submit="submit"
  >
    <SForm :form="form">
      <div class="flex flex-col gap-4">
        <div class="flex items-center gap-3 rounded-xl border border-dashed border-line-strong bg-surface-2 p-3">
          <MethodIcon :method="look" size="lg" />
          <div class="flex min-w-0 flex-col gap-0.5 text-fg">
            <strong>{{ draft.name || t('topup.settings.methods.unnamed') }}</strong>
            <span class="text-[0.8125rem] text-fg-muted">{{ t('topup.settings.methods.previewHint') }}</span>
          </div>
        </div>
        <SGrid :cols="2" :gap="4">
          <SField prop="gateway" :label="t('topup.settings.methods.gateway')">
            <SSelect
              :model-value="draft.gateway"
              :options="gatewayOptions"
              data-autofocus
              @update:model-value="pickGateway(String($event))"
            />
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
          <SInput v-bind="form.bind('name')" :maxlength="METHOD_NAME_MAX" :placeholder="t('topup.settings.methods.namePlaceholder')" />
        </SField>
        <SField :label="t('topup.settings.methods.icon')">
          <SSegmented v-model="mode" :options="modeOptions" size="sm" data-testid="payment-icon-mode" />
        </SField>
        <LogoUpload v-if="mode === 'custom'" :model-value="draft.logo" :name="draft.name" @update:model-value="form.set('logo', $event)" />
        <SGrid v-else :cols="2" :gap="4">
          <SField prop="icon" :label="t('topup.settings.methods.iconKey')">
            <SSelect
              :model-value="draft.icon || undefined"
              :options="iconOptions"
              searchable
              creatable
              :placeholder="t('topup.settings.methods.iconPlaceholder')"
              @update:model-value="form.set('icon', $event)"
              @create="form.set('icon', $event)"
            />
          </SField>
          <SField prop="color" :label="t('topup.settings.methods.color')">
            <div class="flex gap-2">
              <SInput
                type="color"
                :model-value="draft.color || '#64748b'"
                class="w-12 shrink-0"
                :aria-label="t('topup.settings.methods.color')"
                @update:model-value="form.set('color', $event)"
              />
              <SInput v-bind="form.bind('color')" mono placeholder="#1677ff" class="min-w-0 flex-1" />
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
