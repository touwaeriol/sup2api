<script setup lang="ts">
// The dashboard of a user without the operator's view (design §4.1): today's
// figures from GET /me/usage/overview (calendar days in the browser's time
// zone), the charts of a chosen range from /me/usage/summary, the latest
// requests and shortcuts. The balance comes from the auth store, which the
// top bar already keeps.
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SFilterBar, SAlert, SButton, SDateRange, SEmpty, SGrid, SIcon, SSegmented, SSkeleton, SStatCard, rangeBounds, type RangeKey } from '@sub2api/ui'
import type { AccountCounts, UsageLog, UsageOverview } from '@/api/types'
import PluginSlot from '@/components/plugin/PluginSlot.vue'
import UsageCharts from '@/components/stats/UsageCharts.vue'
import { autoGranularity, browserTZ, formatMs, formatTokens, hitRate, hourAllowed, num, rowTokens, type Granularity } from '@/components/stats/stats'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { ACCOUNT_ACTIONS } from '@/composables/useScope'
import AccountStatusCard from './AccountStatusCard.vue'
import { lt } from '@/i18n'
import { errorMessage } from '@/utils/errors'
import { formatDateTime, formatMoney, formatNumber } from '@/utils/format'

const props = withDefaults(defineProps<{ revision?: number }>(), { revision: 0 })
const emit = defineEmits<{ (e: 'update:loading', v: boolean): void }>()
const { t } = useI18n()
const auth = useAuthStore()
const app = useAppStore()

const canUsage = computed(() => auth.has('usage:self:read'))
const canKeys = computed(() => auth.has('apikey:self:manage'))
const canLedger = computed(() => auth.has('balance:self:read'))
/** A supplier (own-level accounts) or any reader of accounts: the accounts in its scope by status. */
const canAccounts = computed(() => auth.hasAction(ACCOUNT_ACTIONS.read))

const accounts = ref<AccountCounts | null>(null)
const loadingAccounts = ref(false)
async function loadAccounts() {
  if (!canAccounts.value) return
  loadingAccounts.value = true
  try {
    accounts.value = await api.get<AccountCounts>('/accounts/summary')
  } catch {
    accounts.value = null
  } finally {
    loadingAccounts.value = false
  }
}

// Today's figures.
const overview = ref<UsageOverview | null>(null)
const overviewError = ref<unknown>(null)
const loadingOverview = ref(false)
async function loadOverview() {
  if (!canUsage.value) return
  loadingOverview.value = true
  overviewError.value = null
  try {
    overview.value = await api.get<UsageOverview>('/me/usage/overview', { tz: browserTZ() })
  } catch (e) {
    overviewError.value = e
  } finally {
    loadingOverview.value = false
  }
}

const cards = computed(() => {
  const o = overview.value
  const today = o?.today
  const cost = num(today?.total_cost)
  const standard = num(today?.standard_cost)
  const y = num(o?.yesterday.requests)
  const h = hitRate(today)
  return {
    keys: o?.api_keys,
    requests: num(today?.requests),
    trend: y ? ((num(today?.requests) - y) / y) * 100 : null,
    requests30: num(o?.last_30d.requests),
    cost,
    standard: Math.abs(standard - cost) > 1e-9 ? standard : null,
    cost30: num(o?.last_30d.total_cost),
    tokens: today ? rowTokens(today) : 0,
    input: num(today?.input_tokens),
    output: num(today?.output_tokens),
    read: num(today?.cache_read_tokens),
    write: num(today?.cache_creation_tokens),
    hit: h == null ? '—' : (h * 100).toFixed(1) + '%',
    rpm: num(o?.last_5m.requests) / 5,
    tpm: num(o?.last_5m.tokens) / 5,
    firstToken: num(today?.first_token_ms_avg),
    latency: num(today?.latency_ms_avg)
  }
})

// The charts' range: last 7 days by default, automatic granularity.
const range = ref<RangeKey>('7d')
const filters = reactive({ ...rangeBounds('7d') })
const picked = ref<Granularity | null>(null)
watch(() => [filters.from, filters.to], () => (picked.value = null))
const canHour = computed(() => hourAllowed(filters.from, filters.to))
const granularity = computed<Granularity>(() => {
  const g = picked.value ?? autoGranularity(filters.from, filters.to)
  return g === 'hour' && !canHour.value ? 'day' : g
})

// The latest requests.
const recent = ref<UsageLog[]>([])
const loadingRecent = ref(false)
async function loadRecent() {
  if (!canUsage.value) return
  loadingRecent.value = true
  try {
    recent.value = (await api.list<UsageLog>('/me/usage', { page_size: 5 })).items
  } catch {
    recent.value = []
  } finally {
    loadingRecent.value = false
  }
}

