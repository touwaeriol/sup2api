<script setup lang="ts">
import { computed, ref } from 'vue'
import { SButton, SDateRange, SDateTime, SFilterBar, SFilterChips, SInput, SPageHeader, SPagination, SRowAction, SSelect, STable, type TableColumn } from '@sub2api/ui'
import { paymentApi, usePaymentHost, type Order, type OrderStats as Stats } from '../host'
import { formatLocal, formatUsd } from '../format'
import OrderDetailDrawer from './OrderDetailDrawer.vue'
import OrderStats from './OrderStats.vue'
import PayStatusBadge from './PayStatusBadge.vue'
import { actionsColumn, columnOf, useAdminList } from './actions'
import { ORDER_RANGES, completeOrder, emptyOrderFilters, useOrderFilters, type OrderFilters } from './orders'
import { completable } from './status'

// Top-up orders (admin): totals, filters, the list, an order's detail and
// the manual completion of an order that was paid but not credited.
const host = usePaymentHost()
const t = host.t

const list = useAdminList<Order>('/admin/orders', emptyOrderFilters())
const filters = list.filters as OrderFilters
const bar = useOrderFilters(filters)

const stats = ref<Stats | null>(null)
const statsLoading = ref(false)
async function loadStats() {
  statsLoading.value = true
  try {
    stats.value = await paymentApi.orderStats()
  } catch {
    // The list shows the load error; the cards keep their last values.
  } finally {
    statsLoading.value = false
  }
}
loadStats()

function refresh() {
  list.reload()
  loadStats()
}

const columns = computed<TableColumn[]>(() => {
  const col = columnOf('admin.orders.fields')
  return [
    col('out_trade_no', { mobile: 'title' }),
    col('user', { grow: true, mobile: 'subtitle' }),
    col('amount', { align: 'right', mobile: 'meta' }),
    col('pay_amount', { align: 'right', mobile: 'meta' }),
    col('method', { mobile: 'meta' }),
    col('status', { mobile: 'status' }),
    col('created_at', { mobile: 'meta' }),
    col('completed_at', { mobile: 'meta' }),
    actionsColumn()
  ]
})

const detail = ref<Order | null>(null)
const detailOpen = ref(false)
function openDetail(o: Order) {
  detail.value = o
  detailOpen.value = true
}

async function complete(o: Order) {
  if (!(await completeOrder(o))) return
  detailOpen.value = false
  refresh()
}
</script>

<template>
  <div>
    <SPageHeader :title="t('admin.orders.title')">
      <template #actions>
        <SButton icon="refresh" :loading="list.loading.value" @click="refresh">{{ t('action.refresh') }}</SButton>
        <SButton icon="settings" :to="{ path: '/plugins/payment', query: { tab: 'settings' } }">{{ t('admin.orders.settings') }}</SButton>
      </template>
    </SPageHeader>

    <OrderStats :stats="stats" :loading="statsLoading" :status="filters.status" @filter="filters.status = $event" />

    <SFilterBar :aria-label="t('admin.filters')" :active-count="bar.activeCount.value" :advanced-count="bar.advancedCount.value" :dirty="bar.dirty.value" :on-reset="bar.reset">
      <template #primary>
        <SInput v-model="filters.q" type="search" size="sm" icon="search" clearable class="payment-search" :placeholder="t('admin.orders.search')" :aria-label="t('admin.orders.search')" />
      </template>
      <SSelect v-model="filters.status" size="sm" class="payment-filter" :options="bar.statusOptions.value" :aria-label="t('admin.orders.fields.status')" />
      <SDateRange v-model:range="bar.range.value" v-model:from="filters.from" v-model:to="filters.to" :keys="ORDER_RANGES" size="sm" :label="false" />
      <template #advanced>
        <SSelect v-model="filters.method" size="sm" class="payment-filter" :options="bar.methodOptions.value" :aria-label="t('admin.orders.fields.method')" />
      </template>
    </SFilterBar>
    <SFilterChips :items="bar.chips.value" class="payment-chips" @remove="filters.method = ''" @clear="filters.method = ''" />

    <STable
      card
      clickable
      class="payment-section"
      :columns="columns"
      :rows="list.items.value"
      :loading="list.loading.value"
      :error="list.error.value"
      :empty-text="t('admin.orders.empty')"
      @retry="list.reload"
      @row-click="openDetail"
    >
      <template #cell-out_trade_no="{ row }"><span class="payment-mono">{{ row.out_trade_no }}</span></template>
      <template #cell-user="{ row }">{{ row.user_email || `#${row.user_id}` }}</template>
      <template #cell-amount="{ row }">
        <span class="payment-num">{{ formatUsd(row.amount) }}</span>
        <span v-if="row.bonus" class="payment-bonus">{{ t('admin.orders.bonus', { amount: formatUsd(row.bonus) }) }}</span>
      </template>
      <template #cell-pay_amount="{ row }"><span class="payment-num">{{ formatLocal(row.pay_amount, row.currency) }}</span></template>
      <template #cell-method="{ row }">{{ row.method_name }}</template>
      <template #cell-status="{ row }"><PayStatusBadge kind="order" :status="row.status" /></template>
      <template #cell-created_at="{ row }"><SDateTime :value="row.created_at" mode="absolute" muted /></template>
      <template #cell-completed_at="{ row }"><SDateTime :value="row.completed_at" mode="absolute" muted /></template>
      <template #cell-actions="{ row }">
        <span class="payment-row-actions">
          <SRowAction :label="t('admin.orders.detail')" @click="openDetail(row)" />
          <SRowAction v-if="completable(row.status)" :label="t('admin.orders.complete')" tone="info" @click="complete(row)" />
        </span>
      </template>
      <template #footer>
        <SPagination v-model:page="list.page.value" v-model:page-size="list.pageSize.value" :total="list.total.value" />
      </template>
    </STable>

    <OrderDetailDrawer v-model:open="detailOpen" :order="detail" @complete="complete" />
  </div>
</template>

<style scoped src="./admin.css"></style>
