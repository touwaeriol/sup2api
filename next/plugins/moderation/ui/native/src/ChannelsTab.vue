<script setup lang="ts">
import { computed, ref } from 'vue'
import { isApiError } from '@sub2api/host'
import {
  SAlert,
  SBadge,
  SButton,
  SCard,
  SChipList,
  SDropdown,
  SHint,
  SRowAction,
  SSwitch,
  STable,
  STableColumnSettings,
  useTableColumns,
  type ChipListItem,
  type MenuAction,
  type TableColumn
} from '@sub2api/ui'
import ChannelEditModal from './ChannelEditModal.vue'
import EnabledBadge from './EnabledBadge.vue'
import PriceCell from './PriceCell.vue'
import RateCell from './RateCell.vue'
import { useCatalog } from './catalog'
import { callCostText, enumLabel, joinList, money, ms, num } from './format'
import { canManage, channelBody, confirmDelete, deleteChannel, errorDetails, errorMessage, saveChannel, testChannel, toastError, useModHost } from './host'
import type { Channel } from './types'

// Channels (CONTRACTS §72.6, new-api channels): the upstreams auditors call,
// with the auditors using each one, last-hour health and today's cost. Keys
// and proxies are sealed with the host key; only the key's last 4 characters
// and the proxy without its password are shown. Rows come from the shared
// catalog; a change refreshes the lists it touches (enabling a channel
// changes whether its auditors are usable). Read users open a channel
// read-only.
defineProps<{ alertPercent: number }>()
const host = useModHost()
const t = host.t
const manage = computed(() => canManage())

const catalog = useCatalog()
const rows = computed(() => catalog.state.channels)
const loading = computed(() => catalog.state.loading)
const error = computed(() => (catalog.state.errors.channels ? errorMessage(catalog.state.errors.channels, t('channels.loadFailed')) : ''))
const reload = () => catalog.refresh(['channels', 'auditors'])
defineExpose({ reload })

// One line per cell: the key, protocol, timeout, proxy, billing model,
// today's tokens and the price state are optional columns (and tooltips of
// the cells they belong to); a channel not priced says so next to today's cost.
const columns = computed<TableColumn[]>(() => [
  { key: 'name', label: t('channels.col.name'), nowrap: true, hideable: false, mobile: 'title' },
  { key: 'type', label: t('channels.col.type'), nowrap: true, mobile: 'meta' },
  { key: 'protocol', label: t('channels.protocol'), nowrap: true, defaultHidden: true, mobile: 'detail' },
  { key: 'base_url', label: t('channels.col.baseUrl'), grow: true, mobile: 'subtitle' },
  { key: 'key', label: t('channels.col.key'), nowrap: true, defaultHidden: true, mobile: 'detail' },
  { key: 'timeout', label: t('channels.col.timeout'), align: 'right', nowrap: true, defaultHidden: true, mobile: 'detail' },
  { key: 'proxy', label: t('channels.proxy'), nowrap: true, defaultHidden: true, mobile: 'detail' },
  { key: 'model', label: t('channels.col.model'), ellipsis: '10rem', mobile: 'meta' },
  { key: 'billing_model', label: t('channels.billingModel'), ellipsis: '10rem', defaultHidden: true, mobile: 'detail' },
  { key: 'used_by', label: t('channels.col.usedBy'), nowrap: true, mobile: 'meta' },
  { key: 'hour', label: t('channels.col.hour'), align: 'right', nowrap: true, mobile: 'meta' },
  { key: 'today', label: t('channels.col.today'), align: 'right', nowrap: true, mobile: 'meta' },
  { key: 'tokens', label: t('channels.col.tokens'), align: 'right', nowrap: true, defaultHidden: true, mobile: 'detail' },
  { key: 'price', label: t('channels.col.price'), nowrap: true, defaultHidden: true, mobile: 'detail' },
  { key: 'enabled', label: t('channels.col.enabled'), nowrap: true, hideable: false, mobile: 'status' },
  { key: 'actions', label: t('actions'), align: 'right', nowrap: true, hideable: false }
])
const columnState = useTableColumns(columns, 'moderation-channels')