// Literal class names so Tailwind keeps them.
const TONE = { primary: 'stat-icon-primary', success: 'stat-icon-success', info: 'stat-icon-info', warning: 'stat-icon-warning' } as const
const quick = computed(() => {
  const list: Array<{ to: string; icon: string; tone: keyof typeof TONE; title: string; sub: string }> = []
  if (canKeys.value) list.push({ to: '/me/api-keys', icon: 'key', tone: 'primary', title: t('dashboard.user.quickKeys'), sub: t('dashboard.user.quickKeysSub') })
  if (canUsage.value) list.push({ to: '/me/usage', icon: 'usage', tone: 'success', title: t('dashboard.user.quickUsage'), sub: t('dashboard.user.quickUsageSub') })
  if (canLedger.value) list.push({ to: '/me/wallet', icon: 'ledger', tone: 'info', title: t('dashboard.user.quickLedger'), sub: t('dashboard.user.quickLedgerSub') })
  // Plugin pages the user can open.
  for (const s of app.menus) for (const i of s.items) if (i.pluginKey && list.length < 5) list.push({ to: i.path, icon: i.icon || 'puzzle', tone: 'warning', title: lt(i.label) || i.id, sub: '' })
  return list
})

const chartsRevision = ref(0)
function reload() {
  loadOverview()
  loadAccounts()
  loadRecent()
  auth.refreshBalance()
  chartsRevision.value++
}
watch(() => props.revision, reload)
reload()
watch(() => loadingOverview.value || loadingRecent.value || loadingAccounts.value, (v) => emit('update:loading', v), { immediate: true })
</script>

