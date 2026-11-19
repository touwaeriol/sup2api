<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { SBadge, SCard, SCode, SDescItem, SDescriptions, SDisclosure, SHint, SIcon, SLink } from '@sub2api/ui'
import type { UsagePricing } from '@/api/pricing'
import ModelBadge from '@/components/platform/ModelBadge.vue'
import { ruleText } from '@/components/pricing/ruleText'
import { useAuthStore } from '@/stores/auth'
import { formatMoney, formatNumber } from '@/utils/format'
import { useOptionalT } from '@/composables/optionalText'
import BillingBreakdown from '@/views/prices/BillingBreakdown.vue'
import ExprHistoryModal from '@/views/prices/ExprHistoryModal.vue'
import UsageTokenFacts from './UsageTokenFacts.vue'
import { billingTone, factText, usageComponents, type UsageRow } from './usage'

// Billing half of an expanded usage record (billing_detail, A.14).
const props = defineProps<{ u: UsageRow }>()
const { t } = useI18n()
const tr = useOptionalT()
const auth = useAuthStore()
const historyOpen = ref(false)

type Rule = { cond: string; multiplier: number | string; matched: boolean }
// billing_detail is free-form JSON (api/types: Record<string, any>).
type Detail = NonNullable<UsageRow['billing_detail']>
const bd = computed<Detail>(() => props.u.billing_detail || {})
const tierLen = computed<number | undefined>(() => bd.value.breakdown?.vars?.len)
const tierName = computed(() => props.u.matched_tier || bd.value.tier || '')
const rules = computed<Rule[]>(() => (Array.isArray(bd.value.rules) ? bd.value.rules : []))
const ledgerId = computed(() => props.u.ledger_id ?? bd.value.ledger_id ?? null)
const rate = computed(() => bd.value.rate_multiplier ?? props.u.rate_multiplier ?? null)
// Group pricing (inputs.pricing, list_cost): a record at the global price and the group's default rate has neither.
const pricing = computed<UsagePricing>(() => pricingOf(bd.value))
/** The rule that gave the list price, when one did: whose it is and what it makes of the global price. */
const listRule = computed(() => {
  const { price_source: source, scale, base } = pricing.value
  if (!source || source === 'global') return null
  const multiplier = scale === undefined ? '' : ruleText(t, { kind: 'multiplier', multiplier: scale })
  return { source, text: base ? t('pricing.rule.custom') : multiplier }
})
// Items priced separately from the primary usage, such as an advisor model at its own price.
const components = computed(() => (['additional', 'replacement'] as const)
  .map(list => ({ list, items: usageComponents(props.u, list) }))
  .filter(group => group.items.length))
// A replaced primary usage is not priced: its rounds are.
const replaced = computed(() => components.value.some(group => group.list === 'replacement'))
// With items the top of billing_detail sums them all; the primary usage's own amount is primary_total_cost
// (absent on records settled before it was kept, whose primary formula then shows no amount).
const primaryTotal = computed(() => (components.value.length ? bd.value.primary_total_cost ?? null : props.u.total_cost))
/** primary + every item = total, for a record with items. */
const sumTerms = computed(() => [
  ...(replaced.value ? [] : [{ label: t('usage.billing.primaryUsage'), amount: primaryTotal.value }]),
  ...components.value.flatMap(group => group.items.map((item, i) => ({ label: itemLabel(group.list, item.kind, i), amount: item.detail.total_cost ?? null })))
])
const inputs = computed(() => [
  ...Object.entries(bd.value.inputs?.params || {}).map(([key, value]) => ({ name: `param("${key}")`, value })),
  ...Object.entries(bd.value.inputs?.headers || {}).map(([key, value]) => ({ name: `header("${key}")`, value }))
])

function pricingOf(detail: Detail): UsagePricing {
  return detail.inputs?.pricing || {}
}

/** BillingBreakdown's props for one priced part of a record (the primary usage or an item): its own breakdown, rules, rates and amount. */
function breakdownOf(detail: Detail, rateMultiplier: string | number | null, total: string | number | null) {
  const { scale, rate_source: source } = pricingOf(detail)
  return {
    breakdown: detail.breakdown || {}, scale, rateMultiplier, total,
    rateSource: source ? label('pricingAccess.source', source) : undefined,
    rules: Array.isArray(detail.rules) ? detail.rules : [],
    cacheWriteLabels: { default: t('usage.tokens.cacheDefaultBucket'), oneHour: t('usage.tokens.cache1hBucket') }
  }
}

/** A replacement item is an attempt round (every round has the attempts rule's kind); an additional item is its kind. */
function itemLabel(list: 'additional' | 'replacement', kind: string, index: number) {
  return list === 'replacement' ? t('usage.billing.componentRound', { round: index + 1 }) : label('usage.billing.componentKinds', kind)
}

/** The translation of `prefix.value` when there is one, else the raw value. */
function label(prefix: string, value: string) {
  return tr(`${prefix}.${value}`, undefined, value)
}
</script>

