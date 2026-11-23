<script setup lang="ts">
// Tabs over one panel. #end goes at the right end of the tab bar (actions
// for every tab), or on a row of its own when the bar is not shown.
import { computed, useId } from 'vue'
import TabBar from './internal/TabBar.vue'
import STitleScope from './STitleScope.vue'
import type { TabItem } from './types'

const props = withDefaults(defineProps<{
  tabs: TabItem[]
  modelValue: string
  showTabs?: boolean
  showSingle?: boolean
  idPrefix?: string
  size?: 'sm' | 'md'
  ariaLabel?: string
}>(), { showTabs: true, showSingle: false, size: 'md' })
const emit = defineEmits<{ (e: 'update:modelValue', value: string): void }>()
const uid = useId()
const prefix = computed(() => props.idPrefix || uid)
const current = computed(() => props.tabs.find(tab => tab.key === props.modelValue && !tab.disabled) || props.tabs.find(tab => !tab.disabled))
const visible = computed(() => props.showTabs && props.tabs.length > (props.showSingle ? 0 : 1))
</script>
<template>
  <div class="space-y-5">
    <TabBar
      v-if="visible"
      :tabs="tabs"
      :model-value="current?.key || ''"
      :id-prefix="prefix"
      :size="size"
      :aria-label="ariaLabel"
      @update:model-value="emit('update:modelValue', $event)"
    >
      <template v-if="$slots.end" #end><slot name="end" /></template>
    </TabBar>
    <div v-else-if="$slots.end" class="flex flex-wrap items-center justify-end gap-2"><slot name="end" /></div>
    <STitleScope
      :id="visible && current ? `${prefix}-panel-${current.key}` : undefined"
      class="space-y-5"
      :title="visible ? current?.label : undefined"
      :role="visible ? 'tabpanel' : undefined"
      :aria-labelledby="visible && current ? `${prefix}-tab-${current.key}` : undefined"
      :tabindex="visible ? 0 : undefined"
    >
      <slot :active="current?.key || ''" />
    </STitleScope>
  </div>
</template>
