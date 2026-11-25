<script setup lang="ts">
import { computed, watch } from 'vue'
import { useList } from '@sub2api/host'
import { SDateTime, SInput, SModal, SPagination, SRowAction, STable, type TableColumn } from '@sub2api/ui'
import { usePaymentHost, type Order } from '../host'
import { formatLocal, formatUsd } from '../format'
import MethodName from '../components/MethodName.vue'
import PayStatusBadge from '../components/PayStatusBadge.vue'
import { methodLook } from '../components/methodCatalog'
import type { CheckoutMethod } from './useTopupQuote'

// The caller's top-up orders (new-api's billing history): searchable by
// order number, paged; a pending order that has not expired can be paid
// from here ("continue paying" hands it to the parent). `methods` (the
// checkout's) give each order's method its mark.
const props = defineProps<{ open: boolean; methods: CheckoutMethod[] }>()
const emit = defineEmits<{ (e: 'update:open', v: boolean): void; (e: 'resume', order: Order): void }>()
const host = usePaymentHost()
const t = host.t

const list = useList<Order>(host.pluginApi, '/orders', { q: '' }, { immediate: false, pageSize: 10 })
watch(
  () => props.open,
  (open) => open && list.reload()
)

const resumable = (o: Order) => o.status === 'pending' && !!o.pay_url && Date.parse(o.expires_at) > Date.now()

const columns = computed<TableColumn[]>(() => [
  { key: 'out_trade_no', label: t('wallet.orders.no'), mobile: 'title', nowrap: true },
  { key: 'amount', label: t('wallet.orders.amount'), align: 'right', mobile: 'meta' },
  { key: 'pay_amount', label: t('wallet.orders.pay'), align: 'right', mobile: 'meta' },
  { key: 'method_name', label: t('wallet.orders.method'), mobile: 'meta' },
  { key: 'status', label: t('wallet.orders.status'), mobile: 'status' },
  { key: 'created_at', label: t('wallet.orders.created'), nowrap: true, mobile: 'subtitle' },
  ...(list.items.value.some(resumable) ? [{ key: 'actions', label: '', align: 'right' } as TableColumn] : [])
])
</script>

<template>
  <SModal :open="open" :title="t('wallet.orders.title')" width="2xl" @update:open="emit('update:open', $event)">
    <div class="pay-orders-toolbar">
      <SInput v-model="list.filters.q" type="search" icon="search" clearable :placeholder="t('wallet.orders.search')" :aria-label="t('wallet.orders.search')" data-testid="pay-orders-search" />
    </div>
    <STable :columns="columns" :rows="list.items.value" :loading="list.loading.value" :error="list.error.value" :empty-text="t('wallet.orders.empty')" row-key="out_trade_no" @retry="list.reload">
      <template #cell-out_trade_no="{ row }"><span class="pay-mono">{{ row.out_trade_no }}</span></template>
      <template #cell-amount="{ row }">
        {{ formatUsd(row.amount) }}<span v-if="row.bonus" class="pay-bonus"> +{{ formatUsd(row.bonus) }}</span>
      </template>
      <template #cell-pay_amount="{ row }">{{ formatLocal(row.pay_amount, row.currency) }}</template>
      <template #cell-method_name="{ row }">
        <MethodName :method="methodLook(methods, row.method, row.method_name)" />
      </template>
      <template #cell-status="{ row }"><PayStatusBadge kind="order" :status="row.status" /></template>
      <template #cell-created_at="{ row }"><SDateTime :value="row.created_at" mode="absolute" /></template>
      <template #cell-actions="{ row }">
        <span><SRowAction v-if="resumable(row)" :label="t('wallet.orders.resume')" data-testid="pay-resume" @click="emit('resume', row)" /></span>
      </template>
    </STable>
    <template #footer>
      <SPagination v-model:page="list.page.value" v-model:page-size="list.pageSize.value" :total="list.total.value" :page-sizes="[10, 20, 50]" />
    </template>
  </SModal>
</template>

<style scoped>
.pay-orders-toolbar {
  margin-bottom: 0.75rem;
  max-width: 20rem;
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
