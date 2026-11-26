<script setup lang="ts">
// A payment method's mark: its custom logo cropped to a rounded square (an
// <img>, so an SVG's scripts never run), else its built-in icon's glyph, or
// its initial, on its color. A logo that fails to load falls back too.
import { computed, ref, watch } from 'vue'
import { SIcon } from '@sub2api/ui'
import { builtinIcon, parseLogo, type MethodLook } from './methodIcon'

const props = withDefaults(defineProps<{ method: MethodLook; size?: 'sm' | 'md' | 'lg' }>(), { size: 'md' })

const failed = ref(false)
watch(
  () => props.method.logo,
  () => (failed.value = false)
)

const src = computed(() => (!failed.value && parseLogo(props.method.logo) ? props.method.logo : ''))
const builtin = computed(() => builtinIcon(props.method.icon))
const glyph = computed(() => builtin.value?.glyph ?? (Array.from(props.method.name.trim())[0]?.toUpperCase() || '·'))
const background = computed(() => props.method.color || builtin.value?.color)
</script>

<template>
  <span class="payment-method-icon" :class="[`payment-method-icon--${size}`, { 'payment-method-icon--logo': src }]" :style="!src && background ? { background } : undefined" aria-hidden="true">
    <img v-if="src" :src="src" alt="" class="payment-method-icon-img" draggable="false" referrerpolicy="no-referrer" @error="failed = true" />
    <SIcon v-else-if="builtin?.uiIcon" :name="builtin.uiIcon" class="payment-method-icon-ui" />
    <template v-else>{{ glyph }}</template>
  </span>
</template>

<style scoped>
.payment-method-icon {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  width: 1.75rem;
  height: 1.75rem;
  border-radius: 0.5rem;
  background: rgb(var(--fg-muted));
  color: #fff;
  font-size: 0.8125rem;
  font-weight: 600;
  line-height: 1;
}
.payment-method-icon--logo {
  background: transparent;
}
.payment-method-icon--sm {
  width: 1.25rem;
  height: 1.25rem;
  border-radius: 0.375rem;
  font-size: 0.6875rem;
}
.payment-method-icon--lg {
  width: 2.5rem;
  height: 2.5rem;
  border-radius: 0.625rem;
  font-size: 1.125rem;
}
.payment-method-icon-img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.payment-method-icon-ui {
  width: 60%;
  height: 60%;
}
</style>
