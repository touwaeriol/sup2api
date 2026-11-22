<script setup lang="ts">
// The side navigation of STabbedForm (layout "side"): per tab its number,
// title, a one-line summary and the marks of TabMarks. A tablist like
// TabBar's (same ids, roving tabindex; arrows of either direction and
// Home / End move and select); below the md breakpoint it turns into a row
// that scrolls sideways. Internal; not a public component.
import { nextTick, ref } from 'vue'
import { navIndex } from '../keyboard'
import type { FormTab } from '../types'
import TabMarks from './TabMarks.vue'

const props = defineProps<{ tabs: FormTab[]; modelValue: string; idPrefix: string; ariaLabel?: string; dirty: readonly string[]; errors: readonly string[] }>()
const emit = defineEmits<{ (e: 'update:modelValue', v: string): void }>()
const nav = ref<HTMLElement>()

const tabEl = (key: string) => Array.from(nav.value?.querySelectorAll<HTMLElement>('[role="tab"]') ?? []).find((el) => el.dataset.key === key)

function onKey(e: KeyboardEvent) {
  const enabled = props.tabs.filter((t) => !t.disabled)
  const i = enabled.findIndex((t) => t.key === (document.activeElement as HTMLElement | null)?.dataset?.key)
  const next = Math.max(navIndex(e.key, i, enabled.length, 'vertical'), navIndex(e.key, i, enabled.length, 'horizontal'))
  if (next < 0) return
  e.preventDefault()
  emit('update:modelValue', enabled[next].key)
  nextTick(() => tabEl(enabled[next].key)?.focus())
}
</script>

<template>
  <nav ref="nav" role="tablist" aria-orientation="vertical" :aria-label="ariaLabel" class="flex min-w-0 gap-1.5 self-start overflow-x-auto md:sticky md:top-0 md:flex-col md:overflow-visible" @keydown="onKey">
    <button
      v-for="(tab, i) in tabs"
      :id="`${idPrefix}-tab-${tab.key}`"
      :key="tab.key"
      type="button"
      role="tab"
      :data-key="tab.key"
      :aria-selected="tab.key === modelValue"
      :aria-controls="`${idPrefix}-panel-${tab.key}`"
      :tabindex="tab.key === modelValue ? 0 : -1"
      :disabled="tab.disabled"
      class="flex min-w-[11rem] shrink-0 items-start gap-2.5 rounded-xl px-3 py-2.5 text-left transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500/40 disabled:cursor-not-allowed disabled:opacity-40 md:min-w-0"
      :class="tab.key === modelValue ? 'bg-primary-50 ring-1 ring-primary-200 dark:bg-primary-900/20 dark:ring-primary-800' : 'hover:bg-surface-2'"
      @click="emit('update:modelValue', tab.key)"
    >
      <span class="mt-0.5 text-xs tabular-nums text-fg-subtle">{{ i + 1 }}</span>
      <span class="min-w-0 flex-1">
        <span class="flex items-center gap-1.5 text-sm font-medium" :class="tab.key === modelValue ? 'text-primary-700 dark:text-primary-300' : 'text-fg'">
          <span class="truncate">{{ tab.label }}</span>
          <TabMarks :dirty="dirty.includes(tab.key)" :error="errors.includes(tab.key)" />
        </span>
        <span v-if="tab.summary" class="mt-0.5 block truncate text-xs text-fg-muted" :title="tab.summary">{{ tab.summary }}</span>
      </span>
    </button>
  </nav>
</template>
