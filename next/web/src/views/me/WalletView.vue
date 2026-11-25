<script setup lang="ts">
// Mine > Wallet: the balance, what it went on lately and every balance change.
// Online top-up comes from a plugin on slot wallet.topup (the payment plugin,
// CONTRACTS §46); without one the hint says who adds funds.
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SAlert, SButton, SDateRange, SPageHeader, SSelect, SStatCard, rangeBounds, type RangeKey } from '@sub2api/ui'
import type { UsageOverview } from '@/api/types'
import { browserTZ, num } from '@/components/stats/stats'
import { formatMoney } from '@/utils/format'
import { LEDGER_RANGES, useKindOptions } from '@/views/ledger/kinds'
import MyLedgerTab from '@/views/ledger/MyLedgerTab.vue'
import PluginSlot from '@/components/plugin/PluginSlot.vue'
import { usePluginStore } from '@/stores/plugins'

const { t } = useI18n()
const plugins = usePluginStore()
const hasTopup = computed(() => plugins.slotEntries('wallet.topup').length > 0)
const range = ref<RangeKey>('month')
const filters = reactive({ ...rangeBounds('month'), kind: '' })
const kinds = useKindOptions()
const kindChoices = computed(() => kinds.value.map((x) => (x.value === '' ? { ...x, label: t('ledger.cols.kind') } : x)))

const overview = ref<UsageOverview | null>(null)
const overviewLoading = ref(false)
async function loadOverview() {
  overviewLoading.value = true
  try {
    overview.value = await api.get<UsageOverview>('/me/usage/overview', { tz: browserTZ() })
  } catch {
    overview.value = null
  } finally {
    overviewLoading.value = false
  }
}
onMounted(loadOverview)
const money = (v: unknown) => (overview.value ? formatMoney(num(v), 2) : '—')

const revision = ref(0)
const loading = ref(false)
function refresh() {
  revision.value++
  loadOverview()
}
</script>

<template>
  <div class="flex flex-col gap-4">
    <SPageHeader :title="t('me.wallet.title')" :description="t('me.wallet.description')" />
    <MyLedgerTab v-model:loading="loading" :filters="filters" :revision="revision">
      <template #stats>
        <SStatCard :label="t('me.wallet.today')" :value="money(overview?.today?.total_cost)" icon="usage" :loading="overviewLoading" data-testid="wallet-today" />
        <SStatCard :label="t('me.wallet.yesterday')" :value="money(overview?.yesterday?.total_cost)" icon="clock" :loading="overviewLoading" />
        <SStatCard :label="t('me.wallet.last30d')" :value="money(overview?.last_30d?.total_cost)" icon="chart" :loading="overviewLoading" />
      </template>
      <template #filters>
        <PluginSlot v-if="hasTopup" name="wallet.topup" :props="{ onCredited: refresh }" item-class="mb-4" />
        <SAlert v-else tone="info" dense class="mb-4" data-testid="wallet-topup-hint">{{ t('me.wallet.topupHint') }}</SAlert>
        <div class="mb-4 flex flex-wrap items-center gap-2 rounded-xl border border-line bg-surface p-3 shadow-sm">
          <SDateRange v-model:range="range" v-model:from="filters.from" v-model:to="filters.to" :keys="LEDGER_RANGES" size="sm" :label="false" />
          <SSelect v-model="filters.kind" class="w-40" size="sm" :aria-label="t('ledger.cols.kind')" :options="kindChoices" data-testid="wallet-kind" />
          <SButton class="ms-auto" size="sm" icon="refresh" :loading="loading" data-testid="wallet-refresh" @click="refresh">{{ t('common.refresh') }}</SButton>
        </div>
      </template>
    </MyLedgerTab>
  </div>
</template>
