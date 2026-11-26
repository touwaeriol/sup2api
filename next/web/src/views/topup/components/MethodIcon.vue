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
const SIZES = {
  sm: 'h-5 w-5 rounded-md text-[0.6875rem]',
  md: 'h-7 w-7 rounded-lg text-[0.8125rem]',
  lg: 'h-10 w-10 rounded-[0.625rem] text-lg'
} as const
const background = computed(() => props.method.color || builtin.value?.color)
</script>

<template>
  <span
    class="inline-flex shrink-0 items-center justify-center overflow-hidden font-semibold leading-none text-white"
    :class="[SIZES[size], src ? 'bg-transparent' : 'bg-fg-muted']"
    :style="!src && background ? { background } : undefined"
    aria-hidden="true"
  >
    <img
      v-if="src"
      :src="src"
      alt=""
      class="h-full w-full object-cover"
      draggable="false"
      referrerpolicy="no-referrer"
      @error="failed = true"
    />
    <SIcon v-else-if="builtin?.uiIcon" :name="builtin.uiIcon" class="h-[60%] w-[60%]" />
    <template v-else>{{ glyph }}</template>
  </span>
</template>
