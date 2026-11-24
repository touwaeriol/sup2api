<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SAlert, SBadge, SCard, SCode, SCopyButton, SDescItem, SDescriptions, SDisclosure, SGrid, SHint, SIcon, SIPAddress, SSpinner } from '@sub2api/ui'
import { useAuthStore } from '@/stores/auth'
import { ACCOUNT_ACTIONS } from '@/composables/useScope'
import { formatMoney } from '@/utils/format'
import { errorMessage } from '@/utils/errors'
import { useAccountTypes } from '@/views/accounts/accountTypes'
import ModelBadge from '@/components/platform/ModelBadge.vue'
import PlatformBadge from '@/components/platform/PlatformBadge.vue'
import { accountLabel, availabilityTrace, factText, hookTone, isConverted, knownUsageRounds, schedulingTrace, seconds, type UsageRow } from './usage'
import { LIST_PRICE_LABEL, type UsageView } from './view'
import UsageAccountMark from './UsageAccountMark.vue'
import UsageAvailabilityTrace from './UsageAvailabilityTrace.vue'
import UsageSchedulingTrace from './UsageSchedulingTrace.vue'
import UsageAttemptTrace from './UsageAttemptTrace.vue'
import UsageConversionBadges from './UsageConversionBadges.vue'
import UsageConversionTrace from './UsageConversionTrace.vue'
import UsageBillingFacts from './UsageBillingFacts.vue'
import UsageStatus from './UsageStatus.vue'
import UsageTokenFacts from './UsageTokenFacts.vue'
import CacheWriteFacts from './CacheWriteFacts.vue'
import { componentCacheWriteMetrics, otherUsageMetrics } from './cacheWriteEvidence'
import FailureDecisionDetails from '@/views/settings/FailureDecisionDetails.vue'
import type { FailureDecision } from '@/views/settings/failurePolicy'
import { displayPluginId } from '@/views/plugins/pluginId'

// Expanded usage record: billing detail (A.14), request info, tokens, hooks, sticky.
// `view` `self`: the caller's own records, whose attempts show no accounts;
// `account`: the account's facts only (CONTRACTS §103.5): no billing, no
// group, key, client or user agent; the cost at the list price.
const props = defineProps<{ row: UsageRow; path?: string; view: UsageView }>()
const accountView = computed(() => props.view === 'account')
const { t } = useI18n()
const auth = useAuthStore()
const accountTypes = useAccountTypes()
if (auth.hasAction(ACCOUNT_ACTIONS.read)) accountTypes.load()

const detail = ref<UsageRow | null>(null)
const loading = ref(false)
const error = ref('')

onMounted(async () => {
  if (!props.path) return
  loading.value = true
  try {
    detail.value = await api.get<UsageRow>(props.path)
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    loading.value = false
  }
})

const u = computed<UsageRow>(() => ({ ...props.row, ...(detail.value || {}) }))
const summary = computed(() => [
  { label: t(accountView.value ? LIST_PRICE_LABEL : 'usage.cols.cost'), value: u.value.billing_status === 'free' ? t('usage.free') : formatMoney(accountView.value ? u.value.standard_cost : u.value.total_cost, 8) },
  { label: t('usage.request.latency'), value: seconds(u.value.latency_ms) },
  { label: t('usage.request.firstToken'), value: u.value.stream && u.value.first_token_ms != null ? seconds(u.value.first_token_ms) : '—' },
  { label: t('usage.request.upload'), value: u.value.upload_ms ? seconds(u.value.upload_ms) : '—' }
])
const knownRounds = computed(() => knownUsageRounds(u.value))
const otherMetrics = computed(() => otherUsageMetrics(u.value.metrics))
const componentCacheFacts = computed(() => componentCacheWriteMetrics(u.value.billing_detail).filter((item) => !knownRounds.value.length || item.kind !== 'replacement'))
const hooks = computed(() => u.value.hook_decisions || [])
const failureDecisions = computed<FailureDecision[]>(() => {
  try {
    const value: unknown = JSON.parse(u.value.anomalies?.failure_policy || '[]')
    return Array.isArray(value) ? value.filter(v => v && typeof v.action === 'string').slice(0, 10) : []
  } catch { return [] }
})
// A model availability rejection's message is the generic client text; the recorded trace has the reasons.
const availabilityHint = computed(() => u.value.error_type === 'model_availability' && !!availabilityTrace(u.value))
// "no available account" and "busy" are the client's text too; the scheduling trace says which account was skipped for what.
const schedulingHint = computed(() => u.value.error_type === 'no_account' && !!schedulingTrace(u.value))
</script>

