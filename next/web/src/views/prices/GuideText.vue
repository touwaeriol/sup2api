<script setup lang="ts">
import { computed } from 'vue'

// A text of the expression help from the locale files, with its inline
// marks rendered: `code` and **bold**.
const props = defineProps<{ text: string }>()

const MARK = /(`[^`]+`|\*\*[^*]+\*\*)/
/** Split with one capture group: the odd parts are the marks. */
const parts = computed(() =>
  props.text
    .split(MARK)
    .map((s, i) => (i % 2 === 0 ? { kind: 'text', s } : s.startsWith('`') ? { kind: 'code', s: s.slice(1, -1) } : { kind: 'b', s: s.slice(2, -2) }))
    .filter(p => p.s)
)
</script>

<template>
  <template v-for="(p, i) in parts" :key="i"
    ><code v-if="p.kind === 'code'" class="rounded bg-surface-2 px-1 py-px font-mono text-xs text-fg">{{ p.s }}</code
    ><b v-else-if="p.kind === 'b'" class="font-semibold text-fg">{{ p.s }}</b
    ><template v-else>{{ p.s }}</template></template
  >
</template>
