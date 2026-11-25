<script setup lang="ts">
import { computed } from 'vue'
import { encodeQr, qrPath } from './qr'

// A QR code drawn as SVG. Always dark on white whatever the theme: scanners
// need the contrast (and a light quiet zone).
const props = withDefaults(defineProps<{ value: string; size?: number; label?: string }>(), { size: 176 })
const MARGIN = 4

const code = computed(() => {
  try {
    const m = encodeQr(props.value)
    return { path: qrPath(m, MARGIN), box: m.length + MARGIN * 2 }
  } catch {
    return null
  }
})
</script>

<template>
  <svg v-if="code" class="pay-qr" :width="size" :height="size" :viewBox="`0 0 ${code.box} ${code.box}`" role="img" :aria-label="label" shape-rendering="crispEdges">
    <rect :width="code.box" :height="code.box" fill="#fff" />
    <path :d="code.path" fill="#000" />
  </svg>
</template>

<style scoped>
.pay-qr {
  display: block;
  border-radius: 0.5rem;
  border: 1px solid rgb(var(--line));
}
</style>
