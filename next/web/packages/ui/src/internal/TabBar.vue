<script setup lang="ts">
// Tab bar (role="tablist"). Arrow Left / Right, Home and End move between
// enabled tabs and select them (roving tabindex: only the selected tab is in
// the Tab order). Each tab's id is `${idPrefix}-tab-${key}` and it controls
// `${idPrefix}-panel-${key}`: give your panel that id, role="tabpanel" and
// aria-labelledby the tab id. Internal to STabs; not a public component.
// When the tabs overflow, the bar scrolls sideways, fades the edge that hides
// more tabs and centres the selected tab whenever it changes. Tabs are 44px
// high on touch screens. `dirty` and `errors` (tab keys) put a mark after
// a tab's title (TabMarks), as STabbedForm does. #end sits at the right end
// of the bar, outside the scrolling tabs (the page's actions for all tabs);
// on a phone it takes a row of its own above the bar, so the tabs keep the
// width.
import { computed, nextTick, ref, useId } from 'vue'
import type { TabItem } from '../types'
import { navIndex } from '../keyboard'
import { useCentredSelection, useScrollFade } from '../scrollClip'
import TabMarks from './TabMarks.vue'
import { useViewport } from '../viewport'

const props = withDefaults(
  defineProps<{ tabs: TabItem[]; modelValue: string; idPrefix?: string; size?: 'sm' | 'md'; ariaLabel?: string; dirty?: readonly string[]; errors?: readonly string[] }>(),
  { size: 'md', dirty: () => [], errors: () => [] }
)
const emit = defineEmits<{ (e: 'update:modelValue', v: string): void }>()
const uid = useId()
const prefix = computed(() => props.idPrefix || uid)
const scroller = ref<HTMLElement>()
const nav = ref<HTMLElement>()
const fade = useScrollFade(scroller, nav)
const { compact } = useViewport()

const tabId = (key: string) => `${prefix.value}-tab-${key}`
const panelId = (key: string) => `${prefix.value}-panel-${key}`
const focusKey = computed(() => (props.tabs.some((t) => t.key === props.modelValue && !t.disabled) ? props.modelValue : props.tabs.find((t) => !t.disabled)?.key))

const tabEl = (key: string) => Array.from(nav.value?.querySelectorAll<HTMLElement>('[role="tab"]') ?? []).find((el) => el.dataset.key === key)
useCentredSelection(scroller, () => tabEl(props.modelValue))

function select(key: string) {
  emit('update:modelValue', key)
  nextTick(() => tabEl(key)?.focus())
}
function onKey(e: KeyboardEvent) {
  const enabled = props.tabs.filter((t) => !t.disabled)
  const i = enabled.findIndex((t) => t.key === (document.activeElement as HTMLElement | null)?.dataset?.key)
  const next = navIndex(e.key, i, enabled.length, 'horizontal')
  if (next < 0) return
  e.preventDefault()
  select(enabled[next].key)
}

</script>

<template>
  <div>
    <div v-if="$slots.end && compact" class="mb-2 flex flex-wrap items-center justify-end gap-2" data-testid="tab-bar-end"><slot name="end" /></div>
    <div class="flex items-end gap-3 border-b border-gray-200 dark:border-dark-700">
      <div ref="scroller" class="relative -mb-px min-w-0 flex-1 overflow-x-auto" :class="fade">
        <nav ref="nav" class="flex w-max min-w-full gap-1" role="tablist" :aria-label="ariaLabel" @keydown="onKey">
          <button
            v-for="tab in tabs"
            :id="tabId(tab.key)"
            :key="tab.key"
            type="button"
            role="tab"
            :data-key="tab.key"
            :aria-selected="tab.key === modelValue"
            :aria-controls="panelId(tab.key)"
            :tabindex="tab.key === focusKey ? 0 : -1"
            :disabled="tab.disabled"
            class="whitespace-nowrap border-b-2 font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary-500/40 disabled:cursor-not-allowed disabled:opacity-40 touch:min-h-11"
            :class="[
              size === 'sm' ? 'px-3 py-2 text-xs' : 'px-4 py-2.5 text-sm',
              tab.key === modelValue
                ? 'border-primary-500 text-primary-600 dark:text-primary-400'
                : 'border-transparent text-gray-500 hover:border-gray-300 hover:text-gray-700 dark:text-dark-400 dark:hover:text-gray-200'
            ]"
            @click="emit('update:modelValue', tab.key)"
          >
            {{ tab.label }}
            <span v-if="tab.badge !== undefined" class="ml-1 rounded-full bg-gray-100 px-1.5 text-xs dark:bg-dark-700">{{ tab.badge }}</span>
            <TabMarks :dirty="dirty.includes(tab.key)" :error="errors.includes(tab.key)" class="mb-0.5 ml-1.5 align-middle" />
          </button>
        </nav>
      </div>
      <div v-if="$slots.end && !compact" class="flex shrink-0 items-center gap-2 py-1" data-testid="tab-bar-end"><slot name="end" /></div>
    </div>
  </div>
</template>
