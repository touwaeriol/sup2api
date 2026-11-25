<script setup lang="ts">
import { computed, ref } from 'vue'
import { SButton, SDateTime, SFilterBar, SPageHeader, SPagination, SRowAction, SSelect, STable, type TableColumn } from '@sub2api/ui'
import { paymentApi, usePaymentHost, type RedeemCode } from '../host'
import { formatUsd } from '../format'
import PayStatusBadge from './PayStatusBadge.vue'
import RedeemGenerateModal from './RedeemGenerateModal.vue'
import RedeemResultModal from './RedeemResultModal.vue'
import { actionsColumn, columnOf, confirmRun, useAdminList } from './actions'
import { REDEEM_STATUSES } from './status'

// Redeem codes (admin): generated in batches, listed masked, the unused ones
// can be voided.
const host = usePaymentHost()
const t = host.t

const list = useAdminList<RedeemCode>('/redeem-codes', { status: '' })
const statusOptions = computed(() => [
  { value: '', label: t('admin.anyOf', { name: t('admin.redeem.fields.status') }) },
  ...REDEEM_STATUSES.map((s) => ({ value: s, label: t(`admin.redeem.status.${s}`) }))
])

const columns = computed<TableColumn[]>(() => {
  const col = columnOf('admin.redeem.fields')
  return [
    col('code', { mobile: 'title' }),
    col('value', { align: 'right', mobile: 'meta' }),
    col('status', { mobile: 'status' }),
    col('used_by', { mobile: 'meta' }),
    col('used_at', { mobile: 'meta' }),
    col('expires_at', { mobile: 'meta' }),
    col('notes', { grow: true, mobile: 'subtitle' }),
    col('created_at', { mobile: 'detail' }),
    actionsColumn()
  ]
})

const generateOpen = ref(false)
const resultOpen = ref(false)
const codes = ref<string[]>([])

function onGenerated(next: string[]) {
  codes.value = next
  resultOpen.value = true
  list.reload()
}

/** Closing the result drops the plain codes. */
function setResultOpen(open: boolean) {
  resultOpen.value = open
  if (!open) codes.value = []
}

async function voidCode(c: RedeemCode) {
  const ok = await confirmRun(
    { title: t('admin.redeem.voidTitle'), message: t('admin.redeem.voidConfirm', { code: c.code, value: formatUsd(c.value) }), confirmText: t('admin.redeem.void'), danger: true },
    () => paymentApi.voidRedeemCode(c.id),
    t('admin.redeem.voided')
  )
  if (ok) list.reload()
}
</script>

<template>
  <div>
    <SPageHeader :title="t('admin.redeem.title')">
      <template #actions>
        <SButton icon="refresh" :loading="list.loading.value" @click="list.reload">{{ t('action.refresh') }}</SButton>
        <SButton variant="primary" icon="plus" @click="generateOpen = true">{{ t('admin.redeem.generate') }}</SButton>
      </template>
    </SPageHeader>

    <SFilterBar :aria-label="t('admin.filters')" :active-count="list.filters.status ? 1 : 0" :dirty="!!list.filters.status" :on-reset="() => (list.filters.status = '')">
      <SSelect v-model="list.filters.status" size="sm" class="payment-filter" :options="statusOptions" :aria-label="t('admin.redeem.fields.status')" />
    </SFilterBar>

    <STable card class="payment-section" :columns="columns" :rows="list.items.value" :loading="list.loading.value" :error="list.error.value" :empty-text="t('admin.redeem.empty')" @retry="list.reload">
      <template #cell-code="{ row }"><span class="payment-mono">{{ row.code }}</span></template>
      <template #cell-value="{ row }"><span class="payment-num">{{ formatUsd(row.value) }}</span></template>
      <template #cell-status="{ row }"><PayStatusBadge kind="redeem" :status="row.status" /></template>
      <template #cell-used_by="{ row }">{{ row.used_by_email || (row.used_by ? `#${row.used_by}` : '—') }}</template>
      <template #cell-used_at="{ row }"><SDateTime :value="row.used_at" mode="absolute" muted /></template>
      <template #cell-expires_at="{ row }"><SDateTime :value="row.expires_at" mode="absolute" muted :empty="t('admin.never')" /></template>
      <template #cell-notes="{ row }"><span :class="{ 'payment-muted': !row.notes }">{{ row.notes || '—' }}</span></template>
      <template #cell-created_at="{ row }"><SDateTime :value="row.created_at" mode="absolute" muted /></template>
      <template #cell-actions="{ row }">
        <SRowAction v-if="row.status === 'unused'" :label="t('admin.redeem.void')" tone="danger" @click="voidCode(row)" />
      </template>
      <template #footer>
        <SPagination v-model:page="list.page.value" v-model:page-size="list.pageSize.value" :total="list.total.value" />
      </template>
    </STable>

    <RedeemGenerateModal v-model:open="generateOpen" @generated="onGenerated" />
    <RedeemResultModal :open="resultOpen" :codes="codes" @update:open="setResultOpen" />
  </div>
</template>

<style scoped src="./admin.css"></style>
