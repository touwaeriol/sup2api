<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SButton, SCard, SCode, SDisclosure, SHint, SPagination, SStatusBadge, useAutoRefresh } from '@sub2api/ui'
import PluginStatusBadge from './PluginStatusBadge.vue'
import type { PluginHistory, PluginRelease } from '@/api/observability'
import { formatDateTime } from '@/utils/format'
import { errorMessage } from '@/utils/errors'
import { usePluginStatusLabel } from '../pluginUtil'
import { displayPluginId } from '../pluginId'
const props = defineProps<{ pluginKey?: string; since?: string }>()
const { t } = useI18n()
const statusLabel = usePluginStatusLabel()
const items = ref<PluginRelease[]>([]), events = ref<PluginHistory[]>([])
const selected = ref<PluginRelease | null>(null), page = ref(1), total = ref(0), eventPage = ref(1), eventTotal = ref(0), error = ref('')
const allEvents = ref(false)
const size = 10
let serial = 0, disposed = false, eventSerial = 0
const groups = computed(() => [...new Set(events.value.map(e => e.node_id || ''))].map(node => ({ node, events: events.value.filter(e => e.node_id === node).slice().reverse() })))
async function load() {
  const token = ++serial
  try {
    const path = props.pluginKey ? `/plugins/${encodeURIComponent(props.pluginKey)}/rollouts` : '/plugins/rollouts'
    const result = await api.list<PluginRelease>(path, { page: page.value, page_size: size, since: props.since })
    if (disposed || token !== serial) return
    items.value = result.items; total.value = result.page.total; error.value = ''
    if (selected.value) selected.value = items.value.find(r => r.id === selected.value?.id) || selected.value
    if (selected.value || allEvents.value) await loadEvents()
  } catch (e) { if (!disposed && token === serial) error.value = errorMessage(e) }
}
async function loadEvents() {
  const key = selected.value?.plugin_key || props.pluginKey
  if (!key) return
  const token = ++eventSerial, id = selected.value?.id
  try {
    const result = await api.list<PluginHistory>(`/plugins/${encodeURIComponent(key)}/history`, { rollout_id: id, page: eventPage.value, page_size: 50 })
    if (disposed || token !== eventSerial || selected.value?.id !== id) return
    events.value = result.items; eventTotal.value = result.page.total
  } catch (e) { if (!disposed && token === eventSerial) error.value = errorMessage(e) }
}
function select(item: PluginRelease) { allEvents.value = false; selected.value = item; eventPage.value = 1; events.value = []; void loadEvents() }
function showAllEvents() { allEvents.value = true; selected.value = null; eventPage.value = 1; events.value = []; void loadEvents() }
watch([() => props.pluginKey, () => props.since], () => { ++eventSerial; allEvents.value = false; selected.value = null; events.value = []; page.value = 1; void load() })
watch(page, () => { selected.value = null; events.value = []; void load() })
watch(eventPage, () => { void loadEvents() })
useAutoRefresh(load, 5000)
onMounted(() => { void load() })
onBeforeUnmount(() => { disposed = true })
</script>
<template>
  <SCard :title="t(pluginKey ? 'observe.history' : 'observe.related')" class="mt-5">
    <SHint v-if="!pluginKey">{{ t('observe.relatedHint') }}</SHint>
    <SButton v-else size="sm" class="mb-3" @click="showAllEvents">{{ t('observe.allEvents') }}</SButton>
    <SHint v-if="error" tone="warning">{{ error }}</SHint>
    <SHint v-if="!items.length && !error">{{ t('observe.noHistory') }}</SHint>
    <div class="space-y-3">
      <div v-for="item in items" :key="item.id" class="rounded-xl border border-gray-200 p-3 dark:border-dark-700">
        <button
          type="button"
          class="flex w-full flex-wrap items-center gap-2 rounded text-left text-sm focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500/40"
          :aria-pressed="selected?.id === item.id"
          @click="select(item)"
        >
          <strong>{{ displayPluginId(item.plugin_key) }}</strong>
          <SCode inline :text="`#${item.id} · ${item.from_version || '—'} → ${item.target_version || '—'}`" />
          <PluginStatusBadge :status="item.phase" />
          <SHint inline size="xs">{{ formatDateTime(item.created_at) }}</SHint>
        </button>
        <div class="mt-2 flex flex-wrap gap-2">
          <SStatusBadge v-for="node in item.nodes" :key="node.boot_id" :status="node.state" :label="`${node.node_id} · ${statusLabel(node.state)}`" :dot="false" :title="node.error" />
        </div>
        <SHint v-if="item.error" tone="warning">{{ item.error }}</SHint>
      </div>
    </div>
    <SPagination v-if="total > size" v-model:page="page" :page-size="size" :page-sizes="[size]" :total="total" />
    <div v-if="selected || allEvents" class="space-y-3 border-t border-gray-200 pt-4 dark:border-dark-700">
      <h3 class="font-semibold">{{ t('observe.timeline') }} · {{ displayPluginId(selected?.plugin_key || pluginKey || '') }} <template v-if="selected">#{{ selected.id }}</template></h3>
      <SHint v-if="!events.length">{{ t('observe.noHistory') }}</SHint>
      <div v-for="group in groups" :key="group.node" class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800">
        <strong class="text-sm">{{ group.node || t('observe.cluster') }}</strong>
        <ol class="mt-2 space-y-3 border-l border-primary-300 pl-4 text-xs">
          <li v-for="event in group.events" :key="event.id">
            <SHint inline size="xs">{{ formatDateTime(event.created_at) }} · {{ event.boot_id.slice(0, 8) }}</SHint>
            <div class="my-1 flex flex-wrap items-center gap-2">
              <PluginStatusBadge :status="event.state" />
              <SCode v-if="event.version" inline :text="event.version" />
            </div>
            <SDisclosure v-if="event.message" :label="t('observe.detail')">
              <pre class="whitespace-pre-wrap break-all">{{ event.message }}</pre>
            </SDisclosure>
          </li>
        </ol>
      </div>
      <SPagination v-if="eventTotal > 50" v-model:page="eventPage" :page-size="50" :page-sizes="[50]" :total="eventTotal" />
    </div>
  </SCard>
</template>
