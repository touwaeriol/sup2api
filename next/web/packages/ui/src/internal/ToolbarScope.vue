<script setup lang="ts">
// Makes its slot a toolbar (see toolbar.ts) without adding an element.
// `float` is read when a button asks, so it follows the prop.
import { provideToolbar } from '../toolbar'

const props = withDefaults(defineProps<{ float?: boolean }>(), { float: false })
provideToolbar({ get float() { return props.float } })
</script>

<template>
  <slot />
</template>
