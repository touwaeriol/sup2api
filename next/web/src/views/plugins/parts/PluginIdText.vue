<script setup lang="ts">
// A plugin id as people see it (manifest.DisplayID, CONTRACTS §137): the bare
// key of an official plugin, "publisher/key" for any other, monospaced with
// the "publisher/" part muted.
import { computed } from 'vue'
import { OFFICIAL_PUBLISHER, parsePluginId } from '../pluginUtil'

const props = defineProps<{ id: string }>()
const parts = computed(() => {
  const p = parsePluginId(props.id)
  return p && p.publisher !== OFFICIAL_PUBLISHER ? { prefix: `${p.publisher}/`, key: p.key } : { prefix: '', key: props.id }
})
</script>

<template>
  <span class="font-mono" :data-plugin-id="id"><span v-if="parts.prefix" class="text-fg-subtle">{{ parts.prefix }}</span>{{ parts.key }}</span>
</template>
