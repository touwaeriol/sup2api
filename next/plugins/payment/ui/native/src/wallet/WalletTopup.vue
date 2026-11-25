<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { errorMessage, paymentApi, usePaymentHost, type CheckoutInfo, type Order, type OrderPreview } from '../host'
import ConfirmDialog from './ConfirmDialog.vue'
import OrdersDialog from './OrdersDialog.vue'
import PayingDialog from './PayingDialog.vue'
import RedeemCard from './RedeemCard.vue'
import TopupCard from './TopupCard.vue'
import { openPayPage, reservePayWindow } from './payWindow'
import type { PayingOrder } from './types'
import type { CheckoutMethod } from './useTopupQuote'

// Slot wallet.topup (Mine > Wallet): the top-up card and the redeem code
// card side by side (stacked on narrow screens), and the flow behind "Pay
// now": confirm → order → wait for payment. `onCredited` refreshes the
// wallet's balance and ledger after money arrives.
const props = defineProps<{ onCredited?: () => void }>()
const host = usePaymentHost()
const t = host.t

const info = ref<CheckoutInfo | null>(null)
const loading = ref(true)
onMounted(async () => {
  try {
    info.value = await paymentApi.checkout()
  } catch (e) {
    host.toast(errorMessage(e, t('loadFailed')), 'error')
  } finally {
    loading.value = false
  }
})

// confirm
const draft = ref<{ amount: number; method: CheckoutMethod } | null>(null)
const confirmOpen = ref(false)
const submitting = ref(false)
function startPay(amount: number, method: CheckoutMethod) {
  draft.value = { amount, method }
  confirmOpen.value = true
}

// waiting
const paying = ref<PayingOrder | null>(null)
const payingOpen = ref(false)
const blocked = ref(false)
function showPaying(order: PayingOrder, opened: boolean) {
  paying.value = order
  blocked.value = !!order.pay_url && !opened
  payingOpen.value = true
}

async function placeOrder(promoCode: string, preview: OrderPreview) {
  const d = draft.value
  if (!d || !info.value) return
  const win = reservePayWindow()
  submitting.value = true
  try {
    const created = await paymentApi.createOrder({ amount: d.amount, method: d.method.type, promo_code: promoCode || undefined })
    const opened = !!created.pay_url && win.go(created.pay_url)
    if (!opened) win.close()
    confirmOpen.value = false
    const { amount, bonus, pay_amount } = preview
    showPaying({ ...created, amount, bonus, pay_amount, currency: info.value.currency, method_name: d.method.name }, opened)
  } catch (e) {
    win.close()
    host.toast(errorMessage(e, t('wallet.orderFailed')), 'error')
  } finally {
    submitting.value = false
  }
}

// history
const ordersOpen = ref(false)
function resume(order: Order) {
  const opened = !!order.pay_url && openPayPage(order.pay_url)
  ordersOpen.value = false
  showPaying(order, opened)
}
</script>

<template>
  <div class="pay-wallet">
    <TopupCard :info="info" :loading="loading" @pay="startPay" @history="ordersOpen = true" />
    <RedeemCard @credited="props.onCredited?.()" />

    <ConfirmDialog v-if="draft && info" v-model:open="confirmOpen" :amount="draft.amount" :method="draft.method" :currency="info.currency" :submitting="submitting" @confirm="placeOrder" />
    <PayingDialog v-model:open="payingOpen" :order="paying" :blocked="blocked" @credited="props.onCredited?.()" />
    <OrdersDialog v-model:open="ordersOpen" @resume="resume" />
  </div>
</template>

<style scoped>
.pay-wallet {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  align-items: start;
  gap: 1rem;
}
@media (min-width: 1024px) {
  .pay-wallet {
    grid-template-columns: minmax(0, 2fr) minmax(18rem, 1fr);
  }
}
</style>
