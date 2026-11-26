<script setup lang="ts">
// A payment method's mark followed by its name, inline (table cells, detail
// rows); the default slot adds a note after the name.
import MethodIcon from './MethodIcon.vue'
import type { MethodLook } from './methodIcon'

withDefaults(defineProps<{ method: MethodLook; size?: 'sm' | 'md' }>(), { size: 'sm' })
</script>

<template>
  <span class="inline-flex min-w-0 max-w-full items-center gap-2 align-middle">
    <MethodIcon :method="method" :size="size" />
    <span class="truncate">{{ method.name }}</span>
    <slot />
  </span>
</template>
