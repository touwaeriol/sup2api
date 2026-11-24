<script setup lang="ts">
// A plugin id as people see it, the display_id the core sends
// (manifest.DisplayID, CONTRACTS §137): the bare key of an official plugin,
// "publisher/key" for any other, monospaced with the "publisher/" part muted.
import { computed } from 'vue'

/** id: the plugin id, kept on the element; displayId: the payload's display_id. */
const props = defineProps<{ id: string; displayId: string }>()
const parts = computed(() => {
  const at = props.displayId.indexOf('/')
  return { prefix: props.displayId.slice(0, at + 1), key: props.displayId.slice(at + 1) }
})
</script>

<template>
  <span class="font-mono" :data-plugin-id="id"><span v-if="parts.prefix" class="text-fg-subtle">{{ parts.prefix }}</span>{{ parts.key }}</span>
</template>
