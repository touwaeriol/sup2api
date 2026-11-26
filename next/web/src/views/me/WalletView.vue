<script setup lang="ts">
// Mine > Wallet: the balance, what it went on lately and how to add funds:
// online top-up from a plugin on slot wallet.topup (the payment plugin,
// CONTRACTS §46) with the core's redeem card beside it; without a plugin the
// redeem card is the page's top-up card and says who else adds funds. Every balance change is under Usage logs (its
// balance changes log type), not here.
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SAlert, SGrid, SIcon, SPageHeader, SStatCard } from '@sub2api/ui'
import type { UsageOverview } from '@/api/types'
import { browserTZ, num } from '@/components/stats/stats'
import { formatMoney } from '@/utils/format'
import { useMyBalance } from '@/views/ledger/useMyBalance'
import PluginSlot from '@/components/plugin/PluginSlot.vue'
import { useAuthStore } from '@/stores/auth'
import RedeemCard from './RedeemCard.vue'
import { usePluginStore } from '@/stores/plugins'

const { t } = useI18n()
const plugins = usePluginStore()
const hasTopup = computed(() => plugins.slotEntries('wallet.topup').length > 0)
const wallet = useMyBalance()
const auth = useAuthStore()
const canRedeem = computed(() => auth.has('redeem:use'))

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

/** After a top-up or a redeemed code: the new balance (the spending figures do not move). */
const refresh = () => wallet.reload()
</script>

<template>
  <div class="flex flex-col gap-4">
    <SPageHeader :title="t('me.wallet.title')" :description="t('me.wallet.description')" />
    <SGrid variant="stats" :cols="1" :md-cols="4">
      <SStatCard
        :label="t('ledger.balance')"
        :value="formatMoney(wallet.balance.value)"
        icon="balance"
        :tone="wallet.negative.value ? 'danger' : 'primary'"
        :loading="wallet.loading.value"
        data-testid="wallet-balance"
      />
      <SStatCard :label="t('me.wallet.today')" :value="money(overview?.today?.total_cost)" icon="usage" :loading="overviewLoading" data-testid="wallet-today" />
      <SStatCard :label="t('me.wallet.yesterday')" :value="money(overview?.yesterday?.total_cost)" icon="clock" :loading="overviewLoading" />
      <SStatCard :label="t('me.wallet.last30d')" :value="money(overview?.last_30d?.total_cost)" icon="chart" :loading="overviewLoading" />
    </SGrid>
    <SAlert v-if="wallet.negative.value" tone="danger" data-testid="wallet-negative">{{ t('ledger.negativeHint') }}</SAlert>
    <div v-if="hasTopup" class="grid grid-cols-1 items-start gap-4" :class="{ 'lg:grid-cols-[minmax(0,2fr)_minmax(18rem,1fr)]': canRedeem }">
      <PluginSlot name="wallet.topup" :props="{ onCredited: refresh }" />
      <RedeemCard v-if="canRedeem" @credited="refresh" />
    </div>
    <RedeemCard v-else-if="canRedeem" main @credited="refresh">
      <template #aside>
        <div class="flex gap-3 rounded-lg bg-gray-50 p-4 dark:bg-dark-800" data-testid="wallet-topup-hint">
          <SIcon name="info" class="mt-0.5 h-4 w-4 shrink-0 text-fg-subtle" />
          <div class="flex flex-col gap-1 text-sm">
            <span class="font-medium text-fg">{{ t('me.wallet.noOnline') }}</span>
            <span class="text-fg-muted">{{ t('me.wallet.noOnlineHint') }}</span>
          </div>
        </div>
      </template>
    </RedeemCard>
    <SAlert v-else tone="info" dense data-testid="wallet-topup-hint">{{ t('me.wallet.topupHint') }}</SAlert>
  </div>
</template>
