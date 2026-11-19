<script setup lang="ts">
// List of short strings as removable chips plus a text box: Enter, comma or
// blur adds the typed text; pasting splits on commas and new lines; values
// are trimmed and de-duplicated; Backspace in an empty box removes the last
// chip. The frame looks like .input (focus ring on focus-within) and grows
// with its chips. Inside SField / SSettingRow the text box takes the
// frame's id, aria-describedby and aria-invalid.
import { ref } from 'vue'
import { useUiT } from './i18n'
import SIcon from './SIcon.vue'
import { useFieldControl, type ControlSize } from './field'

const props = withDefaults(
  defineProps<{ modelValue: string[] | null | undefined; placeholder?: string; disabled?: boolean; size?: ControlSize; error?: boolean; ariaLabel?: string; max?: number }>(),
  { size: undefined }
)
const emit = defineEmits<{ (e: 'update:modelValue', v: string[]): void }>()
const t = useUiT()
const { id, a11y, invalid, size, blur } = useFieldControl(props)
const draft = ref('')
const box = ref<HTMLInputElement>()

function addParts(text: string) {
  const list = [...(props.modelValue || [])]
  for (const part of text.split(/[,\n\r]/).map((s) => s.trim()).filter(Boolean)) {
    if (props.max !== undefined && list.length >= props.max) break
    if (!list.includes(part)) list.push(part)
  }
  if (list.length !== (props.modelValue || []).length) emit('update:modelValue', list)
}

function add() {
  const v = draft.value.trim()
  draft.value = ''
  if (v) addParts(v)
}

function onPaste(e: ClipboardEvent) {
  const text = e.clipboardData?.getData('text') ?? ''
  if (!/[,\n\r]/.test(text)) return
  e.preventDefault()
  addParts(draft.value + text)
  draft.value = ''
}

function remove(i: number) {
  if (props.disabled) return
  const list = [...(props.modelValue || [])]
  list.splice(i, 1)
  emit('update:modelValue', list)
}

function onKey(e: KeyboardEvent) {
  if (e.isComposing) return
  if (e.key === 'Enter' || e.key === ',') {
    e.preventDefault()
    add()
  } else if (e.key === 'Backspace' && !draft.value && props.modelValue?.length) {
    remove(props.modelValue.length - 1)
  }
}
</script>

<template>
  <div
    class="input input-group !h-auto flex-wrap !gap-1.5"
    :class="[
      size === 'sm' ? 'min-h-control-sm !px-2 !py-1' : 'min-h-control !py-1.5',
      invalid ? 'input-error' : '',
      disabled ? 'input-group-disabled opacity-60' : 'cursor-text'
    ]"
    @click="box?.focus()"
  >
    <span
      v-for="(tag, i) in modelValue || []"
      :key="tag"
      class="inline-flex max-w-full items-center gap-1 rounded-chip bg-primary-50 px-2 py-0.5 text-xs text-primary-700 dark:bg-primary-900/30 dark:text-primary-300"
    >
      <span class="truncate">{{ tag }}</span>
      <button
        type="button"
        class="shrink-0 rounded opacity-60 hover:opacity-100 focus:outline-none focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-primary-500/40 disabled:cursor-not-allowed"
        :disabled="disabled"
        :aria-label="t('ui.removeItem', { name: tag })"
        @click.stop="remove(i)"
      >
        <SIcon name="x" class="h-3 w-3" />
      </button>
    </span>
    <input
      :id="id"
      ref="box"
      v-model="draft"
      class="!h-7 min-w-[8rem] !flex-1 !p-0.5 text-sm"
      :placeholder="placeholder || t('ui.addTag')"
      :disabled="disabled"
      v-bind="a11y"
      @keydown="onKey"
      @paste="onPaste"
      @blur="add(), blur()"
    />
  </div>
</template>
