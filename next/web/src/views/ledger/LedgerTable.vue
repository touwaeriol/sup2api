<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SDateTime, SBadge, SHint, STable, type TableColumn } from '@sub2api/ui'
import type { LedgerEntry } from '@/api/types'
import { formatDelta, formatNumber } from '@/utils/format'
import { useOptionalT } from '@/composables/optionalText'
import { KIND_TONE } from './kinds'
import { displayPluginId } from '@/views/plugins/pluginId'

type Row = LedgerEntry & { user_name?: string; idempotency_key?: string }

const props = withDefaults(defineProps<{ rows: Row[]; loading?: boolean; showUser?: boolean }>(), { showUser: true })
const { t } = useI18n()
const tr = useOptionalT()

const columns = computed<TableColumn[]>(() => {
  // A phone card: the user (else the time) as its title, the kind as its status.
  const cols: TableColumn[] = [{ key: 'created_at', label: t('common.time'), width: '120px', mobile: props.showUser ? 'subtitle' : 'title' }]
  if (props.showUser) cols.push({ key: 'user', label: t('common.user'), mobile: 'title' })
  cols.push(
    { key: 'kind', label: t('ledger.cols.kind'), width: '130px', mobile: 'status' },
    { key: 'delta', label: t('ledger.cols.delta'), align: 'right', width: '130px', mobile: 'meta' },
    { key: 'balance_after', label: t('ledger.cols.balance'), align: 'right', width: '130px', mobile: 'meta' },
    { key: 'note', label: t('ledger.cols.note'), mobile: 'meta', mobileSpan: 2 }
  )
  return cols
})

function kindLabel(k: string) {
  return tr(`ledger.kinds.${k}`, undefined, k)
}

function deltaCls(v: string) {
  const n = Number(v)
  if (n > 0) return 'text-success-600 dark:text-success-400'
  return n < 0 ? 'text-danger-600 dark:text-danger-400' : ''
}
</script>

<template>
  <STable card :columns="columns" :rows="rows" :loading="loading" dense>
    <template v-if="$slots.toolbar" #toolbar><slot name="toolbar" /></template>
    <template v-if="$slots.footer" #footer><slot name="footer" /></template>
    <template #cell-created_at="{ row }">
      <SDateTime :value="row.created_at" mode="time" class="text-xs" />
    </template>
    <template #cell-user="{ row }">
      <span class="text-sm">{{ row.user_email || row.user_name || '#' + row.user_id }}</span>
    </template>
    <template #cell-kind="{ row }">
      <SBadge :tone="KIND_TONE[row.kind] ?? 'gray'">{{ kindLabel(row.kind) }}</SBadge>
    </template>
    <template #cell-delta="{ row }">
      <span class="font-mono text-sm" :class="deltaCls(row.delta)">{{ formatDelta(row.delta) }}</span>
    </template>
    <template #cell-balance_after="{ row }">
      <span class="font-mono text-sm">{{ formatNumber(row.balance_after, 4) }}</span>
    </template>
    <template #cell-note="{ row }">
      <div class="max-w-md text-sm">
        <span v-if="row.note">{{ row.note }}</span>
        <SHint v-if="row.ref_type || row.ref_id" inline size="xs" class="ml-1 truncate font-mono" :title="`${row.ref_type}:${row.ref_id}`">
          {{ row.ref_type }}:{{ row.ref_id && row.ref_id.length > 18 ? row.ref_id.slice(0, 18) + '…' : row.ref_id }}
        </SHint>
        <SHint v-if="row.plugin_key" inline size="xs" class="ml-1">· {{ displayPluginId(row.plugin_key) }}</SHint>
        <SHint v-if="row.operator_id" inline size="xs" class="ml-1">· {{ t('ledger.operator', { id: row.operator_id }) }}</SHint>
        <SHint v-if="!row.note && !row.ref_type && !row.ref_id" inline>—</SHint>
      </div>
    </template>
  </STable>
</template>
