<script setup lang="ts">
import { computed, ref } from 'vue'
import { highlight, offsetOf } from './exprSyntax'

// A code box for expressions: line numbers, syntax colours and the error
// position underlined. A transparent textarea sits on a highlighted <pre>
// of the same metrics; both scroll together and lines never wrap.
const model = defineModel<string>({ required: true })
const props = withDefaults(defineProps<{ rows?: number; readonly?: boolean; errorLine?: number; errorColumn?: number; label?: string }>(), {
  rows: 10
})

const ta = ref<HTMLTextAreaElement>()
const layer = ref<HTMLElement>()
const gutter = ref<HTMLElement>()

const lineCount = computed(() => Math.max(model.value.split('\n').length, props.rows))
const errorAt = computed(() => (props.errorLine ? offsetOf(model.value, props.errorLine, props.errorColumn || 1) : -1))
const html = computed(() => highlight(model.value, errorAt.value))

function sync() {
  const t = ta.value
  if (!t) return
  if (layer.value) {
    layer.value.scrollTop = t.scrollTop
    layer.value.scrollLeft = t.scrollLeft
  }
  if (gutter.value) gutter.value.scrollTop = t.scrollTop
}

/** Puts the caret at a 1-based line and column. */
function focusAt(line: number, column = 1) {
  const t = ta.value
  if (!t) return
  const at = offsetOf(model.value, line, column)
  t.focus()
  t.setSelectionRange(at, at + 1)
}

/** Inserts text at the caret (or replaces the selection); `caret` is the offset inside `text` to leave the caret at. Read-only, nothing. */
function insert(text: string, caret = text.length) {
  if (props.readonly) return
  const t = ta.value
  const v = model.value
  const s = t ? t.selectionStart : v.length
  const e = t ? t.selectionEnd : v.length
  model.value = v.slice(0, s) + text + v.slice(e)
  requestAnimationFrame(() => {
    if (!t) return
    t.focus()
    t.setSelectionRange(s + caret, s + caret)
  })
}

defineExpose({ focusAt, insert })
</script>

<template>
  <div
    class="flex overflow-hidden rounded-lg border border-line bg-surface font-mono text-xs leading-5 focus-within:border-primary-500 focus-within:ring-2 focus-within:ring-primary-500/20"
    data-testid="expr-code"
  >
    <div ref="gutter" aria-hidden="true" class="shrink-0 select-none overflow-hidden border-r border-line bg-surface-2 py-2 text-right text-fg-subtle">
      <div
        v-for="n in lineCount"
        :key="n"
        class="px-2.5"
        :class="n === errorLine ? 'bg-danger-500/15 font-semibold text-danger-600 dark:text-danger-400' : ''"
      >
        {{ n }}
      </div>
    </div>
    <div class="relative min-w-0 flex-1">
      <pre
        ref="layer"
        aria-hidden="true"
        class="pointer-events-none absolute inset-0 m-0 overflow-hidden whitespace-pre px-3 py-2 text-fg"
        v-html="html"
      ></pre>
      <textarea
        ref="ta"
        v-model="model"
        :rows="rows"
        :readonly="readonly"
        :aria-label="label"
        wrap="off"
        spellcheck="false"
        autocapitalize="off"
        autocomplete="off"
        class="relative block w-full resize-y whitespace-pre bg-transparent px-3 py-2 text-transparent caret-fg outline-none selection:bg-primary-500/25"
        :style="{ minHeight: `${rows * 1.25 + 1}rem` }"
        @scroll="sync"
        @input="sync"
      ></textarea>
    </div>
  </div>
</template>
