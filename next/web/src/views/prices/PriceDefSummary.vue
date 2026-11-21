<script setup lang="ts">
// Compact, detailed view of a price definition {mode, config, expression}:
// a per-request price, one line per tier of a per-token price, or the
// expression. With `compare`, values that differ from the compared
// definition are highlighted.
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SHint } from '@sub2api/ui'
import type { PriceSyncDef } from '@/api/types'
import { PRICE_KEYS, asTokenConfig, isSet, num, sortedTiers, type PriceKey, type TokenTier } from './priceExpr'

const props = defineProps<{ def: PriceSyncDef; compare?: PriceSyncDef | null; muted?: boolean }>()
const { t } = useI18n()

type View = { kind: 'per_request'; price: number } | { kind: 'tiers'; tiers: TokenTier[]; rules: number } | { kind: 'custom'; expression: string }

function viewOf(d: PriceSyncDef): View {
  if (d.mode === 'per_request') return { kind: 'per_request', price: num(d.config?.price) }
  if (d.mode === 'expression') return { kind: 'custom', expression: d.expression }
  const cfg = asTokenConfig(d.config)
  return { kind: 'tiers', tiers: sortedTiers(cfg.tiers), rules: cfg.rules.length }
}

const view = computed(() => viewOf(props.def))
const other = computed(() => (props.compare ? viewOf(props.compare) : null))

function usd(v: number | null): string {
  return v === null ? '—' : '$' + String(Number(v.toPrecision(8)))
}

/** A price of a tier: `price` per request, a token price per 1M; null when unset. */
const valueOf = (tier: TokenTier | undefined, k: PriceKey | 'price'): number | null => (isSet(tier?.[k]) ? num(tier?.[k]) : null)

/** The prices a tier shows: its per-request price, or p, c and those set. */
const shownKeys = (tier: TokenTier): Array<PriceKey | 'price'> => (tier.unit === 'request' ? ['price'] : PRICE_KEYS.filter(k => k === 'p' || k === 'c' || isSet(tier[k])))

/** True when the value differs from the same position of the compared definition. */
function differs(i: number, k: PriceKey | 'price'): boolean {
  const [v, o] = [view.value, other.value]
  if (!o || v.kind === 'custom') return false
  if (v.kind === 'per_request') return o.kind !== 'per_request' || o.price !== v.price
  return o.kind !== 'tiers' || valueOf(v.tiers[i], k) !== valueOf(o.tiers[i], k)
}

const HIGHLIGHT = 'font-semibold text-amber-600 dark:text-amber-400'
const label = (k: PriceKey | 'price') => (k === 'price' ? t('prices.def.perRequestLabel') : t(`prices.vars.${k}`))
</script>

<template>
  <div class="text-xs leading-5" :class="muted ? 'text-fg-muted' : 'text-fg'">
    <span v-if="view.kind === 'per_request'" :class="differs(0, 'price') ? HIGHLIGHT : ''">
      {{ t('prices.def.perRequest', { price: usd(view.price) }) }}
    </span>
    <template v-else-if="view.kind === 'tiers'">
      <div v-for="(tier, i) in view.tiers" :key="i" class="flex flex-wrap gap-x-2">
        <SHint v-if="view.tiers.length > 1" inline size="xs" class="font-mono">
          {{ tier.name }}<template v-if="tier.max_len !== null"> ≤{{ tier.max_len }}</template><template v-else> ({{ t('prices.def.otherwise') }})</template>:
        </SHint>
        <span v-for="k in shownKeys(tier)" :key="k" :class="differs(i, k) ? HIGHLIGHT : ''">{{ label(k) }} {{ usd(valueOf(tier, k)) }}</span>
        <SHint v-if="tier.unit !== 'request'" inline size="xs">{{ t('prices.def.perMillion') }}</SHint>
      </div>
      <SHint v-if="view.rules" size="xs">{{ t('prices.def.rules', { n: view.rules }) }}</SHint>
    </template>
    <span v-else class="block max-w-md truncate font-mono" :title="view.expression">{{ t('prices.summary.custom') }} · {{ view.expression }}</span>
  </div>
</template>
