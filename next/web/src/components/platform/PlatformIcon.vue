<script setup lang="ts">
import { computed } from 'vue'
import { usePlatforms } from '@/composables/platforms'
import BrandMark from './BrandMark.vue'
import { platformBrand } from './brands'

// A platform's brand mark in its vendor's colour, named by the platform's
// label, or by `detail` when the mark stands for more than the platform.
const props = defineProps<{ id: string; detail?: string }>()
const platforms = usePlatforms()
platforms.load()
const brand = computed(() => platformBrand(props.id))
</script>

<template>
  <BrandMark :brand="brand" :label="detail || platforms.label(id)" :data-platform="id" />
</template>
