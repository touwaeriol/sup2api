<script setup lang="ts">
// The frame of a form in tabs (UI 4.1): the tabs on top (layout "top", the
// bar of STabs) or as a side navigation with numbers and summaries (layout
// "side"; a scrolling row on phones). Each tab shows a dot for unsaved
// changes and a red one for errors, from `form` (useTabbedForm). v-model is
// the tab in front. The default slot is rendered once per tab with `{ tab }`
// (the key), each in its own panel: every panel stays mounted and only the
// one in front shows, so the drafts of the others live on. A form of one tab
// shows no tabs, as STabs does.
import { computed, useId } from 'vue'
import TabBar from './internal/TabBar.vue'
import SideTabNav from './internal/SideTabNav.vue'
import STitleScope from './STitleScope.vue'
import type { TabbedForm } from './tabbedForm'
import type { FormTab } from './types'

const props = withDefaults(
  defineProps<{
    tabs: FormTab[]
    modelValue: string
    layout?: 'top' | 'side'
    form?: Pick<TabbedForm, 'dirtyTabs' | 'errorTabs'>
    idPrefix?: string
    ariaLabel?: string
  }>(),
  { layout: 'top' }
)
const emit = defineEmits<{ (e: 'update:modelValue', v: string): void }>()
defineSlots<{ default(props: { tab: string }): unknown }>()
const uid = useId()
const prefix = computed(() => props.idPrefix || uid)
const current = computed(() => (props.tabs.some((t) => t.key === props.modelValue && !t.disabled) ? props.modelValue : props.tabs.find((t) => !t.disabled)?.key))
const marks = computed(() => ({ dirty: props.form?.dirtyTabs.value ?? [], errors: props.form?.errorTabs.value ?? [] }))
const select = (key: string) => emit('update:modelValue', key)
const nav = computed(() => props.tabs.length > 1)
</script>

<template>
  <div :class="nav && layout === 'side' ? 'grid gap-4 md:grid-cols-[13rem_minmax(0,1fr)]' : 'space-y-5'" :data-layout="layout">
    <template v-if="nav">
      <TabBar v-if="layout === 'top'" :tabs="tabs" :model-value="current ?? ''" :id-prefix="prefix" :aria-label="ariaLabel" v-bind="marks" @update:model-value="select" />
      <SideTabNav v-else :tabs="tabs" :model-value="current ?? ''" :id-prefix="prefix" :aria-label="ariaLabel" v-bind="marks" @update:model-value="select" />
    </template>
    <div class="min-w-0">
      <STitleScope
        v-for="tab in tabs"
        v-show="tab.key === current"
        :id="nav ? `${prefix}-panel-${tab.key}` : undefined"
        :key="tab.key"
        :title="nav ? tab.label : undefined"
        :role="nav ? 'tabpanel' : undefined"
        :aria-labelledby="nav ? `${prefix}-tab-${tab.key}` : undefined"
        :tabindex="nav ? 0 : undefined"
        class="space-y-5 focus:outline-none"
        :data-tab="tab.key"
      >
        <slot :tab="tab.key" />
      </STitleScope>
    </div>
  </div>
</template>
