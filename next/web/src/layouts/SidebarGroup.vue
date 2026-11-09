<script setup lang="ts">
/** A run of sidebar entries under a title; collapsed to icons, a rule sets it apart from the group above instead. */
defineProps<{ title?: string; collapsed?: boolean; divided?: boolean }>()
</script>

<template>
  <div class="mb-3">
    <hr v-if="collapsed && divided" class="mx-2 mb-3 border-line" aria-hidden="true" />
    <div v-if="!collapsed && title" class="mb-1 px-3 text-xxs font-semibold uppercase tracking-wider text-fg-subtle">{{ title }}</div>
    <slot />
  </div>
</template>