const usedBy = (c: Channel): ChipListItem[] => (c.used_by || []).map((a) => ({ key: String(a.id), label: a.name || `#${a.id}` }))

/** The address with its timeout and proxy, the tooltip of the address cell. */
const urlTitle = (c: Channel) =>
  [c.base_url, `${t('channels.col.timeout')} ${ms(c.timeout_ms)}`, c.has_proxy ? t('channels.viaProxy') : ''].filter(Boolean).join('\n')

/** Today's tokens, cost by source ("审核 $0.01 · 在线测试 $0.02", sources with calls only) and unpriced calls: the tooltip of today's cost. */
function todayTitle(c: Channel): string {
  const s = c.stats
  if (!s) return ''
  const sources = Object.entries(s.cost_today_sources || {})
    .filter(([, v]) => v.calls > 0)
    .map(([k, v]) => `${enumLabel('costSource', k)} ${money(v.cost)}`)
    .join(' · ')
  const unpriced = s.cost_today.unpriced_calls ? t('channels.unpricedN', { n: num(s.cost_today.unpriced_calls) }) : ''
  return [t('channels.tokensN', { n: num(s.tokens_today) }), sources, unpriced].filter(Boolean).join('\n')
}

const editOpen = ref(false)
const editing = ref<Channel | null>(null)
function openEdit(c: Channel | null) {
  editing.value = c
  editOpen.value = true
}

// Inline enable / disable (new-api channel-row-actions), confirmed first.
const toggling = ref<number | null>(null)
async function setEnabled(c: Channel, enabled: boolean) {
  const ok = await host.confirm({ message: t(enabled ? 'channels.enableConfirm' : 'channels.disableConfirm', { name: c.name }), danger: !enabled })
  if (!ok) return
  toggling.value = c.id
  try {
    await saveChannel(c.id, channelBody({ ...c, enabled }))
    c.enabled = enabled
    host.toast(t('saved'), 'success')
    void catalog.refresh(['channels', 'auditors'])
  } catch (e) {
    toastError(e)
  } finally {
    toggling.value = null
  }
}

/** Why a channel cannot be deleted: the auditors using it. */
function deleteError(e: unknown): string {
  if (!isApiError(e) || e.status !== 409) return errorMessage(e, t('actionFailed'))
  const names = errorDetails(e, 'auditors').map((a) => a.name || `#${a.id}`)
  return names.length ? t('channels.inUse', { names: joinList(names) }) : errorMessage(e, t('actionFailed'))
}

/** The row's "more" menu: the danger action. */
const moreActions = computed<MenuAction[]>(() => [{ key: 'delete', label: t('delete'), danger: true }])
const remove = (c: Channel) =>
  confirmDelete(t('channels.deleteConfirm', { name: c.name }), () => deleteChannel(c.id), () => void catalog.refresh(['channels']), deleteError)

const testing = ref<number | null>(null)
async function test(c: Channel) {
  testing.value = c.id
  try {
    const r = await testChannel(c.id)
    const url = r.url ? ` · ${r.url}` : ''
    if (r.ok) {
      const msg = t('channels.testOk', { ms: num(r.latency_ms), model: r.model || c.model })
      host.toast((r.cost !== undefined ? t('channels.testCost', { msg, cost: callCostText(r.cost).text }) : msg) + url, 'success')
      if (r.cost?.mode === 'priced') void catalog.refresh(['channels'])
    } else host.toast(t('channels.testFailed', { err: [enumLabel('err', r.error), r.status, r.message].filter(Boolean).join(' ') }) + url, 'error')
  } catch (e) {
    toastError(e)
  } finally {
    testing.value = null
  }
}
</script>

