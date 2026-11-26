<script setup lang="ts">
import { computed, ref } from 'vue'
import { useList } from '@sub2api/host'
import { SButton, SDateTime, SInput, SPageHeader, SPagination, SRowAction, SSelect, STable, type TableColumn } from '@sub2api/ui'
import { useTopup, type Order } from '../topup'
import { formatLocal, formatUsd } from '../format'
import MethodName from '../components/MethodName.vue'
import PayStatusBadge from '../components/PayStatusBadge.vue'
import { useMethodCatalog } from '../components/methodCatalog'
import { ORDER_STATUSES } from '../components/status'
import PayingDialog from './PayingDialog.vue'
import { isPayUrl, openPayPage } from './payWindow'
import type { PayingOrder } from './types'

// Mine > Top-up history (new-api's billing history): the caller's own
// orders, searchable by order number and filtered by status. A pending order
// that has not expired can be paid from here: the payment page opens again
// (or its QR code shows) and the waiting dialog (which can also cancel it)
// polls it.
const host = useTopup()
const t = host.t

const list = useList<Order>(host.api, '/me/topup/orders', { q: '', status: '' }, { pageSize: 20 })
const catalog = useMethodCatalog()
const statusOptions = computed(() => [{ value: '', label: t('topup.wallet.orders.anyStatus') }, ...ORDER_STATUSES.map((s) => ({ value: s, label: t(`topup.status.${s}`) }))])

/** A pending order not yet expired with somewhere to pay: an https payment page or a QR code. */
const resumable = (o: Order) => o.status === 'pending' && (isPayUrl(o.pay_url) || !!o.qr_code) && Date.parse(o.expires_at) > Date.now()

const columns = computed<TableColumn[]>(() => [
  { key: 'out_trade_no', label: t('topup.wallet.orders.no'), mobile: 'title', nowrap: true },
  { key: 'amount', label: t('topup.wallet.orders.amount'), align: 'right', mobile: 'meta' },
  { key: 'pay_amount', label: t('topup.wallet.orders.pay'), align: 'right', mobile: 'meta' },
  { key: 'promo_code', label: t('topup.wallet.orders.promo'), mobile: 'meta' },
  { key: 'method_name', label: t('topup.wallet.orders.method'), mobile: 'meta' },
  { key: 'status', label: t('topup.wallet.orders.status'), mobile: 'status' },
  { key: 'created_at', label: t('topup.wallet.orders.created'), nowrap: true, mobile: 'subtitle' },
  { key: 'completed_at', label: t('topup.wallet.orders.completed'), nowrap: true, mobile: 'meta' },
  { key: 'actions', label: '', align: 'right', nowrap: true }
])

const paying = ref<PayingOrder | null>(null)
const payingOpen = ref(false)
const blocked = ref(false)
function resume(order: Order) {
  const opened = isPayUrl(order.pay_url) ? openPayPage(order.pay_url) : null
  paying.value = order
  blocked.value = opened === 'blocked'
  payingOpen.value = true
}
</script>

<template>
  <div>
    <SPageHeader :title="t('topup.wallet.orders.title')" :description="t('topup.wallet.orders.subtitle')" />

    <div class="pay-orders-toolbar">
      <SInput v-model="list.filters.q" type="search" size="sm" icon="search" clearable class="pay-orders-search" :placeholder="t('topup.wallet.orders.search')" :aria-label="t('topup.wallet.orders.search')" data-testid="pay-orders-search" />
      <SSelect v-model="list.filters.status" size="sm" class="pay-orders-filter" :options="statusOptions" :aria-label="t('topup.wallet.orders.status')" />
      <span class="pay-orders-spacer" />
      <SButton size="sm" icon="refresh" :loading="list.loading.value" @click="list.reload()">{{ t('topup.action.refresh') }}</SButton>
      <SButton size="sm" variant="primary" icon="balance" to="/me/wallet" data-testid="pay-orders-topup">{{ t('topup.wallet.orders.topup') }}</SButton>
    </div>

    <STable card :columns="columns" :rows="list.items.value" :loading="list.loading.value" :error="list.error.value" :empty-text="t('topup.wallet.orders.empty')" row-key="out_trade_no" @retry="list.reload">
      <template #cell-out_trade_no="{ row }"><span class="pay-mono">{{ row.out_trade_no }}</span></template>
      <template #cell-amount="{ row }">
        {{ formatUsd(row.amount) }}<span v-if="row.bonus" class="pay-bonus"> +{{ formatUsd(row.bonus) }}</span>
      </template>
      <template #cell-promo_code="{ row }">
        <span v-if="row.promo_code" class="pay-mono">{{ row.promo_code }}</span><span v-else class="pay-bonus">—</span>
      </template>
      <template #cell-pay_amount="{ row }">{{ formatLocal(row.pay_amount, row.currency) }}</template>
      <template #cell-method_name="{ row }"><MethodName :method="catalog.look(row.method, row.method_name)" /></template>
      <template #cell-status="{ row }"><PayStatusBadge kind="order" :status="row.status" /></template>
      <template #cell-created_at="{ row }"><SDateTime :value="row.created_at" mode="absolute" muted /></template>
      <template #cell-completed_at="{ row }"><SDateTime :value="row.completed_at" mode="absolute" muted /></template>
      <template #cell-actions="{ row }">
        <span><SRowAction v-if="resumable(row)" :label="t('topup.wallet.orders.resume')" data-testid="pay-resume" @click="resume(row)" /></span>
      </template>
      <template #footer>
        <SPagination v-model:page="list.page.value" v-model:page-size="list.pageSize.value" :total="list.total.value" />
      </template>
    </STable>

    <PayingDialog v-model:open="payingOpen" :order="paying" :blocked="blocked" @credited="list.reload()" />
  </div>
</template>

<style scoped>
.pay-orders-toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  margin-bottom: 0.75rem;
}
.pay-orders-search {
  width: min(20rem, 100%);
}
.pay-orders-filter {
  width: 9rem;
}
.pay-orders-spacer {
  flex: 1;
}
.pay-mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.75rem;
}
.pay-bonus {
  font-size: 0.75rem;
  color: rgb(var(--fg-muted));
}
</style>
