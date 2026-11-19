<script setup lang="ts">
// Form item frame: label / hint / error / required star. `hint` and `error`
// can also come from the `#hint` / `#error` slots (error wins over hint); the
// `#details` slot is a longer explanation behind a toggle. The first
// @sub2api/ui control inside without its own `id` takes the label's id
// (`for`), and gets aria-describedby (hint / error) and aria-invalid; `for`
// names a control id explicitly instead. `size="sm"` is the compact variant
// (small label, and small controls unless they set `size`). Remaining attrs
// (data-testid, ...) fall through to the root div.
// Inside an SForm, `prop` names the field's path: the error and the star
// come from the form's rules and errors (an explicit `error` / `required`
// wins), and the control's blur validates the field.
import { computed, useId, useSlots } from 'vue'
import SDisclosure from './SDisclosure.vue'
import { provideField, type ControlSize } from './field'
import { useFormItem } from './form/context'

const props = withDefaults(
  defineProps<{
    label?: string
    hint?: string
    error?: string
    required?: boolean
    inline?: boolean
    for?: string
    size?: ControlSize
    prop?: string
  }>(),
  { required: undefined }
)
const slots = useSlots()
const uid = useId()
const { error: shownError, required: shownRequired, touch } = useFormItem(props)
const hasError = computed(() => !!shownError.value || !!slots.error)
const hasHint = computed(() => !hasError.value && (!!props.hint || !!slots.hint))
const { labelFor } = provideField(uid, {
  for: () => props.for,
  labelId: computed(() => (props.label || slots.label ? `${uid}-label` : '')),
  describedBy: computed(() => (hasError.value ? `${uid}-error` : hasHint.value ? `${uid}-hint` : undefined)),
  invalid: hasError,
  size: computed(() => props.size),
  touch
})
</script>

<template>
  <div :class="inline ? 'flex items-center gap-3' : ''">
    <label
      v-if="label || $slots.label"
      :id="`${uid}-label`"
      :for="labelFor"
      class="input-label"
      :class="[inline ? '!mb-0 shrink-0' : '', size === 'sm' ? '!mb-1 !text-xs' : '']"
    >
      <slot name="label">{{ label }}</slot><span v-if="shownRequired" class="ml-0.5 text-red-500" aria-hidden="true">*</span>
    </label>
    <div :class="inline ? 'min-w-0 flex-1' : ''">
      <slot />
      <p v-if="hasError" :id="`${uid}-error`" class="input-error-text"><slot name="error">{{ shownError }}</slot></p>
      <p v-else-if="hasHint" :id="`${uid}-hint`" class="input-hint"><slot name="hint">{{ hint }}</slot></p>
      <SDisclosure v-if="$slots.details" class="mt-1"><slot name="details" /></SDisclosure>
    </div>
  </div>
</template>
