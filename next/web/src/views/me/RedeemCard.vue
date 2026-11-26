<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SButton, SCard, SField, SInput, toast } from '@sub2api/ui'
import { errorMessage } from '@/utils/errors'
import { formatMoney } from '@/utils/format'

// Mine > Wallet: redeem a code into the balance (POST /me/redeem, core
// redeem codes). The server answers one message for a code that does not
// exist, is used, voided or expired. `main`: the wallet's only way to add
// funds (no top-up plugin) — a wide card with the form on one row and the
// #aside slot beside it; otherwise the narrow card next to the top-up card.
const props = defineProps<{ main?: boolean }>()
const emit = defineEmits<{ credited: [] }>()
const { t } = useI18n()

const title = computed(() => t(props.main ? 'redeem.card.mainTitle' : 'redeem.card.title'))
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
  <SCard :title="title" :subtitle="t('redeem.card.subtitle')">
    <div :class="main ? 'redeem-main' : ''">
      <form :class="main ? 'flex flex-col gap-2' : 'flex flex-col gap-3'" @submit.prevent="redeem">
        <span v-if="main" class="text-sm font-medium text-fg">{{ t('redeem.card.title') }}</span>
        <div :class="main ? 'flex gap-2 max-sm:flex-col' : 'contents'">
          <SField :error="error" class="min-w-0 flex-1">
            <SInput v-model="code" mono icon="key" :placeholder="t('redeem.card.placeholder')" :aria-label="t('redeem.card.title')" autocomplete="off" data-testid="redeem-code" @update:model-value="error = ''" />
          </SField>
          <SButton type="submit" :block="!main" :variant="main ? 'primary' : undefined" :loading="busy" data-testid="redeem-submit">{{ t('redeem.card.submit') }}</SButton>
        </div>
        <span v-if="main" class="text-xs text-fg-muted">{{ t('redeem.card.hint') }}</span>
      </form>
      <slot v-if="main" name="aside" />
    </div>
  </SCard>
</template>

<style scoped>
.redeem-main {
  display: grid;
  gap: 1.5rem;
}
@media (min-width: 1024px) {
  .redeem-main {
    grid-template-columns: minmax(0, 3fr) minmax(16rem, 2fr);
    align-items: start;
  }
}
</style>