<template>
  <div class="usage-detail space-y-4 bg-surface-2/60 p-3 md:p-5">
    <SHint v-if="error" tone="danger">{{ error }}</SHint>
    <SCard variant="flat" :padded="false" data-testid="usage-detail-summary">
      <div class="flex flex-wrap items-center gap-4 p-4">
        <div class="min-w-0 flex-1">
          <ModelBadge v-if="u.model" :model="u.model" class="mb-2" />
          <p v-else class="mb-2 text-sm">—</p>
          <div class="flex flex-wrap items-center gap-2">
            <UsageStatus :row="u" />
            <SBadge size="xs" :tone="u.stream ? 'info' : 'gray'">{{ u.stream ? t('usage.stream') : t('usage.nonStream') }}</SBadge>
            <SSpinner v-if="loading" />
          </div>
        </div>
        <dl class="grid w-full grid-cols-3 gap-4 border-t border-line pt-3 sm:w-auto sm:min-w-[22rem] sm:border-t-0 sm:pt-0">
          <div v-for="item in summary" :key="item.label" class="min-w-0">
            <dt class="mb-1 text-xs text-fg-muted">{{ item.label }}</dt>
            <dd class="break-all font-mono text-sm font-semibold tabular-nums">{{ item.value }}</dd>
          </div>
        </dl>
      </div>
    </SCard>
    <SAlert v-if="!u.success && (u.error_type || u.error_message)" tone="danger" :title="t('usage.request.error')" dense>
      <span class="break-all font-mono text-xs">{{ u.error_type }}</span>
      <p v-if="u.error_message" class="mt-1 break-words text-sm">{{ u.error_message }}</p>
      <p v-if="availabilityHint" class="mt-1 text-sm" data-testid="usage-availability-hint">{{ t('usage.availability.seeBelow') }}</p>
      <p v-if="schedulingHint" class="mt-1 text-sm" data-testid="usage-scheduling-hint">{{ t('usage.scheduling.seeBelow') }}</p>
      <p v-if="u.error_type === 'usage_unreported'" class="mt-1 text-sm" data-testid="usage-unreported-hint">{{ t('usage.unreportedHint') }}</p>
    </SAlert>

    <SGrid :cols="1" :lg-cols="knownRounds.length || accountView ? 1 : 2" :gap="4" class="items-start" data-testid="usage-detail-primary">
      <UsageBillingFacts v-if="!accountView" :u="u" />
      <SCard variant="flat" :title="t('usage.tokens.title')" class="min-w-0" data-testid="usage-token-facts">
        <div v-if="knownRounds.length" class="space-y-3" data-testid="known-usage-rounds">
          <SHint tone="warning">{{ t('usage.tokens.incompleteRounds') }}</SHint>
          <SGrid :cols="1" :md-cols="2" class="items-start">
            <div v-for="round in knownRounds" :key="round.round" data-testid="known-usage-round">
              <h4 class="mb-2 text-sm font-medium">{{ t('usage.tokens.knownRound', { round: round.round }) }}</h4>
              <UsageTokenFacts :tokens="round.tokens" />
              <CacheWriteFacts :metrics="round.metrics" :has-cache-writes="!!(round.tokens.cache_creation_tokens || round.tokens.cache_creation_1h_tokens)" />
            </div>
          </SGrid>
        </div>
        <UsageTokenFacts v-else :tokens="u" />
        <CacheWriteFacts
          v-if="!knownRounds.length"
          :metrics="u.metrics"
          :has-cache-writes="!!(u.cache_creation_tokens || u.cache_creation_1h_tokens)"
          compact
        />
        <dl v-if="Object.keys(otherMetrics).length" class="mt-3 flex flex-wrap gap-x-4 gap-y-2 border-t border-line pt-3">
          <div v-for="(v, k) in otherMetrics" :key="k" class="flex min-w-0 items-baseline gap-2 text-xs">
            <dt class="font-mono text-fg-muted">u("{{ k }}")</dt>
            <dd class="break-all">{{ factText(v) }}</dd>
          </div>
        </dl>
      </SCard>
    </SGrid>

    <SCard variant="flat" :title="t('usage.request.title')" data-testid="usage-request-facts">
      <SDescriptions dense :columns="2" label-width="6rem">
        <SDescItem v-if="!accountView" :label="t('common.group')">{{ u.group_name || (u.group_id ? '#' + u.group_id : '—') }}</SDescItem>
        <SDescItem v-if="u.account_id || u.account_name" :label="t('common.account')"><span class="inline-flex items-center gap-1" data-testid="detail-account">
          <UsageAccountMark :row="u" />{{ accountLabel(u) }}
        </span></SDescItem>
        <SDescItem v-if="u.api_key_name || u.api_key_id" :label="t('usage.request.apiKey')">{{ u.api_key_name || '#' + u.api_key_id }}</SDescItem>
        <SDescItem v-if="!accountView" :label="t('usage.request.clientIP')"><SIPAddress :value="u.client_ip" data-testid="detail-client-ip" /></SDescItem>
        <SDescItem :label="t('usage.request.id')"><div class="flex items-center gap-1">
          <SCode inline class="min-w-0 break-all" :text="u.request_id" />
          <SCopyButton :text="u.request_id" icon-only class="shrink-0" />
        </div></SDescItem>
        <SDescItem v-if="!accountView" :label="t('usage.request.clientId')"><div class="flex min-w-0 items-center gap-1" data-testid="detail-client-request-id">
          <template v-if="u.client_request_id">
            <SCode inline class="break-all" :text="u.client_request_id" />
            <SCopyButton :text="u.client_request_id" icon-only class="shrink-0" />
          </template>
          <SHint v-else inline :title="t('usage.request.clientIdNone')">—</SHint>
        </div></SDescItem>
        <SDescItem v-if="u.endpoint || u.protocol" :label="t('usage.request.endpoint')"><span class="font-mono text-xs">
          {{ u.endpoint || '—' }} <SHint inline size="xs">{{ u.protocol }}</SHint>
          <template v-if="u.platform"><SHint inline size="xs" class="font-sans"> · {{ t('common.platform') }}</SHint> <PlatformBadge :id="u.platform" class="font-sans" /></template>
        </span></SDescItem>
        <SDescItem v-if="u.account_type" :label="t('usage.cols.accountType')">
          {{ accountTypes.typeLabel(u.plugin_key, u.account_type) }}
          <SHint inline size="xs">
            · {{ accountTypes.pluginName(u.plugin_key) }}
            <span class="font-mono">({{ displayPluginId(u.plugin_key) }} · {{ u.account_type }})</span>
          </SHint>
        </SDescItem>
        <SDescItem :label="t('usage.cols.upstreamProtocol')"><div class="flex flex-wrap items-center gap-2">
          <span class="font-mono text-xs">{{ u.upstream_protocol || u.protocol || '—' }}</span>
          <UsageConversionBadges :row="u" />
          <SHint v-if="isConverted(u)" inline size="xs">{{ t('usage.convertedFrom', { protocol: u.protocol }) }}</SHint>
        </div></SDescItem>
        <SDescItem v-if="u.upstream_model && u.upstream_model !== u.model" :label="t('usage.request.upstreamModel')"><ModelBadge :model="u.upstream_model" plain /></SDescItem>
        <SDescItem v-if="u.node_id" :label="t('usage.request.node')"><span class="font-mono text-xs">{{ u.node_id }}</span></SDescItem>
        <SDescItem v-if="u.attempts !== undefined" :label="t('usage.request.attempts')">{{ u.attempts }}</SDescItem>
        <SDescItem v-if="!accountView" :label="t('usage.request.userAgent')" full><span class="break-all text-xs">{{ u.user_agent || '—' }}</span></SDescItem>
      </SDescriptions>
    </SCard>

    <SGrid v-if="componentCacheFacts.length" :cols="1" :lg-cols="2" class="items-start">
      <div v-for="item in componentCacheFacts" :key="`${item.kind}:${item.index}`" data-testid="component-cache-write-facts">
        <h4 class="text-sm font-medium">{{ t(`usage.cacheEvidence.component.${item.kind}`, { index: item.index }) }}</h4>
        <CacheWriteFacts :metrics="item.metrics" />
      </div>
    </SGrid>

    <SGrid v-if="hooks.length || u.sticky_rule" :cols="1" :lg-cols="hooks.length && u.sticky_rule ? 2 : 1" class="items-start">
      <SCard v-if="hooks.length" variant="flat">
        <SDisclosure variant="section" :label="t('usage.hooks.title')" :default-open="hooks.some(h => h.decision !== 'allow')">
          <template #actions><SBadge size="xs">{{ hooks.length }}</SBadge></template>
          <ul class="space-y-1 text-sm">
            <li v-for="(h, i) in hooks" :key="i" class="flex flex-wrap items-center gap-2">
              <span class="font-mono text-xs">{{ displayPluginId(h.plugin_key) }} · {{ h.hook_id }}</span>
              <SBadge :tone="hookTone(h.decision)">{{ h.decision }}</SBadge>
              <SHint inline size="xs">{{ h.latency_ms }} ms</SHint>
              <span v-if="h.note" class="text-xs">{{ h.note }}</span>
            </li>
          </ul>
        </SDisclosure>
      </SCard>

      <SCard v-if="u.sticky_rule" variant="flat">
        <SDisclosure variant="section" :label="t('usage.sticky.title')">
          <template #actions>
            <SBadge size="xs" :tone="u.sticky_hit ? 'success' : 'gray'">{{ u.sticky_hit ? t('usage.sticky.hitYes') : t('usage.sticky.hitNo') }}</SBadge>
          </template>
          <SDescriptions dense>
            <SDescItem :label="t('usage.sticky.rule')"><span class="font-mono text-xs">{{ u.sticky_rule }}</span></SDescItem>
            <SDescItem :label="t('usage.sticky.hit')">
              <SBadge :tone="u.sticky_hit ? 'success' : 'gray'">
                <SIcon :name="u.sticky_hit ? 'check' : 'x'" class="h-3 w-3" />{{ u.sticky_hit ? t('usage.sticky.hitYes') : t('usage.sticky.hitNo') }}
              </SBadge>
            </SDescItem>
          </SDescriptions>
        </SDisclosure>
      </SCard>
    </SGrid>
    <UsageAttemptTrace :row="u" :mine="view === 'self'" />
    <UsageSchedulingTrace :row="u" />
    <UsageAvailabilityTrace :row="u" />
    <UsageConversionTrace :row="u" />
    <SCard v-if="failureDecisions.length" variant="flat" data-testid="failure-decisions">
      <SDisclosure variant="section" :label="t('failurePolicy.decisions')" default-open>
        <FailureDecisionDetails v-for="(decision, index) in failureDecisions" :key="index" :decision="decision" />
      </SDisclosure>
    </SCard>
    <SHint v-if="!u.success && u.billing_status === 'free'" size="xs">{{ t('usage.billing.freeNote') }}</SHint>
  </div>
</template>