<template>
  <div class="flex flex-col gap-4">
    <SAlert v-if="overviewError" tone="danger">
      {{ t('stats.loadFailed', { msg: errorMessage(overviewError) }) }}
      <template #actions><SButton size="sm" icon="refresh" @click="loadOverview">{{ t('stats.retry') }}</SButton></template>
    </SAlert>
    <SGrid variant="stats" :cols="2" :xl-cols="4" data-testid="user-dashboard-cards">
      <SStatCard
        v-if="auth.balance !== null"
        :label="t('dashboard.balance')"
        :value="formatMoney(auth.balance, 2)"
        :sub="canLedger ? t('dashboard.user.balanceSub') : undefined"
        icon="balance"
        :tone="Number(auth.balance) < 0 ? 'danger' : 'success'"
        :to="canLedger ? '/me/wallet' : undefined"
      />
      <AccountStatusCard v-if="canAccounts" :counts="accounts" :loading="loadingAccounts && !accounts" />
      <template v-if="canUsage">
        <SStatCard
          v-if="canKeys"
          :label="t('dashboard.user.apiKeys')"
          :value="formatNumber(cards.keys?.total ?? 0)"
          :sub="t('dashboard.user.apiKeysSub', { n: cards.keys?.active ?? 0 })"
          icon="key"
          tone="info"
          to="/me/api-keys"
          :loading="loadingOverview && !overview"
        />
        <SStatCard
          :label="t('dashboard.user.todayRequests')"
          :value="formatNumber(cards.requests)"
          :trend="cards.trend"
          :sub="t('dashboard.user.last30dRequests', { n: formatNumber(cards.requests30) })"
          icon="chart"
          :loading="loadingOverview && !overview"
        />
        <SStatCard
          :label="t('dashboard.user.todayCost')"
          :value="formatMoney(cards.cost, 2)"
          :sub="cards.standard !== null ? t('dashboard.user.standard', { cost: formatMoney(cards.standard, 2) }) : undefined"
          :sub2="t('dashboard.user.last30dCost', { cost: formatMoney(cards.cost30, 2) })"
          icon="price"
          tone="accent"
          :loading="loadingOverview && !overview"
        />
        <SStatCard
          :label="t('dashboard.user.todayTokens')"
          :value="formatTokens(cards.tokens)"
          :sub="t('dashboard.user.tokensSub', { input: formatTokens(cards.input), output: formatTokens(cards.output) })"
          :sub2="t('dashboard.user.cacheSub', { read: formatTokens(cards.read), write: formatTokens(cards.write) })"
          icon="bolt"
          tone="warning"
          :loading="loadingOverview && !overview"
        />
        <SStatCard :label="t('dashboard.user.hitRate')" :value="cards.hit" :sub="t('dashboard.user.hitRateSub')" icon="check" tone="success" :loading="loadingOverview && !overview" />
        <SStatCard
          :label="t('dashboard.user.performance')"
          :value="t('dashboard.user.rpm', { n: formatNumber(cards.rpm, 1) })"
          :sub="t('dashboard.user.tpm', { n: formatTokens(cards.tpm) })"
          icon="cpu"
          tone="accent"
          :loading="loadingOverview && !overview"
        />
        <SStatCard
          :label="t('dashboard.user.response')"
          :value="formatMs(cards.firstToken)"
          :sub="t('dashboard.user.firstTokenLabel')"
          :sub2="t('dashboard.user.duration', { t: formatMs(cards.latency) })"
          icon="clock"
          tone="info"
          :loading="loadingOverview && !overview"
        />
      </template>
      <PluginSlot name="dashboard.widgets" />
    </SGrid>

    <template v-if="canUsage">
      <SFilterBar :aria-label="t('stats.filters')">
        <SDateRange v-model:range="range" v-model:from="filters.from" v-model:to="filters.to" size="sm" :label="false" />
        <SSegmented
          :model-value="granularity"
          :options="[
            { value: 'day', label: t('stats.day') },
            { value: 'hour', label: t('stats.hour'), disabled: !canHour }
          ]"
          size="sm"
          :aria-label="t('stats.granularity')"
          @update:model-value="picked = $event as Granularity"
        />
        <template #end><span class="text-xs text-fg-subtle">{{ t('stats.tz', { tz: browserTZ() }) }}</span></template>
      </SFilterBar>
      <UsageCharts summary-path="/me/usage/summary" :filters="filters" :granularity="granularity" layout="dashboard" :revision="chartsRevision" group="dashboard-mine" />

      <div class="grid grid-cols-1 gap-4 xl:grid-cols-3">
        <section class="card xl:col-span-2">
          <header class="card-header flex items-center gap-2">
            <h3 class="text-sm font-semibold text-fg">{{ t('dashboard.user.recent') }}</h3>
            <span class="text-xs text-fg-subtle">{{ t('dashboard.user.recentHint') }}</span>
            <RouterLink to="/me/usage" class="ms-auto text-sm text-primary-600 hover:underline dark:text-primary-400">{{ t('dashboard.user.viewAll') }} →</RouterLink>
          </header>
          <div class="flex flex-col gap-2 p-4" data-testid="recent-usage">
            <template v-if="loadingRecent && !recent.length"><SSkeleton v-for="i in 3" :key="i" height="3.25rem" /></template>
            <SEmpty v-else-if="!recent.length" :title="t('dashboard.user.recentEmpty')" :text="t('dashboard.user.recentEmptyHint')" size="sm" />
            <div v-for="r in recent" :key="r.id" class="flex items-center gap-3 rounded-xl bg-surface-2 px-3 py-2.5">
              <div class="stat-icon !h-9 !w-9" :class="r.success ? 'stat-icon-primary' : 'stat-icon-danger'">
                <SIcon :name="r.success ? 'cpu' : 'warning'" class="h-5 w-5" />
              </div>
              <div class="min-w-0">
                <p class="truncate font-mono text-sm font-medium text-fg">{{ r.model || '—' }}</p>
                <p class="text-xs text-fg-subtle">
                  {{ formatDateTime(r.created_at) }}<template v-if="!r.success"> · HTTP {{ r.status_code }}</template>
                </p>
              </div>
              <div class="ms-auto text-right">
                <p class="text-sm font-semibold tabular-nums text-fg">{{ num(r.total_cost) ? formatMoney(r.total_cost) : t('dashboard.user.free') }}</p>
                <p class="text-xs tabular-nums text-fg-subtle">{{ t('dashboard.user.tokens', { n: formatNumber(rowTokens(r)) }) }}</p>
              </div>
            </div>
          </div>
        </section>
        <section class="card">
          <header class="card-header"><h3 class="text-sm font-semibold text-fg">{{ t('dashboard.user.quick') }}</h3></header>
          <div class="flex flex-col gap-2 p-4" data-testid="quick-actions">
            <RouterLink v-for="q in quick" :key="q.to" :to="q.to" class="flex items-center gap-3 rounded-xl bg-surface-2 px-3 py-2.5 transition-colors hover:bg-line/40">
              <div class="stat-icon !h-10 !w-10" :class="TONE[q.tone]"><SIcon :name="q.icon" class="h-5 w-5" /></div>
              <div class="min-w-0">
                <p class="truncate text-sm font-medium text-fg">{{ q.title }}</p>
                <p v-if="q.sub" class="truncate text-xs text-fg-subtle">{{ q.sub }}</p>
              </div>
              <SIcon name="chevron-right" class="ms-auto h-4 w-4 text-fg-subtle" />
            </RouterLink>
          </div>
        </section>
      </div>
    </template>
  </div>
</template>
