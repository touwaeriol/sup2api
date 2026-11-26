<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SButton, SCard, SField, SInput, toast } from '@sub2api/ui'
import { errorMessage } from '@/utils/errors'
import { formatMoney } from '@/utils/format'

// Mine > Wallet: redeem a code into the balance (POST /me/redeem, core
// redeem codes). The server answers one message for a code that does not
// exist, is used, voided or expired.
const emit = defineEmits<{ credited: [] }>()
const { t } = useI18n()

const code = ref('')
const error = ref('')
const busy = ref(false)

async function redeem() {
  const value = code.value.trim()
  error.value = value ? '' : t('redeem.card.required')
  if (!value) return
  busy.value = true
  try {
    const res = await api.post<{ amount: string }>('/me/redeem', { code: value })
    toast(t('redeem.card.success', { amount: formatMoney(res.amount, 2) }), 'success')
    code.value = ''
    emit('credited')
  } catch (e) {
    error.value = errorMessage(e) || t('redeem.card.failed')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <SCard :title="t('redeem.card.title')" :subtitle="t('redeem.card.subtitle')">
    <form class="flex flex-col gap-3" @submit.prevent="redeem">
      <SField :error="error">
        <SInput v-model="code" mono icon="key" :placeholder="t('redeem.card.placeholder')" :aria-label="t('redeem.card.title')" autocomplete="off" data-testid="redeem-code" @update:model-value="error = ''" />
      </SField>
      <SButton type="submit" block :loading="busy" data-testid="redeem-submit">{{ t('redeem.card.submit') }}</SButton>
    </form>
  </SCard>
</template>
