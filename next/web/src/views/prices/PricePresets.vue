<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { PresetGroup } from './presets'

// Preset templates: chips in labelled groups; picking one replaces what is edited.
defineProps<{ groups: PresetGroup[]; disabled?: boolean }>()
const emit = defineEmits<{ (e: 'pick', key: string): void }>()
const { t } = useI18n()
</script>

<template>
  <div class="flex flex-wrap items-start gap-x-3 gap-y-2" data-testid="price-presets">
    <span class="pt-1 text-sm font-medium text-fg">{{ t('prices.presets.title') }}</span>
    <div class="min-w-0 flex-1 space-y-1.5">
      <div v-for="g in groups" :key="g.key" class="flex flex-wrap items-center gap-1.5">
        <span class="rounded-md bg-primary-50 px-2 py-0.5 text-xs text-primary-700 dark:bg-primary-900/40 dark:text-primary-300">{{ t(`prices.presets.group.${g.key}`) }}</span>
        <button
          v-for="key in g.items"
          :key="key"
          type="button"
          :disabled="disabled"
          class="rounded-full border border-line bg-surface px-2.5 py-0.5 text-xs text-fg transition hover:border-primary-400 hover:text-primary-700 disabled:opacity-50 dark:hover:text-primary-300"
          :data-preset="key"
          @click="emit('pick', key)"
        >
          {{ t(`prices.presets.item.${key}`) }}
        </button>
      </div>
    </div>
  </div>
</template>
