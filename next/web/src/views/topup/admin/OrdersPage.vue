<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  SButton,
  SDateRange,
  SDateTime,
  SFilterBar,
  SFilterChips,
  SInput,
  SPageHeader,
  SPagination,
  SSelect,
  STable,
  type TableColumn
} from '@sub2api/ui'
import { useList } from '@/composables/useList'
import { formatCurrency, formatMoney } from '@/utils/format'
import { TOPUP_PATHS, topupApi } from '../api'
import type { Order, OrderStats as Stats } from '../topup'
import MethodName from '../components/MethodName.vue'
import OrderDetailDrawer from './OrderDetailDrawer.vue'
import OrderStats from './OrderStats.vue'
import PayStatusBadge from '../components/PayStatusBadge.vue'
import { columnOf } from './columns'
import { ORDER_RANGES, emptyOrderFilters, useOrderFilters, type OrderFilters } from './orders'

// Top-up orders (admin, read-only): totals, filters, the list and an order's
// detail for reconciling with the gateway.
const { t } = useI18n()

const list = useList<Order>(TOPUP_PATHS.orders, emptyOrderFilters())
const filters = list.filters as OrderFilters
const bar = useOrderFilters(filters, () => list.items.value)

const stats = ref<Stats | null>(null)
const statsLoading = ref(false)
async function loadStats() {
  statsLoading.value = true
  try {
    stats.value = await topupApi.orderStats()
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
    col('completed_at', { mobile: 'meta' })
  ]
})

const detail = ref<Order | null>(null)
const detailOpen = ref(false)
function openDetail(o: Order) {
  detail.value = o
  detailOpen.value = true
}
</script>

<template>
  <div>
    <SPageHeader :title="t('topup.admin.orders.title')">
      <template #actions>
        <SButton icon="refresh" :loading="list.loading.value" @click="refresh">{{ t('common.refresh') }}</SButton>
      </template>
    </SPageHeader>

    <OrderStats :stats="stats" :loading="statsLoading" :status="filters.status" @filter="filters.status = $event" />

    <SFilterBar
      :aria-label="t('topup.admin.filters')"
      :active-count="bar.activeCount.value"
      :advanced-count="bar.advancedCount.value"
      :dirty="bar.dirty.value"
      :on-reset="bar.reset"
    >
      <template #primary>
        <SInput
          v-model="filters.q"
          type="search"
          size="sm"
          icon="search"
          clearable
          class="w-full md:w-72"
          :placeholder="t('topup.admin.orders.search')"
          :aria-label="t('topup.admin.orders.search')"
        />
      </template>
      <SSelect
        v-model="filters.status"
        size="sm"
        class="w-full md:w-36"
        :options="bar.statusOptions.value"
        :aria-label="t('topup.admin.orders.fields.status')"
      />
      <SDateRange
        v-model:range="bar.range.value"
        v-model:from="filters.from"
        v-model:to="filters.to"
        :keys="ORDER_RANGES"
        size="sm"
        :label="false"
      />
      <template #advanced>
        <SSelect
          v-model="filters.method"
          size="sm"
          class="w-full md:w-36"
          :options="bar.methodOptions.value"
          :aria-label="t('topup.admin.orders.fields.method')"
        />
      </template>
    </SFilterBar>
    <SFilterChips :items="bar.chips.value" class="mt-2" @remove="filters.method = ''" @clear="filters.method = ''" />

    <STable
      card
      clickable
      class="mt-3"
      :columns="columns"
      :rows="list.items.value"
      :loading="list.loading.value"
      :error="list.error.value"
      :empty-text="t('topup.admin.orders.empty')"
      @retry="list.reload"
      @row-click="openDetail"
    >
      <template #cell-out_trade_no="{ row }"
        ><span class="font-mono text-[0.8125rem]">{{ row.out_trade_no }}</span></template
      >
      <template #cell-user="{ row }">{{ row.user_email || `#${row.user_id}` }}</template>
      <template #cell-amount="{ row }">
        <span class="tabular-nums">{{ formatMoney(row.amount, 2) }}</span>
        <span v-if="Number(row.bonus)" class="ml-1.5 text-xs text-fg-muted">{{
          t('topup.admin.orders.bonus', { amount: formatMoney(row.bonus, 2) })
        }}</span>
      </template>
      <template #cell-pay_amount="{ row }"
        ><span class="tabular-nums">{{ formatCurrency(row.pay_amount, row.currency) }}</span></template
      >
      <template #cell-method="{ row }"><MethodName :method="{ name: row.method_name || row.method }" /></template>
      <template #cell-status="{ row }"><PayStatusBadge kind="order" :status="row.status" /></template>
      <template #cell-created_at="{ row }"><SDateTime :value="row.created_at" mode="absolute" muted /></template>
      <template #cell-completed_at="{ row }"><SDateTime :value="row.completed_at" mode="absolute" muted /></template>
      <template #footer>
        <SPagination v-model:page="list.page.value" v-model:page-size="list.pageSize.value" :total="list.total.value" />
      </template>
    </STable>

    <OrderDetailDrawer v-model:open="detailOpen" :order="detail" />
  </div>
</template>
