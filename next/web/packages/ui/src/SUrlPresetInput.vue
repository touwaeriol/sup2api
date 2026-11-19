<script setup lang="ts">
// Base URL with suggestions: a URL box with a datalist and a "Presets"
// menu. With `allowed` (the caller may only use the type's official values,
// CONTRACTS §21.3; useGuardedSettings) it is a plain choice; a stored value
// outside the list (set by an administrator) stays selectable so saving
// other fields keeps it; a single allowed value is read-only ("Set by an
// administrator"). An empty value emits undefined: the key is not sent.
import { computed, useId } from 'vue'
import SDropdown from './SDropdown.vue'
import SHint from './SHint.vue'
import SInput from './SInput.vue'
import SSelect from './SSelect.vue'
import { useUiT } from './i18n'

const props = withDefaults(
  defineProps<{ modelValue?: string | null; presets?: string[]; allowed?: string[] | null; placeholder?: string; disabled?: boolean; readonly?: boolean }>(),
  { modelValue: undefined, presets: () => [], allowed: null, placeholder: undefined }
)
const emit = defineEmits<{ (e: 'update:modelValue', v: string | undefined): void }>()
const t = useUiT()
const listId = useId()
const current = computed(() => props.modelValue ?? '')
const choices = computed(() => {
  const list = props.allowed
  if (!list) return null
  return (current.value && !list.includes(current.value) ? [current.value, ...list] : list).map((v) => ({ value: v, label: v }))
})
const known = computed(() => !!choices.value?.some((c) => c.value === current.value))
const locked = computed(() => props.allowed?.length === 1)
const menu = computed(() => props.presets.map((p) => ({ key: p, label: p })))

const set = (v: unknown) => emit('update:modelValue', typeof v === 'string' && v !== '' ? v : undefined)
</script>

<template>
  <div class="s-url-preset">
    <template v-if="choices">
      <SSelect
        :model-value="known ? current : null"
        :options="choices"
        :placeholder="known ? undefined : placeholder || '—'"
        :disabled="disabled || readonly || locked"
        @update:model-value="set"
      />
      <SHint v-if="locked" size="xs">{{ t('ui.form.setByAdmin') }}</SHint>
    </template>
    <div v-else class="s-url-preset-row">
      <SInput
        type="url"
        class="s-url-preset-input"
        :model-value="current"
        :placeholder="placeholder || presets[0]"
        :list="listId"
        :disabled="disabled"
        :readonly="readonly"
        @update:model-value="set"
      />
      <datalist :id="listId">
        <option v-for="p in presets" :key="p" :value="p" />
      </datalist>
      <SDropdown v-if="presets.length" :actions="menu" :label="t('ui.form.presets')" :disabled="disabled || readonly" width="20rem" @select="set" />
    </div>
  </div>
</template>

<style scoped>
.s-url-preset {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 0.25rem;
}
.s-url-preset-row {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 0.5rem;
}
.s-url-preset-input {
  min-width: 0;
  flex: 1 1 auto;
}
</style>
