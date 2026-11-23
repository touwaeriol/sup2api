<script setup lang="ts">
// Page header. Directly in an app shell (provideTitleContext with a sink) the
// shell's title bar shows the page's title and description, and on a wide
// screen what the page puts beside its title (#before, #title-extra); the
// header itself is a single toolbar row in the page: #filters on the left,
// #actions on the right (alone, they end the row). Actions never go into the
// title bar: every page keeps them in the same place. The header is a
// toolbar (toolbar.ts): its buttons take one size, and directly in a shell a
// primary button with an icon floats on a phone.
// In a nested scope (a tab, a dialog) or outside a shell it is a title block
// with the actions beside it and the filters below; a title that repeats the
// scope's is not drawn again.
// `#filters-primary` holds the filters that stay in sight on a phone; on
// wide screens they come before #filters. On a compact screen #filters fold
// behind a Filters button (badge `filtersActiveCount`) that opens a bottom
// sheet with them.
import { computed, onBeforeUnmount, useSlots, watchEffect } from 'vue'
import { usePageHeadSink, useTitleVisibility } from './titleContext'
import { useViewport } from './viewport'
import { provideToolbar } from './toolbar'
import FilterSheet from './internal/FilterSheet.vue'

const props = withDefaults(defineProps<{ title: string; description?: string; filtersActiveCount?: number }>(), {
  description: undefined,
  filtersActiveCount: 0
})
const slots = useSlots()
const sink = usePageHeadSink()
const lifted = !!sink
const distinct = useTitleVisibility(() => props.title)
const showTitle = computed(() => !lifted && distinct.value)
provideToolbar({ float: lifted })

// A phone's title bar has room for the title only: what goes with the title
// and the actions stay in the page there (and the description is not shown).
// These are read while rendering, not cached: a slot can come and go (a
// `<template v-if #title-extra>`), and the slots object is not reactive.
const { compact } = useViewport()
const hasExtras = () => !!slots.before || !!slots['title-extra']
const extrasUp = () => lifted && !!sink.extra && hasExtras() && !compact.value
const headed = () => (lifted ? hasExtras() && !extrasUp() : showTitle.value || hasExtras() || !!props.description)
const hasFilters = () => !!slots.filters || !!slots['filters-primary']

const owner = ++headers
watchEffect(() => {
  if (sink) sink.head.value = { owner, title: props.title, description: props.description || '' }
})
onBeforeUnmount(() => {
  if (sink && sink.head.value?.owner === owner) sink.head.value = null
})
</script>

<script lang="ts">
let headers = 0
</script>

<template>
  <div
    v-if="headed()"
    class="mb-4 flex flex-wrap items-start justify-between gap-3"
  >
    <div class="min-w-0">
      <div class="flex items-center gap-2">
        <slot name="before" />
        <h1
          v-if="showTitle"
          class="truncate text-lg font-semibold text-gray-900 dark:text-white"
        >
          {{ title }}
        </h1>
        <slot name="title-extra" />
      </div>
      <p
        v-if="description && !lifted"
        class="mt-0.5 text-sm text-gray-500 dark:text-dark-400"
      >
        {{ description }}
      </p>
    </div>
    <div
      v-if="$slots.actions"
      class="flex flex-wrap items-center gap-2"
    >
      <slot name="actions" />
    </div>
  </div>
  <Teleport
    v-if="extrasUp()"
    defer
    :to="sink!.extra!"
  >
    <div
      class="flex shrink-0 items-center gap-2"
      data-testid="page-title-extra"
    >
      <slot name="before" />
      <slot name="title-extra" />
    </div>
  </Teleport>
  <div
    v-if="hasFilters() || (!headed() && $slots.actions)"
    class="mb-3 flex flex-wrap items-end gap-2"
  >
    <div
      v-if="hasFilters()"
      class="flex min-w-0 flex-1 flex-wrap items-end gap-2"
    >
      <FilterSheet
        :collapse="compact"
        :active-count="filtersActiveCount"
      >
        <template
          v-if="$slots['filters-primary']"
          #primary
        >
          <slot name="filters-primary" />
        </template>
        <template
          v-if="$slots.filters"
          #default
        >
          <slot name="filters" />
        </template>
      </FilterSheet>
    </div>
    <div
      v-if="!headed() && $slots.actions"
      class="ms-auto flex max-w-full shrink-0 flex-wrap items-center gap-2"
    >
      <slot name="actions" />
    </div>
  </div>
</template>
