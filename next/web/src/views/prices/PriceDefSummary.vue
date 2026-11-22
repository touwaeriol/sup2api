<script setup lang="ts">
// Compact, detailed view of a price definition {mode, config, expression}:
// one line per tier of a visual price (its condition in words, in the order
// the tiers match; a request tier's price per request), or the
// expression, then how many tiers and rules it has, as the price lists say
// it (priceExtras). With `compare`, values that differ from the compared
// definition are highlighted, and so is a tier whose name or condition does.
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SHint } from '@sub2api/ui'
import type { PriceSyncDef } from '@/api/types'
import { priceExtras, usd } from '@/components/pricing/pricingFormat'
import { asVisualConfig, isSet, num, pricedKeys, type PriceKey, type VisualTier } from './priceExpr'
import { condText } from './tierCond'

const props = defineProps<{ def: PriceSyncDef; compare?: PriceSyncDef | null; muted?: boolean }>()
const { t } = useI18n()

type View = { kind: 'tiers'; tiers: VisualTier[] } | { kind: 'custom'; expression: string }

function viewOf(d: PriceSyncDef): View {
  if (d.mode === 'expression') return { kind: 'custom', expression: d.expression }
  return { kind: 'tiers', tiers: asVisualConfig(d.config).tiers }
}

const view = computed(() => viewOf(props.def))
const other = computed(() => (props.compare ? viewOf(props.compare) : null))
const extras = computed(() => priceExtras(t, props.def))

/** A price of a tier: `price` per request, a token price per 1M; null when unset. */
const valueOf = (tier: VisualTier | undefined, k: PriceKey | 'price'): number | null => (isSet(tier?.[k]) ? num(tier?.[k]) : null)

/** The prices a tier shows: its per-request price, or p, c and those set. */
const shownKeys = (tier: VisualTier): Array<PriceKey | 'price'> => (tier.unit === 'request' ? ['price'] : pricedKeys(tier))

/** A tier's name and its condition in words. */
const headText = (tier: VisualTier | undefined) => tier && `${tier.name} (${tier.when ? condText(t, tier.when) : t('prices.def.otherwise')})`

/** True when what `read` reads of tier i differs from the same tier of the compared definition. */
function differsBy(i: number, read: (tier: VisualTier | undefined) => unknown): boolean {
  const [v, o] = [view.value, other.value]
  if (!o || v.kind === 'custom') return false
  return o.kind !== 'tiers' || read(v.tiers[i]) !== read(o.tiers[i])
}
const differs = (i: number, k: PriceKey | 'price') => differsBy(i, tier => valueOf(tier, k))

const HIGHLIGHT = 'font-semibold text-amber-600 dark:text-amber-400'
const label = (k: PriceKey | 'price') => (k === 'price' ? t('prices.def.perRequestLabel') : t(`prices.vars.${k}`))
</script>

<template>
  <div class="text-xs leading-5" :class="muted ? 'text-fg-muted' : 'text-fg'">
    <template v-if="view.kind === 'tiers'">
      <div v-for="(tier, i) in view.tiers" :key="i" class="flex flex-wrap gap-x-2">
        <SHint v-if="view.tiers.length > 1" inline size="xs" class="font-mono">
          <span :class="differsBy(i, headText) ? HIGHLIGHT : ''">{{ headText(tier) }}:</span>
        </SHint>
        <span v-for="k in shownKeys(tier)" :key="k" :class="differs(i, k) ? HIGHLIGHT : ''">{{ label(k) }} {{ usd(valueOf(tier, k)) }}</span>
        <SHint v-if="tier.unit !== 'request'" inline size="xs">{{ t('prices.def.perMillion') }}</SHint>
      </div>
    </template>
    <span v-else class="block max-w-md truncate font-mono" :title="view.expression">{{ t('prices.summary.custom') }} · {{ view.expression }}</span>
    <SHint v-if="extras" size="xs">{{ extras }}</SHint>
  </div>
</template>
