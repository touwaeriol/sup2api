<script setup lang="ts">
// Styled native <textarea class="input">; height follows `rows` (min 80px,
// 64px when size="sm"). Remaining attrs fall through. Inside SField /
// SSettingRow it takes the frame's id, aria-describedby and aria-invalid.
import { useAttrs } from 'vue'
import { useFieldControl, type ControlSize } from './field'

defineOptions({ inheritAttrs: false })
const props = withDefaults(
  defineProps<{
    modelValue: string | null | undefined
    rows?: number
    placeholder?: string
    disabled?: boolean
    readonly?: boolean
    mono?: boolean
    error?: boolean
    maxlength?: number
    size?: ControlSize
    ariaLabel?: string
  }>(),
  { rows: 4, size: undefined }
)
const emit = defineEmits<{ (e: 'update:modelValue', v: string): void }>()
const attrs = useAttrs()
const { id, a11y, invalid, size, blur } = useFieldControl(props)
</script>

<template>
  <textarea
    class="input"
    :class="[mono ? 'font-mono' : '', invalid ? 'input-error' : '', size === 'sm' ? 'input-sm' : '']"
    :value="modelValue ?? ''"
    :rows="rows"
    :placeholder="placeholder"
    :disabled="disabled"
    :readonly="readonly"
    :maxlength="maxlength"
    :id="id"
    v-bind="{ ...attrs, ...a11y }"
    @input="emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)"
    @blur="blur"
  />
</template>
