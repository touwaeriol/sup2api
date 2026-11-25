<script setup lang="ts">
// A payment method's mark: its color as a rounded tile with the brand
// glyph of a known icon key, else the first letter of its name.
import { computed } from 'vue'
import { SIcon } from '@sub2api/ui'

const props = withDefaults(defineProps<{ icon?: string; color?: string; name?: string; size?: 'sm' | 'md' }>(), { icon: '', color: '', name: '', size: 'sm' })

const GLYPHS: Record<string, string> = { alipay: '支', wxpay: '微', qqpay: 'Q', stripe: 'S' }
const glyph = computed(() => GLYPHS[props.icon] ?? (props.name.trim().charAt(0).toUpperCase() || '?'))
</script>

<template>
  <span class="payment-mark" :class="`payment-mark-${size}`" :style="color ? { background: color } : undefined" aria-hidden="true">
    <SIcon v-if="icon === 'card'" name="balance" class="payment-mark-icon" />
    <template v-else>{{ glyph }}</template>
  </span>
</template>
