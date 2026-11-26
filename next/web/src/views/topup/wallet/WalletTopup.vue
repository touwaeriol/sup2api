<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { errorMessage, paymentApi, useTopup, type CheckoutInfo } from '../topup'
import ConfirmDialog from './ConfirmDialog.vue'
import PayingDialog from './PayingDialog.vue'
import TopupCard from './TopupCard.vue'
import { reservePayWindow, type PayOpen, type PayWindow } from './payWindow'
import type { PayingOrder } from './types'
import type { CheckoutMethod } from './useTopupQuote'

// Mine > Wallet: the top-up card and the flow behind "Pay now": confirm →
// order → wait for payment. `credited` tells the wallet money arrived (it
// reloads the balance).
const emit = defineEmits<{ credited: [] }>()
const host = useTopup()
const t = host.t

const info = ref<CheckoutInfo | null>(null)
const loading = ref(true)
onMounted(async () => {
  try {
    info.value = await paymentApi.checkout()
  } catch (e) {
    host.toast(errorMessage(e, t('topup.loadFailed')), 'error')
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
function showPaying(order: PayingOrder, opened: PayOpen | null) {
  paying.value = order
  blocked.value = opened === 'blocked'
  payingOpen.value = true
}

/** Points the reserved window at the order's payment page, or closes it (no page, or not https). */
function openReserved(win: PayWindow, payUrl: string | undefined): PayOpen | null {
  const opened = payUrl ? win.go(payUrl) : null
  if (opened !== 'opened') win.close()
  if (opened === 'refused') host.toast(t('topup.wallet.paying.badUrl'), 'error')
  return opened
}

async function placeOrder(promoCode: string) {
  const d = draft.value
  if (!d || !info.value) return
  const win = reservePayWindow()
  submitting.value = true
  try {
    const created = await paymentApi.createOrder({ amount: d.amount, method: d.method.type, promo_code: promoCode || undefined })
    const opened = openReserved(win, created.pay_url)
    confirmOpen.value = false
    showPaying({ ...created, method_name: d.method.name }, opened)
  } catch (e) {
    win.close()
    host.toast(errorMessage(e, t('topup.wallet.orderFailed')), 'error')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div>
    <TopupCard :info="info" :loading="loading" @pay="startPay" />

    <ConfirmDialog v-if="draft && info" v-model:open="confirmOpen" :amount="draft.amount" :method="draft.method" :currency="info.currency" :submitting="submitting" @confirm="placeOrder" />
    <PayingDialog v-model:open="payingOpen" :order="paying" :blocked="blocked" @credited="emit('credited')" />
  </div>
</template>

