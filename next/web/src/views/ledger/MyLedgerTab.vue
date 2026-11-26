<script setup lang="ts">
import { watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { SAlert, SGrid, SPagination, SStatCard } from '@sub2api/ui'
import type { LedgerEntry } from '@/api/types'
import { useList } from '@/composables/useList'
import { formatMoney } from '@/utils/format'
import LedgerTable from './LedgerTable.vue'
import { useMyBalance } from './useMyBalance'

// "Balance changes" log type of Mine > Usage records: the caller's balance and
// their own ledger (GET /me/balance, GET /me/ledger) under `filters` (time range
// and kind, in the page's search bar). `revision` (bumped by the page's refresh
// button) reloads both; `loading` tells the button when to spin. A page can add
// figures beside the balance (#stats) and its filters above the list (#filters).
const props = withDefaults(defineProps<{ filters: { from: string; to: string; kind: string }; revision?: number }>(), { revision: 0 })
const emit = defineEmits<{ (e: 'update:loading', value: boolean): void }>()
const { t } = useI18n()
const { balance, loading: balanceLoading, negative, reload: reloadBalance } = useMyBalance()

const { items, loading, error, page, pageSize, total, filters, reload } = useList<LedgerEntry>('/me/ledger', { ...props.filters })
watch(() => props.filters, (v) => Object.assign(filters, v), { deep: true })

function reloadAll() {
  reloadBalance()
  reload()
}

watch(() => props.revision, reloadAll)
watch(loading, (v) => emit('update:loading', v), { immediate: true })
</script>

<template>
  <div>
    <SGrid :variant="$slots.stats ? 'stats' : undefined" :cols="1" :md-cols="$slots.stats ? 4 : 3" class="mb-4">
      <SStatCard
        :label="t('ledger.balance')"
        :value="formatMoney(balance)"
        icon="balance"
        :tone="negative ? 'danger' : 'primary'"
        :loading="balanceLoading"
      />
      <slot name="stats" />
      <SAlert v-if="negative" tone="danger" class="self-center md:col-span-2" data-testid="ledger-negative">{{ t('ledger.negativeHint') }}</SAlert>
    </SGrid>
    <slot name="filters" />

    <LedgerTable :rows="items" :loading="loading" :error="error" :show-user="false" @retry="reload">
      <template #footer><SPagination v-model:page="page" v-model:page-size="pageSize" :total="total" /></template>
    </LedgerTable>
  </div>
</template>
