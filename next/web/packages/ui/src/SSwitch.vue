<script setup lang="ts">
// Toggle switch: a native <input type="checkbox" role="switch" class="switch">
// (aria-checked mirrors `modelValue`), optionally followed by a text label.
// inheritAttrs: false — class / style go to the outer element; id,
// data-testid, aria-*, title, name and every other attr go to the <input>.
// Click and Space toggle. `describedby` is aria-describedby of the input.
// Inside SField / SSettingRow it takes the frame's id (so the row label is
// its <label for>) and aria-describedby. The checked state is written back
// to the DOM when the parent keeps the old value (controlled). On touch
// screens the switch is 24px high and a labelled one a 44px-high target.
import { computed, ref, useAttrs } from 'vue'
import { splitAttrs, useFieldControl, type ControlSize } from './field'
import { useCheckboxModel } from './controlled'

defineOptions({ inheritAttrs: false })
const props = withDefaults(
  defineProps<{ modelValue: boolean; disabled?: boolean; label?: string; describedby?: string; ariaLabel?: string; size?: ControlSize; error?: boolean }>(),
  { size: undefined }
)
const emit = defineEmits<{ (e: 'update:modelValue', v: boolean): void }>()
const attrs = useAttrs()
const { id, a11y, size, blur } = useFieldControl(props, true, true)
const input = ref<HTMLInputElement>()
const parts = computed(() => splitAttrs(attrs))
const describedBy = computed(() => [props.describedby, a11y.value['aria-describedby']].filter(Boolean).join(' ') || undefined)
const { onClick, onChange } = useCheckboxModel(props, emit, input)
</script>

<template>
  <label
    class="inline-flex items-center gap-2 align-middle"
    :class="[disabled ? 'cursor-not-allowed' : 'cursor-pointer', label || $slots.default ? 'touch:min-h-11' : '']"
    v-bind="parts.wrap"
  >
    <input
      :id="id"
      ref="input"
      type="checkbox"
      role="switch"
      class="switch"
      :class="size === 'sm' ? 'switch-sm' : ''"
      :checked="modelValue"
      :aria-checked="modelValue ? 'true' : 'false'"
      :disabled="disabled"
      v-bind="{ ...a11y, ...parts.control }"
      :aria-describedby="describedBy"
      @click="onClick"
      @change="onChange"
      @blur="blur"
    />
    <span v-if="label || $slots.default" class="text-sm text-gray-700 dark:text-gray-300" :class="disabled ? 'opacity-50' : ''"><slot>{{ label }}</slot></span>
  </label>
</template>