<template>
  <SCard variant="flat" :title="t('usage.billing.title')" class="min-w-0" data-testid="usage-billing-facts">
    <template #actions>
      <SBadge :tone="billingTone(u.billing_status)">{{ label('usage.billing.status', u.billing_status) }}</SBadge>
    </template>
    <SDescriptions dense stacked :columns="2">
      <SDescItem :label="t('usage.billing.price')"><div class="flex flex-wrap items-center gap-2">
        <template v-if="u.price">
          <SLink v-if="auth.has('price:read')" :to="`/prices/${u.price.id}`" class="inline-flex max-w-full"><ModelBadge :model="u.price.model" plain /></SLink>
          <ModelBadge v-else :model="u.price.model" plain />
          <SBadge :tone="u.price.source === 'sync' ? 'info' : 'primary'">{{ label('prices.source', u.price.source) }}</SBadge>
        </template>
        <template v-else-if="u.price_id">
          <SLink v-if="auth.has('price:read')" :to="`/prices/${u.price_id}`">#{{ u.price_id }}</SLink>
          <span v-else>#{{ u.price_id }}</span>
        </template>
        <SHint v-else inline>—</SHint>
      </div></SDescItem>

      <SDescItem v-if="u.expr_hash" :label="t('prices.expression')"><div class="flex flex-wrap items-center gap-2">
        <span v-if="bd.expr_version !== undefined">v{{ bd.expr_version }}</span>
        <SCode inline :title="u.expr_hash" :text="t('usage.billing.hash', { hash: u.expr_hash.slice(0, 8) })" />
        <SLink v-if="auth.has('price:read')" as="button" class="text-xs" @click="historyOpen = true">{{ t('prices.viewHistory') }}</SLink>
      </div></SDescItem>

      <SDescItem v-if="u.billing_mode" :label="t('prices.modeCol')">{{ label('prices.mode', u.billing_mode) }}</SDescItem>

      <SDescItem v-if="tierName" :label="t('usage.billing.tier')">
        <span class="font-mono">{{ tierName }}</span>
        <SHint v-if="tierLen !== undefined" inline size="xs" class="ml-1"> ({{ t('usage.billing.len', { n: formatNumber(tierLen) }) }}) </SHint>
      </SDescItem>

      <SDescItem v-if="listRule" :label="t('usage.billing.listPrice')"><div class="flex flex-wrap items-center gap-2" data-testid="usage-list-price">
        <SBadge :tone="listRule.source === 'segment' ? 'primary' : 'info'">{{ label('pricing.source', listRule.source) }}</SBadge>
        <span>{{ listRule.text }}</span>
      </div></SDescItem>

      <SDescItem v-if="bd.list_cost != null" :label="t('usage.billing.listCost')">
        <span class="font-mono" data-testid="usage-list-cost">{{ formatMoney(bd.list_cost, 8) }}</span>
      </SDescItem>
    </SDescriptions>

    <div v-if="!replaced || ledgerId" class="mt-3 border-t border-line pt-3" data-testid="usage-primary">
      <template v-if="!replaced">
        <BillingBreakdown v-if="Object.keys(bd.breakdown || {}).length || u.total_cost" v-bind="breakdownOf(bd, rate, primaryTotal)" data-testid="usage-primary-breakdown" />
        <SHint v-else inline>—</SHint>
      </template>
      <SHint v-if="ledgerId" size="xs" class="mt-2">{{ t('usage.billing.ledger', { id: ledgerId }) }}</SHint>
    </div>

    <div v-for="group in components" :key="group.list" class="mt-3 space-y-3 border-t border-line pt-3" :data-testid="`usage-${group.list}`">
      <h4 class="text-sm font-medium">{{ t(`usage.billing.components.${group.list}`) }}</h4>
      <div v-for="(item, i) in group.items" :key="i" class="space-y-2" :data-testid="`usage-${group.list}-item`">
        <div class="flex flex-wrap items-center gap-2">
          <SBadge tone="info">{{ itemLabel(group.list, item.kind, i) }}</SBadge>
          <ModelBadge :model="item.model" plain />
          <SBadge v-if="item.free" tone="gray" data-testid="usage-component-free">{{ t('usage.free') }}</SBadge>
          <SHint v-if="item.free && item.reason" inline size="xs" class="font-mono" data-testid="usage-component-reason">{{ item.reason }}</SHint>
          <span class="ml-auto font-mono text-sm">{{ formatMoney(item.detail.total_cost, 8) }}</span>
        </div>
        <UsageTokenFacts :tokens="item.tokens" />
        <BillingBreakdown v-if="!item.free" v-bind="breakdownOf(item.detail, item.detail.rate_multiplier ?? null, item.detail.total_cost ?? null)" data-testid="usage-component-breakdown" />
      </div>
    </div>

    <p v-if="components.length" class="mt-3 border-t border-line pt-3 font-mono text-xs" data-testid="usage-components-total">
      <template v-for="(term, i) in sumTerms" :key="i">
        <template v-if="i"> + </template>{{ term.label }} {{ term.amount == null ? '—' : formatMoney(term.amount, 8) }}
      </template>
      = {{ t('usage.billing.componentsTotal') }} <span class="font-semibold">{{ formatMoney(u.total_cost, 8) }}</span>
    </p>

    <SDisclosure v-if="rules.length" :label="t('usage.billing.rules')" :open-label="t('usage.billing.rules')" class="mt-3">
      <ul class="space-y-1">
        <li v-for="(r, i) in rules" :key="i" class="flex flex-wrap items-center gap-2">
          <SCode inline :text="r.cond" />
          <span>×{{ r.multiplier }}</span>
          <SBadge :tone="r.matched ? 'success' : 'gray'">
            <SIcon :name="r.matched ? 'check' : 'x'" class="h-3 w-3" />{{ r.matched ? t('usage.billing.matched') : t('usage.billing.notMatched') }}
          </SBadge>
        </li>
      </ul>
    </SDisclosure>

    <SDisclosure v-if="inputs.length" :label="t('usage.billing.inputs')" :open-label="t('usage.billing.inputs')" class="mt-3">
      <div v-for="input in inputs" :key="input.name" class="break-all font-mono text-xs">{{ input.name }} = {{ factText(input.value) }}</div>
    </SDisclosure>
    <ExprHistoryModal v-model:open="historyOpen" :hash="u.expr_hash" />
  </SCard>
</template>
