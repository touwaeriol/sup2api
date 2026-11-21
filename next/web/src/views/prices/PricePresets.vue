<script setup lang="ts">
// 预设模板: chips in labelled groups; picking one replaces what is edited.
// PROTOTYPE: new texts are Chinese literals until adopted.
defineProps<{ groups: Array<{ label: string; items: Array<{ key: string; label: string }> }>; disabled?: boolean }>()
const emit = defineEmits<{ (e: 'pick', key: string): void }>()
</script>

<template>
  <div class="flex flex-wrap items-start gap-x-3 gap-y-2" data-testid="price-presets">
    <span class="pt-1 text-sm font-medium text-fg">预设模板</span>
    <div class="min-w-0 flex-1 space-y-1.5">
      <div v-for="g in groups" :key="g.label" class="flex flex-wrap items-center gap-1.5">
        <span class="rounded-md bg-primary-50 px-2 py-0.5 text-xs text-primary-700 dark:bg-primary-900/40 dark:text-primary-300">{{ g.label }}</span>
        <button
          v-for="x in g.items"
          :key="x.key"
          type="button"
          :disabled="disabled"
          class="rounded-full border border-line bg-surface px-2.5 py-0.5 text-xs text-fg transition hover:border-primary-400 hover:text-primary-700 disabled:opacity-50 dark:hover:text-primary-300"
          :data-preset="x.key"
          @click="emit('pick', x.key)"
        >
          {{ x.label }}
        </button>
      </div>
    </div>
  </div>
</template>
