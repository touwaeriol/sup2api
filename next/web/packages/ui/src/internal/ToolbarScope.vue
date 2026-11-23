<script setup lang="ts">
// Makes its slot a toolbar (see toolbar.ts) without adding an element.
import { provideToolbar } from '../toolbar'

const props = withDefaults(defineProps<{ float?: boolean }>(), { float: false })
provideToolbar({ float: props.float })
</script>

<template>
  <slot />
</template>
