<script setup lang="ts">
import { computed, ref } from 'vue'
import { SAlert, SButton, SDateTime, SPageHeader, SPagination, SProgress, SRowAction, STable, type TableColumn } from '@sub2api/ui'
import { paymentApi, usePaymentHost, type PromoCode } from '../host'
import { formatUsd } from '../format'
import PayStatusBadge from '../components/PayStatusBadge.vue'
import PromoEditModal from './PromoEditModal.vue'
import { actionsColumn, columnOf, confirmRun, runAction, useAdminList } from './actions'
import { promoState } from '../components/status'

// Promo codes (admin): a bonus credited on top of a paid top-up when the
// user enters the code at checkout.
const host = usePaymentHost()
const t = host.t

const list = useAdminList<PromoCode>('/promo-codes', {})

const columns = computed<TableColumn[]>(() => {
  const col = columnOf('admin.promo.fields')
  return [
    col('code', { mobile: 'title' }),
    col('bonus_amount', { align: 'right', mobile: 'meta' }),
    col('min_amount', { align: 'right', mobile: 'meta' }),
    col('usage', { mobile: 'meta' }),
    col('expires_at', { mobile: 'meta' }),
    col('status', { mobile: 'status' }),
    col('notes', { grow: true, mobile: 'subtitle' }),
    col('created_at', { mobile: 'detail' }),
    actionsColumn()
  ]
})

const usageText = (p: PromoCode) => `${host.i18n.formatNumber(p.used_count)} / ${p.max_uses ? host.i18n.formatNumber(p.max_uses) : t('admin.promo.unlimited')}`

const editOpen = ref(false)
const editing = ref<PromoCode | null>(null)
function openEdit(p: PromoCode | null) {
  editing.value = p
  editOpen.value = true
}

async function toggle(p: PromoCode) {
  const status = p.status === 'active' ? 'disabled' : 'active'
  if (await runAction(() => paymentApi.updatePromoCode(p.id, { status }), t(`admin.promo.toggled.${status}`))) list.reload()
}

async function remove(p: PromoCode) {
  const ok = await confirmRun(
    { title: t('admin.promo.deleteTitle'), message: t('admin.promo.deleteConfirm', { code: p.code }), confirmText: t('action.delete'), danger: true },
    () => paymentApi.deletePromoCode(p.id),
    t('admin.promo.deleted')
  )
  if (ok) list.reload()
}
</script>

<template>
  <div>
    <SPageHeader :title="t('admin.promo.title')">
      <template #actions>
        <SButton icon="refresh" :loading="list.loading.value" @click="list.reload">{{ t('action.refresh') }}</SButton>
        <SButton variant="primary" icon="plus" @click="openEdit(null)">{{ t('admin.promo.create') }}</SButton>
      </template>
    </SPageHeader>

    <SAlert tone="info" class="payment-intro">{{ t('admin.promo.intro') }}</SAlert>

    <STable card :columns="columns" :rows="list.items.value" :loading="list.loading.value" :error="list.error.value" :empty-text="t('admin.promo.empty')" @retry="list.reload">
      <template #cell-code="{ row }"><span class="payment-mono">{{ row.code }}</span></template>
      <template #cell-bonus_amount="{ row }"><span class="payment-num">{{ formatUsd(row.bonus_amount) }}</span></template>
      <template #cell-min_amount="{ row }">
        <span class="payment-num" :class="{ 'payment-muted': !row.min_amount }">{{ row.min_amount ? formatUsd(row.min_amount) : t('admin.promo.anyAmount') }}</span>
      </template>
      <template #cell-usage="{ row }">
        <div class="payment-usage">
          <SProgress v-if="row.max_uses" :value="row.used_count" :max="row.max_uses" :tone="row.used_count >= row.max_uses ? 'warning' : 'primary'">{{ usageText(row) }}</SProgress>
          <span v-else class="payment-usage-text">{{ usageText(row) }}</span>
        </div>
      </template>
      <template #cell-expires_at="{ row }"><SDateTime :value="row.expires_at" mode="absolute" muted :empty="t('admin.never')" /></template>
      <template #cell-status="{ row }"><PayStatusBadge kind="promo" :status="promoState(row)" /></template>
      <template #cell-notes="{ row }"><span :class="{ 'payment-muted': !row.notes }">{{ row.notes || '—' }}</span></template>
      <template #cell-created_at="{ row }"><SDateTime :value="row.created_at" mode="absolute" muted /></template>
      <template #cell-actions="{ row }">
        <span class="payment-row-actions">
          <SRowAction :label="t('action.edit')" @click="openEdit(row)" />
          <SRowAction :label="row.status === 'active' ? t('admin.promo.disable') : t('admin.promo.enable')" @click="toggle(row)" />
          <SRowAction :label="t('action.delete')" tone="danger" @click="remove(row)" />
        </span>
      </template>
      <template #footer>
        <SPagination v-model:page="list.page.value" v-model:page-size="list.pageSize.value" :total="list.total.value" />
      </template>
    </STable>

    <PromoEditModal v-model:open="editOpen" :promo="editing" @saved="list.reload" />
  </div>
</template>

<style scoped src="./admin.css"></style>
