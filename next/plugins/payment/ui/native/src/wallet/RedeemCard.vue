<script setup lang="ts">
import { ref } from 'vue'
import { SButton, SCard, SField, SInput } from '@sub2api/ui'
import { errorMessage, paymentApi, usePaymentHost } from '../host'
import { balance } from './money'

// Redeem code card: the code's value goes straight to the balance.
const emit = defineEmits<{ (e: 'credited'): void }>()
const host = usePaymentHost()
const t = host.t

const code = ref('')
const error = ref('')
const busy = ref(false)

async function redeem() {
  const value = code.value.trim()
  error.value = value ? '' : t('wallet.redeem.required')
  if (!value) return
  busy.value = true
  try {
    const res = await paymentApi.redeem(value)
    host.toast(t('wallet.redeem.success', { amount: balance(res.amount) }), 'success')
    code.value = ''
    emit('credited')
  } catch (e) {
    error.value = errorMessage(e, t('wallet.redeem.failed'))
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <SCard :title="t('wallet.redeem.title')" :subtitle="t('wallet.redeem.subtitle')">
    <form class="pay-redeem" @submit.prevent="redeem">
      <SField :error="error">
        <SInput v-model="code" mono icon="key" :placeholder="t('wallet.redeem.placeholder')" :aria-label="t('wallet.redeem.title')" autocomplete="off" data-testid="pay-redeem-code" @update:model-value="error = ''" />
      </SField>
      <SButton type="submit" block :loading="busy" data-testid="pay-redeem">{{ t('wallet.redeem.submit') }}</SButton>
    </form>
  </SCard>
</template>

<style scoped>
.pay-redeem {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}
</style>