<template>
  <div>
    <SHint class="mod-hint">{{ t('channels.hint') }}</SHint>
    <div class="mod-toolbar">
      <STableColumnSettings :state="columnState" />
      <SButton v-if="manage" variant="primary" size="sm" icon="plus" @click="openEdit(null)">{{ t('channels.add') }}</SButton>
    </div>
    <SAlert v-if="error" tone="danger" class="mod-block">{{ error }}</SAlert>

    <SCard :padded="false">
      <STable :columns="columnState.visibleColumns.value" :rows="rows" :loading="loading" :empty-text="t('channels.empty')" dense>
        <template #cell-name="{ row }">
          <span class="mod-cell-line">
            <span class="mod-ellipsis" :title="row.name">{{ row.name }}</span>
            <SHint inline size="xs" class="mod-num">#{{ row.id }}</SHint>
            <SBadge v-if="!row.has_key" size="xs" tone="warning">{{ t('channels.noKey') }}</SBadge>
          </span>
        </template>
        <template #cell-type="{ row }">
          <span :title="row.type === 'llm' && row.protocol ? enumLabel('protocol', row.protocol) : undefined">{{ enumLabel('channelType', row.type) }}</span>
        </template>
        <template #cell-protocol="{ row }">{{ row.type === 'llm' && row.protocol ? enumLabel('protocol', row.protocol) : '—' }}</template>
        <template #cell-base_url="{ row }">
          <!-- the scheme is left out to leave room for the host; the tooltip has the whole address -->
          <code class="mod-code mod-code-plain" :title="urlTitle(row)">{{ row.base_url.replace(/^https?:\/\//, '') }}</code>
        </template>
        <template #cell-key="{ row }">
          <code v-if="row.has_key" class="mod-code mod-code-plain">…{{ row.api_key_tail || '****' }}</code>
          <SHint v-else inline>—</SHint>
        </template>
        <template #cell-timeout="{ row }"><span class="mod-num">{{ ms(row.timeout_ms) }}</span></template>
        <template #cell-proxy="{ row }">{{ row.has_proxy ? t('channels.viaProxy') : '—' }}</template>
        <template #cell-model="{ row }">
          <span :title="row.billing_model ? `${row.model}\n${t('channels.pricedAs', { model: row.billing_model })}` : row.model">{{ row.model || '—' }}</span>
        </template>
        <template #cell-billing_model="{ row }">{{ row.billing_model || '—' }}</template>
        <template #cell-used_by="{ row }">
          <SChipList v-if="row.used_by?.length" :items="usedBy(row)" :max="1" />
          <SHint v-else inline>{{ t('channels.unused') }}</SHint>
        </template>
        <template #cell-hour="{ row }">
          <span v-if="row.stats" class="mod-cell-line mod-num" :title="t('stats.errorRate')">
            {{ t('channels.callsN', { n: num(row.stats.calls_1h) }) }} · <RateCell :rate="row.stats.error_rate_1h" :alert-percent="alertPercent" />
          </span>
          <SHint v-else inline>—</SHint>
        </template>
        <template #cell-today="{ row }">
          <span class="mod-cell-line">
            <span v-if="row.stats" class="mod-num" :title="todayTitle(row)">{{ money(row.stats.cost_today.cost) }}</span>
            <SHint v-else inline>—</SHint>
            <PriceCell :channel="row" problem-only />
          </span>
        </template>
        <template #cell-tokens="{ row }">
          <span v-if="row.stats" class="mod-num">{{ num(row.stats.tokens_today) }}</span>
          <SHint v-else inline>—</SHint>
        </template>
        <template #cell-price="{ row }"><PriceCell :channel="row" /></template>
        <template #cell-enabled="{ row }">
          <SSwitch v-if="manage" :model-value="row.enabled" :disabled="toggling === row.id" :aria-label="t('channels.col.enabled')" @update:model-value="setEnabled(row, $event)" />
          <EnabledBadge v-else :enabled="row.enabled" />
        </template>
        <template #cell-actions="{ row }">
          <span v-if="manage" class="mod-cell-line">
            <SRowAction :label="t('channels.test')" :loading="testing === row.id" @click="test(row)" />
            <SRowAction :label="t('edit')" @click="openEdit(row)" />
            <SDropdown :actions="moreActions" @select="remove(row)" />
          </span>
          <SRowAction v-else :label="t('view')" @click="openEdit(row)" />
        </template>
      </STable>
    </SCard>

    <ChannelEditModal v-model:open="editOpen" :channel="editing" :readonly="!manage" @saved="catalog.refresh(['channels', 'auditors'])" />
  </div>
</template>
