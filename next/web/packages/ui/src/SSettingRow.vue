<script setup lang="ts">
// One setting: label and short description on the left, its control on the
// right (below it on narrow screens, or always with `stacked` for wide
// controls). The `details` slot is a longer explanation, collapsed behind a
// toggle. Like SField, the first @sub2api/ui control inside without an `id`
// takes the label's id (`for` names a control id explicitly), and gets
// aria-describedby (description / error) and aria-invalid. `error` shows a
// message under the control. Remaining attrs (data-testid, ...) go to the
// root. `dense` is for rows of a label and a small control only (unset, it
// follows a `dense` SSettingsGroup); `flush` drops the vertical padding (a
// single row inside a form or modal). On narrow screens a row whose only
// control is a switch / checkbox keeps it beside the label; other controls
// take the full width. Inside an SForm, `prop` names the setting's path as
// SField's does (error, star and blur validation from the form).
import { computed, useId, useSlots } from 'vue'
import SDisclosure from './SDisclosure.vue'
import { provideField, type ControlSize } from './field'
import { useFormItem } from './form/context'
import { useSettingsDensity } from './settings'

const props = withDefaults(
  defineProps<{
    label?: string
    description?: string
    stacked?: boolean
    dense?: boolean
    flush?: boolean
    for?: string
    error?: string
    required?: boolean
    size?: ControlSize
    prop?: string
  }>(),
  { dense: undefined, required: undefined }
)
const slots = useSlots()
const uid = useId()
const groupDense = useSettingsDensity()
const isDense = computed(() => props.dense ?? groupDense())
const hasDescription = computed(() => !!props.description || !!slots.description)
const { error: shownError, required: shownRequired, touch } = useFormItem(props)
const { labelFor, inlineOnly } = provideField(uid, {
  for: () => props.for,
  labelId: computed(() => `${uid}-label`),
  describedBy: computed(() => [shownError.value ? `${uid}-error` : '', hasDescription.value ? `${uid}-desc` : ''].filter(Boolean).join(' ') || undefined),
  invalid: computed(() => !!shownError.value),
  size: computed(() => props.size),
  touch
})
</script>

<template>
  <div
    class="grid gap-3"
    :class="[
      flush ? '' : isDense ? 'py-2.5' : 'py-4',
      stacked ? '' : 'items-center sm:grid-cols-[minmax(0,1fr)_auto] sm:gap-8',
      !stacked && inlineOnly ? 'max-sm:grid-cols-[minmax(0,1fr)_auto]' : ''
    ]"
  >
    <div class="min-w-0">
      <component :is="labelFor ? 'label' : 'p'" :id="`${uid}-label`" :for="labelFor" class="text-sm font-medium text-fg">
        <slot name="label">{{ label }}</slot><span v-if="shownRequired" class="ml-0.5 text-red-500" aria-hidden="true">*</span>
      </component>
      <p v-if="hasDescription" :id="`${uid}-desc`" class="mt-1 max-w-3xl text-xs leading-5 text-fg-muted">
        <slot name="description">{{ description }}</slot>
      </p>
      <SDisclosure v-if="$slots.details" class="mt-1.5"><slot name="details" /></SDisclosure>
    </div>
    <div class="min-w-0" :class="stacked ? '' : 'sm:justify-self-end'">
      <div class="flex min-w-0 flex-wrap items-center gap-2 max-sm:[&>*]:grow" :class="stacked ? '' : 'sm:justify-end'"><slot /></div>
      <p v-if="shownError" :id="`${uid}-error`" class="input-error-text" :class="stacked ? '' : 'sm:text-right'">{{ shownError }}</p>
    </div>
  </div>
</template>
