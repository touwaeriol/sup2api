<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SAlert, SBadge, SGrid, SHint } from '@sub2api/ui'
import type { PriceDef, PriceRule, PricingQuote } from '@/api/pricing'
import { formatMoney } from '@/utils/format'
import SegmentBadge from './SegmentBadge.vue'
import { applyRule, priceBrief, priceCells, rateText } from './pricingFormat'
import { priceLevels, rateLevels, type LevelState, type QuoteLevel } from './quoteLevels'
import { priceLabel, ruleText } from './ruleText'

// A quote (POST /pricing/quote) read as one sum: whether the segment may use
// the group, the model price and the rate level by level (the one that
// applies stands out, the ones under it are greyed), and model price cost ×
// rate = paid. A segment that may not use the group (not granted, or the
// group is disabled) still gets its price: what it would pay. A tiered price
// shows its rest (the last tier) while the cost follows the tier of the
// usage, which a note under the model price says. A model without an
// original price cannot be billed: the sum gives way to saying so.
const props = defineProps<{ quote: PricingQuote; groupName?: string }>()
const { t } = useI18n()

interface LevelRow {
  key: string
  label: string
  /** What the level is set to, or what it says of having no setting. */
  text: string
  state: LevelState
}
interface Chain {
  key: 'price' | 'rate'
  rows: LevelRow[]
  /** A remark under the levels. */
  note?: string
}

/** A level of the model price: the original price itself, or the group's price with the price it comes to; a multiplier of an expression the console cannot break down is the multiplier alone. */
function priceLevelText(v: PriceRule | PriceDef): string {
  if (!('kind' in v)) return priceLabel(t, v)
  const price = applyRule(props.quote.base, v)
  return !price || (v.kind === 'multiplier' && !priceBrief(price)) ? ruleText(t, v) : `${ruleText(t, v)} = ${priceLabel(t, price)}`
}

/** What a level without a setting says. A discount of 1 is no discount: "not set" would read as something left undone. */
const UNSET: Record<string, string | undefined> = { 'rate.discount': 'pricingTools.quote.rate.noDiscount' }

const names = computed(() => ({
  segment: props.quote.segment.name,
  group: props.groupName ?? '',
  base: rateText(props.quote.rate.group_default),
  discount: rateText(props.quote.rate.discount)
}))

function chain<V>(key: Chain['key'], levels: QuoteLevel<string, V>[], text: (v: V) => string, note?: string): Chain {
  const unset = (level: string) => t(UNSET[`${key}.${level}`] ?? 'pricingTools.quote.unset')
  const rows = levels.map((l) => ({ key: l.key, state: l.state, label: t(`pricingTools.quote.${key}.${l.key}`, names.value), text: l.value === null ? unset(l.key) : text(l.value) }))
  return { key, rows, note }
}
const tieredNote = computed(() => (priceCells(props.quote.list_price).tiers > 1 ? t('pricingTools.quote.price.tiered') : undefined))
const chains = computed(() => [chain('price', priceLevels(props.quote), priceLevelText, tieredNote.value), chain('rate', rateLevels(props.quote), rateText)])

const ROW_CLASS: Record<LevelState, string> = {
  applies: 'border-primary-500 bg-primary-50/60 text-fg dark:bg-primary-900/20',
  shadowed: 'border-line text-fg-subtle',
  unset: 'border-dashed border-line text-fg-subtle'
}
</script>

<template>
  <div class="space-y-4 text-sm" data-testid="price-quote">
    <div class="flex flex-wrap items-center gap-2" data-testid="price-quote-access">
      <h4 class="mr-1 font-semibold text-fg">{{ t('pricingTools.quote.access.title') }}</h4>
      <SegmentBadge :segment="quote.segment" with-name />
      <SBadge :tone="quote.accessible ? 'success' : 'danger'">{{ t(`pricingTools.quote.access.${quote.access_source}`) }}</SBadge>
      <SHint v-if="!quote.accessible" inline tone="danger" data-testid="price-quote-denied">{{ t(`pricingTools.quote.denied.${quote.access_source}`) }}</SHint>
    </div>

    <SGrid :cols="1" :lg-cols="2">
      <section v-for="c in chains" :key="c.key" :data-testid="`price-quote-${c.key}`">
        <h4 class="font-semibold text-fg">{{ t(`pricingTools.quote.${c.key}.title`) }}</h4>
        <SHint size="xs" class="mb-2">{{ t(`pricingTools.quote.${c.key}.hint`) }}</SHint>
        <ol class="space-y-1.5">
          <li
            v-for="row in c.rows"
            :key="row.key"
            class="flex items-center justify-between gap-3 rounded-control border px-3 py-2"
            :class="ROW_CLASS[row.state]"
            :data-level="row.key"
            :data-state="row.state"
          >
            <span class="min-w-0 truncate" :title="row.label">{{ row.label }}</span>
            <span class="flex shrink-0 items-center gap-2">
              <span :class="row.state === 'unset' ? '' : 'font-mono'">{{ row.text }}</span>
              <SBadge v-if="row.state === 'applies'" tone="primary" size="xs">{{ t('pricingTools.quote.applies') }}</SBadge>
            </span>
          </li>
        </ol>
        <SHint v-if="c.note" size="xs" class="mt-1.5" :data-testid="`price-quote-${c.key}-note`">{{ c.note }}</SHint>
      </section>
    </SGrid>

    <SAlert v-if="!quote.list_price" tone="warning" data-testid="price-quote-unpriced">{{ t('pricingTools.quote.unpriced') }}</SAlert>
    <div v-else class="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 border-t border-line pt-4" data-testid="price-quote-paid">
      <h4 class="font-semibold text-fg">{{ t(quote.accessible ? 'pricingTools.quote.paid' : `pricingTools.quote.paidIf.${quote.access_source}`) }}</h4>
      <p class="flex flex-wrap items-baseline gap-x-2 gap-y-1">
        <SHint inline>{{ t('pricingTools.quote.listCost') }}</SHint>
        <span class="font-mono text-fg">{{ formatMoney(quote.list_cost, 6) }}</span>
        <span class="text-fg-subtle">×</span>
        <SHint inline>{{ t('pricingTools.quote.rateName') }}</SHint>
        <span class="font-mono text-fg">{{ rateText(quote.rate.value) }}</span>
        <span class="text-fg-subtle">=</span>
        <span class="font-mono text-2xl font-semibold text-fg" data-testid="price-quote-cost">{{ formatMoney(quote.cost, 6) }}</span>
      </p>
    </div>
  </div>
</template>
