<script setup lang="ts">
// A list page of the asset library's local index: SResourcePage over the
// plugin route with the page's columns (badge, datetime and number formats
// drawn here), the q search the routes honour, and a create dialog for
// callers holding assets:manage.
import { computed, ref } from 'vue'
import { SBadge, SPageHeader, SResourcePage, statusTone, type TableColumn, type Tone } from '@sub2api/ui'
import { useVolcHost } from './host'
import type { IndexPageSpec } from './pages'

const props = defineProps<{ spec: IndexPageSpec }>()
const host = useVolcHost()
const t = host.t
/** index_status of a row the upstream no longer has. */
const TONES: Record<string, Tone> = { missing: 'warning' }

const columns = computed<TableColumn[]>(() =>
  props.spec.columns.map((c) => ({ key: c.key, label: c.label ? t(c.label) : '#', align: c.format === 'number' ? 'right' : 'left' }))
)
const formatted = computed(() => props.spec.columns.filter((c) => c.format))
const empty = (v: unknown) => v === null || v === undefined || v === ''
const creator = ref<{ submit: () => Promise<boolean> } | null>(null)
const submit = () => creator.value?.submit() ?? Promise.resolve(false)
</script>

<template>
  <div>
    <SPageHeader :title="t(spec.title)" />
    <SResourcePage
      :client="host.pluginApi"
      :path="spec.path"
      :columns="columns"
      search="q"
      row-key="id"
      :can-create="host.can('assets:manage')"
      :create-label="t(spec.createLabel)"
      :submit="submit"
    >
      <template v-for="c in formatted" :key="c.key" #[`cell-${c.key}`]="{ value }">
        <template v-if="empty(value)">—</template>
        <SBadge v-else-if="c.format === 'badge'" :tone="statusTone(String(value), TONES)">{{ value }}</SBadge>
        <template v-else-if="c.format === 'datetime'">{{ host.i18n.formatDateTime(value as string) }}</template>
        <template v-else>{{ host.i18n.formatNumber(value as number) }}</template>
      </template>
      <template #create>
        <component :is="spec.form" ref="creator" />
      </template>
    </SResourcePage>
  </div>
</template>
