<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SButton, SDateTime, SFilterBar, SPageHeader, SPagination, SRowAction, SSelect, SStatusBadge, STable, confirm, toast, type TableColumn } from '@sub2api/ui'
import { useList } from '@/composables/useList'
import { notifyError } from '@/utils/errors'
import { formatMoney } from '@/utils/format'
import RedeemGenerateModal from './RedeemGenerateModal.vue'
import RedeemResultModal from './RedeemResultModal.vue'
import { REDEEM_DIGITS, REDEEM_STATUSES, REDEEM_TONES, maskCode, type RedeemCode } from './redeem'

// Top-up & payment > Redeem codes (core, redeem:manage): generated in batches, listed
// masked, the unused ones can be voided. Users redeem them in their wallet.
const { t } = useI18n()

const list = useList<RedeemCode>('/redeem-codes', { status: '' })
const statusOptions = computed(() => [{ value: '', label: t('redeem.anyStatus') }, ...REDEEM_STATUSES.map(s => ({ value: s, label: t(`redeem.status.${s}`) }))])

const COLS = ['code', 'value', 'status', 'used_by', 'used_at', 'expires_at', 'notes', 'created_at'] as const
const MOBILE: Partial<Record<(typeof COLS)[number], TableColumn['mobile']>> = { code: 'title', status: 'status', notes: 'subtitle', created_at: 'detail' }
const columns = computed<TableColumn[]>(() => [
  ...COLS.map(key => ({ key, label: t(`redeem.cols.${key}`), nowrap: key !== 'notes', grow: key === 'notes', align: key === 'value' ? ('right' as const) : undefined, mobile: MOBILE[key] ?? 'meta' })),
  { key: 'actions', label: t('common.actions'), align: 'right', nowrap: true }
])

const generateOpen = ref(false)
const codes = ref<string[]>([])
const resultOpen = computed({ get: () => codes.value.length > 0, set: open => !open && (codes.value = []) })

function onGenerated(next: string[]) {
  codes.value = next
  list.reload()
}

async function voidCode(c: RedeemCode) {
  const ok = await confirm({ title: t('redeem.voidTitle'), message: t('redeem.voidConfirm', { code: maskCode(c.code_tail), value: formatMoney(c.value, REDEEM_DIGITS) }), confirmText: t('redeem.void'), danger: true })
  if (!ok) return
  try {
    await api.del(`/redeem-codes/${c.id}`)
    toast(t('redeem.voided'), 'success')
    list.reload()
  } catch (e) {
    notifyError(e)
  }
}
</script>

<template>
  <div>
    <SPageHeader :title="t('redeem.title')" :description="t('redeem.description')" />

    <SFilterBar :aria-label="t('redeem.cols.status')" :active-count="list.filters.status ? 1 : 0" :dirty="!!list.filters.status" :on-reset="() => (list.filters.status = '')">
      <SSelect v-model="list.filters.status" size="sm" class="w-36" :options="statusOptions" :aria-label="t('redeem.cols.status')" />
      <template #end>
        <SButton size="sm" icon="refresh" :loading="list.loading.value" @click="list.reload">{{ t('common.refresh') }}</SButton>
        <SButton v-permission="'redeem:manage'" size="sm" variant="primary" icon="plus" data-testid="redeem-generate" @click="generateOpen = true">{{ t('redeem.generate') }}</SButton>
      </template>
    </SFilterBar>

    <STable card class="mt-3" :columns="columns" :rows="list.items.value" :loading="list.loading.value" :error="list.error.value" :empty-text="t('redeem.empty')" @retry="list.reload">
      <template #cell-code="{ row }"><span class="font-mono text-xs">{{ maskCode(row.code_tail) }}</span></template>
      <template #cell-value="{ row }"><span class="tabular-nums">{{ formatMoney(row.value, REDEEM_DIGITS) }}</span></template>
      <template #cell-status="{ row }"><SStatusBadge :status="row.status" :label="t(`redeem.status.${row.status}`)" :tones="REDEEM_TONES" size="xs" /></template>
      <template #cell-used_by="{ row }">{{ row.used_by_email || (row.used_by ? `#${row.used_by}` : '—') }}</template>
      <template #cell-used_at="{ row }"><SDateTime :value="row.used_at" mode="absolute" muted /></template>
      <template #cell-expires_at="{ row }"><SDateTime :value="row.expires_at" mode="absolute" muted :empty="t('redeem.never')" /></template>
      <template #cell-notes="{ row }"><span :class="{ 'text-fg-muted': !row.notes }">{{ row.notes || '—' }}</span></template>
      <template #cell-created_at="{ row }"><SDateTime :value="row.created_at" mode="absolute" muted /></template>
      <template #cell-actions="{ row }">
        <SRowAction v-if="row.status === 'unused'" :label="t('redeem.void')" tone="danger" @click="voidCode(row)" />
      </template>
      <template #footer>
        <SPagination v-model:page="list.page.value" v-model:page-size="list.pageSize.value" :total="list.total.value" />
      </template>
    </STable>

    <RedeemGenerateModal v-model:open="generateOpen" @generated="onGenerated" />
    <RedeemResultModal v-model:open="resultOpen" :codes="codes" />
  </div>
</template>
