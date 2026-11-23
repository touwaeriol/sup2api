<script setup lang="ts">
// Auto-refresh interval picker: v-model is the interval in seconds (0 =
// off). `choices` lists the offered seconds (default 0, 10, 30, 60). Pair it
// with useAutoRefresh:
//   const seconds = ref(30)
//   useAutoRefresh(list.reload, () => seconds.value * 1000)
//   <SAutoRefresh v-model="seconds" @refresh="list.reload" />
//
// A button with a menu rather than a select, so it stays one control wide in a
// toolbar. It shows the current interval, or just its name when off. On a
// compact screen it is a square refresh icon with a dot while running, and
// with @refresh bound its menu starts with "Refresh now". `button` adds a
// Refresh button after it on wide screens (spinning while `loading`; only
// with @refresh bound); the
// phone keeps the one icon button, whose menu has "Refresh now".
import { computed } from 'vue'
import { useUiT } from './i18n'
import SButton from './SButton.vue'
import SDropdown from './SDropdown.vue'
import type { MenuAction } from './types'
import { useViewport } from './viewport'

const props = withDefaults(defineProps<{ modelValue: number; choices?: number[]; size?: 'sm' | 'md'; disabled?: boolean; button?: boolean; loading?: boolean; onRefresh?: () => void }>(), {
  choices: () => [0, 10, 30, 60],
  size: 'md',
  onRefresh: undefined
})
const emit = defineEmits<{ (e: 'update:modelValue', v: number): void }>()
const t = useUiT()
const { compact } = useViewport()
const every = (n: number) => t('ui.autoRefreshEvery', { n })
const actions = computed<MenuAction[]>(() => {
  const list = props.choices.includes(props.modelValue) ? props.choices : [...props.choices, props.modelValue].sort((a, b) => a - b)
  const intervals = list.map((n) => ({ key: String(n), label: n > 0 ? every(n) : t('ui.autoRefreshOff'), checked: n === props.modelValue }))
  if (!compact.value || !props.onRefresh) return intervals
  return [{ key: 'now', label: t('ui.refreshNow'), icon: 'refresh' }, { ...intervals[0], divider: true }, ...intervals.slice(1)]
})
function select(key: string) {
  if (key === 'now') props.onRefresh?.()
  else emit('update:modelValue', Number(key))
}
</script>

<template>
  <span class="inline-flex items-center gap-2">
    <SDropdown :actions="actions" width="9rem" :disabled="disabled" @select="select">
      <template #trigger="{ toggle, attrs }">
        <SButton
          icon="refresh"
          :icon-right="compact ? undefined : 'chevron-down'"
          :square="compact"
          :class="compact ? 'relative' : ''"
          :size="size"
          :disabled="disabled"
          :title="t('ui.autoRefresh')"
          :aria-label="modelValue > 0 ? `${t('ui.autoRefresh')}: ${every(modelValue)}` : t('ui.autoRefresh')"
          data-testid="auto-refresh"
          v-bind="attrs"
          @click.stop="toggle"
        >
          <template v-if="!compact">{{ modelValue > 0 ? every(modelValue) : t('ui.autoRefresh') }}</template>
          <span v-else-if="modelValue > 0" class="absolute right-1 top-1 h-2 w-2 rounded-full bg-success-500" aria-hidden="true" data-testid="auto-refresh-dot" />
        </SButton>
      </template>
    </SDropdown>
    <SButton
      v-if="button && onRefresh && !compact"
      icon="refresh"
      :size="size"
      :loading="loading"
      :disabled="disabled"
      data-testid="auto-refresh-now"
      @click="onRefresh?.()"
    >
      {{ t('ui.refresh') }}
    </SButton>
  </span>
</template>
