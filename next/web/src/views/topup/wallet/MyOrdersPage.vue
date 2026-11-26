<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { SButton, SDateTime, SInput, SPageHeader, SPagination, SRowAction, SSelect, STable, toast, type TableColumn } from '@sub2api/ui'
import { useList } from '@/composables/useList'
import { formatCurrency, formatMoney } from '@/utils/format'
import { notifyError } from '@/utils/errors'
import { TOPUP_PATHS, topupApi } from '../api'
import type { Order, PayingOrder } from '../topup'
import MethodName from '../components/MethodName.vue'
import PayStatusBadge from '../components/PayStatusBadge.vue'
import { useMethodCatalog } from '../components/methodCatalog'
import { orderStatusOptions } from '../components/status'
import PayingDialog from './PayingDialog.vue'
import { payWindowFor } from './payWindow'

// Mine > Top-up history: the caller's own orders, searchable by order number
// and filtered by status. A pending order can be paid from here while the
// server still gives it a payment page or QR code (only until it expires);
// the waiting dialog (which can also cancel it) polls it.
const { t } = useI18n()

const list = useList<Order>(TOPUP_PATHS.myOrders, { q: '', status: '' })
const catalog = useMethodCatalog()
const statusOptions = computed(() => orderStatusOptions(t('topup.wallet.orders.anyStatus')))

// The list carries no QR code: a pending order not yet expired may be paid,
// and where to pay is read from the order itself.
const resumable = (o: Order) => o.status === 'pending' && Date.parse(o.expires_at) > Date.now()

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
async function resume(row: Order) {
  // Reserved inside the click: a window opened after the read would be blocked.
  const win = payWindowFor(row.method)
  try {
    const order = await topupApi.order(row.out_trade_no, true)
    // Paid, cancelled or expired since the list was read: nowhere to pay, and
    // the method keeps its page (go would remember it as pageless).
    if (!resumable(order)) return gone(win)
    paying.value = order
    blocked.value = win.go(order.pay_url) === 'blocked'
    payingOpen.value = true
  } catch (e) {
    win.close()
    notifyError(e)
  }
}
function gone(win: { close(): void }) {
  win.close()
  toast(t('topup.wallet.orders.gone'), 'warning')
  list.reload()
}
// Paid, cancelled or still waiting: the list shows the order as it now is.
watch(payingOpen, open => !open && list.reload())
</script>

<template>
  <div>
    <SPageHeader :title="t('topup.wallet.orders.title')" :description="t('topup.wallet.orders.subtitle')" />

    <div class="mb-3 flex flex-wrap items-center gap-2">
      <SInput
        v-model="list.filters.q"
        type="search"
        size="sm"
        icon="search"
        clearable
        class="w-full sm:w-80"
        :placeholder="t('topup.wallet.orders.search')"
        :aria-label="t('topup.wallet.orders.search')"
        data-testid="pay-orders-search"
      />
      <SSelect
        v-model="list.filters.status"
        size="sm"
        class="w-36"
        :options="statusOptions"
        :aria-label="t('topup.wallet.orders.status')"
      />
      <span class="flex-1" />
      <SButton size="sm" icon="refresh" :loading="list.loading.value" @click="list.reload()">{{ t('common.refresh') }}</SButton>
      <SButton size="sm" variant="primary" icon="balance" to="/me/wallet" data-testid="pay-orders-topup">{{
        t('topup.wallet.orders.topup')
      }}</SButton>
    </div>

    <STable
      card
      :columns="columns"
      :rows="list.items.value"
      :loading="list.loading.value"
      :error="list.error.value"
      :empty-text="t('topup.wallet.orders.empty')"
      row-key="out_trade_no"
      @retry="list.reload"
    >
      <template #cell-out_trade_no="{ row }"
        ><span class="font-mono text-xs">{{ row.out_trade_no }}</span></template
      >
      <template #cell-amount="{ row }">
        {{ formatMoney(row.amount, 2)
        }}<span v-if="Number(row.bonus)" class="text-xs text-fg-muted"> +{{ formatMoney(row.bonus, 2) }}</span>
      </template>
      <template #cell-promo_code="{ row }">
        <span v-if="row.promo_code" class="font-mono text-xs">{{ row.promo_code }}</span
        ><span v-else class="text-xs text-fg-muted">—</span>
      </template>
      <template #cell-pay_amount="{ row }">{{ formatCurrency(row.pay_amount, row.currency) }}</template>
      <template #cell-method_name="{ row }"><MethodName :method="catalog.look(row.method, row.method_name)" /></template>
      <template #cell-status="{ row }"><PayStatusBadge kind="order" :status="row.status" /></template>
      <template #cell-created_at="{ row }"><SDateTime :value="row.created_at" mode="absolute" muted /></template>
      <template #cell-completed_at="{ row }"><SDateTime :value="row.completed_at" mode="absolute" muted /></template>
      <template #cell-actions="{ row }">
        <span
          ><SRowAction v-if="resumable(row)" :label="t('topup.wallet.orders.resume')" data-testid="pay-resume" @click="resume(row)"
        /></span>
      </template>
      <template #footer>
        <SPagination v-model:page="list.page.value" v-model:page-size="list.pageSize.value" :total="list.total.value" />
      </template>
    </STable>

    <PayingDialog v-model:open="payingOpen" :order="paying" :blocked="blocked" />
  </div>
</template>
