<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { notifyError } from '@/utils/errors'
import { topupApi } from '../api'
import type { CheckoutInfo, CheckoutMethod, PayingOrder } from '../topup'
import ConfirmDialog from './ConfirmDialog.vue'
import PayingDialog from './PayingDialog.vue'
import TopupCard from './TopupCard.vue'
import { payWindowFor, type PayOpen } from './payWindow'

// Mine > Wallet: the top-up card and the flow behind "Pay now": confirm →
// order → wait for payment. `credited` tells the wallet money arrived (it
// reloads the balance).
const emit = defineEmits<{ credited: [] }>()

const info = ref<CheckoutInfo | null>(null)
const loading = ref(true)
onMounted(async () => {
  try {
    info.value = await topupApi.checkout()
  } catch (e) {
    notifyError(e)
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

async function placeOrder(promoCode: string) {
  const d = draft.value
  if (!d) return
  // Reserved inside the click: a window opened after the request would be blocked.
  const win = payWindowFor(d.method.type)
  submitting.value = true
  try {
    const created = await topupApi.createOrder({ amount: String(d.amount), method: d.method.type, promo_code: promoCode || undefined })
    const opened = win.go(created.pay_url)
    confirmOpen.value = false
    showPaying({ ...created, method_name: d.method.name }, opened)
  } catch (e) {
    win.close()
    notifyError(e)
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div>
    <TopupCard :info="info" :loading="loading" @pay="startPay" />

    <ConfirmDialog
      v-if="draft && info"
      v-model:open="confirmOpen"
      :amount="draft.amount"
      :method="draft.method"
      :currency="info.currency"
      :exponent="info.currency_exponent"
      :submitting="submitting"
      @confirm="placeOrder"
    />
    <PayingDialog v-model:open="payingOpen" :order="paying" :blocked="blocked" @credited="emit('credited')" />
  </div>
</template>
