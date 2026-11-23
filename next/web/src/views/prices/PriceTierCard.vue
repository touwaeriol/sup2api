<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { SButton, SCheckbox, SDropdown, SIcon, SInput, SSelect } from '@sub2api/ui'
import { usd } from '@/components/pricing/pricingFormat'
import { MAX_COND_DEPTH, MAX_COND_LEAVES, chargedKeys, orNull, type PriceKey, type VisualTier } from './priceExpr'
import { condText } from './tierCond'
import { priceOn, setPriceOn } from './visualPrice'
import TierCondTree from './TierCondTree.vue'

// One tier of a visual price as a numbered card: the header reads back
// when it matches (its condition in words) and what it charges; the body
// edits the condition (TierCondTree; the rest has none), the name, the unit
// and the prices. A price is on when its box is ticked (the key is in the
// tier, visualPrice.priceOn); a ticked price left blank is not filled in
// (never 0): the price cannot be saved until a number is typed. The
// menu moves the tier up or down (`canUp` / `canDown`) and removes it.
// Read-only, it still opens and closes.
const tier = defineModel<VisualTier>({ required: true })
const props = defineProps<{ index: number; count: number; canUp?: boolean; canDown?: boolean; disabled?: boolean }>()
const open = defineModel<boolean>('open', { default: false })
const emit = defineEmits<{ (e: 'remove'): void; (e: 'move', by: number): void }>()
const { t } = useI18n()

const isRest = computed(() => props.index === props.count - 1)
const only = computed(() => props.count === 1)

const condition = computed(() => {
  if (only.value) return t('prices.tier.matchOnly')
  if (isRest.value) return t('prices.tier.matchRest')
  return condText(t, tier.value.when!)
})

/** A price as typed: '—' while blank. */
const typed = (v: unknown) => usd(orNull(v))
const summary = computed(() => {
  const x = tier.value
  if (x.unit === 'request') return [{ k: t('prices.def.perRequestLabel'), v: typed(x.price) }]
  return chargedKeys(x).map(k => ({ k: t(`prices.vars.${k}`), v: typed(x[k]) }))
})

const unitOptions = computed(() => (['token', 'request'] as const).map(u => ({ value: u, label: t(`prices.tier.unitOf.${u}`) })))
const menu = computed(() => [
  { key: 'up', label: t('prices.tier.moveUp'), disabled: !props.canUp },
  { key: 'down', label: t('prices.tier.moveDown'), disabled: !props.canDown },
  { key: 'remove', label: t('prices.tier.remove'), danger: true, divider: true, disabled: only.value }
])
function onMenu(key: string) {
  if (key === 'remove') emit('remove')
  else emit('move', key === 'up' ? -1 : 1)
}

/** A price box: p and c are always charged (their boxes stay ticked); cc1h, img and img_o fall back to another price while unticked. */
type Field = { key: PriceKey; fallback?: boolean }
const TOKEN_ROWS: Field[][] = [[{ key: 'p' }, { key: 'c' }], [{ key: 'cr' }, { key: 'cc' }, { key: 'cc1h', fallback: true }]]
const MEDIA_FIELDS: Field[] = [
  { key: 'img', fallback: true },
  { key: 'img_o', fallback: true }
]
const on = (k: PriceKey) => priceOn(tier.value, k)
const placeholder = (f: Field) => (on(f.key) ? t('prices.tier.required') : f.fallback ? t(`prices.tier.fallback.${f.key}`) : t('prices.unsetPlaceholder'))
/** The media prices start open on a tier that charges one (a card is keyed by its tier: a template makes new cards). */
const mediaOpen = ref(MEDIA_FIELDS.some(f => on(f.key)))
</script>

<template>
  <li
    class="rounded-xl border bg-surface transition"
    :class="open ? 'border-primary-300 ring-1 ring-primary-200 dark:border-primary-700 dark:ring-primary-900' : 'border-line'"
    :data-tier="index"
  >
    <!-- header: what it matches and charges -->
    <div class="flex items-start gap-3 px-4 py-3">
      <span class="mt-0.5 grid h-5 min-w-5 place-items-center rounded-full bg-surface-2 px-1.5 text-[11px] font-medium text-fg-muted ring-1 ring-line">{{ index + 1 }}</span>
      <button type="button" class="flex min-w-0 flex-1 items-start gap-2 text-left" :aria-expanded="open" data-testid="tier-toggle" @click="open = !open">
        <span class="min-w-0 flex-1">
          <span class="flex items-center gap-2">
            <span class="font-mono text-sm text-fg">{{ tier.name || t('prices.tier.unnamed') }}</span>
            <span v-if="isRest && !only" class="rounded-md bg-surface-2 px-1.5 py-px text-[11px] text-fg-muted ring-1 ring-line">{{ t('prices.tier.restBadge') }}</span>
          </span>
          <span class="mt-0.5 block text-sm text-fg-muted">{{ condition }}</span>
          <span class="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 text-xs text-fg-muted">
            <span v-for="s in summary" :key="s.k">{{ s.k }}: <b class="font-medium text-fg">{{ s.v }}</b></span>
            <span class="text-fg-subtle">{{ t(tier.unit === 'request' ? 'prices.per.request' : 'prices.per.token') }}</span>
          </span>
        </span>
        <SIcon name="chevron-down" class="mt-1 h-4 w-4 shrink-0 text-fg-muted transition" :class="open ? 'rotate-180' : ''" />
      </button>
      <SDropdown v-if="!disabled" :actions="menu" width="10rem" @select="onMenu">
        <template #trigger="{ toggle, attrs }">
          <SButton variant="ghost" size="sm" v-bind="attrs" :aria-label="t('ui.moreActions')" @click.stop="toggle"><SIcon name="more" class="h-4 w-4" /></SButton>
        </template>
      </SDropdown>
    </div>

    <!-- body -->
    <div v-if="open" class="space-y-4 border-t border-line px-4 pb-4 pt-3">
      <div>
        <div class="mb-1.5 text-sm font-medium text-fg">{{ t('prices.tier.condition') }}</div>
        <div v-if="only || isRest" class="rounded-lg bg-surface-2 px-3 py-2 text-xs text-fg-muted">
          {{ only ? t('prices.tier.onlyHint') : t('prices.tier.restHint') }}
        </div>
        <template v-else>
          <TierCondTree v-model="tier.when!" :disabled="disabled" />
          <p class="mt-1.5 text-xs text-fg-subtle">{{ t('prices.cond.hint', { depth: MAX_COND_DEPTH, leaves: MAX_COND_LEAVES }) }}</p>
        </template>
      </div>

      <div class="flex flex-wrap items-center gap-2">
        <span class="rounded-md bg-primary-50 px-2 py-1 text-xs text-primary-700 dark:bg-primary-900/40 dark:text-primary-300">{{ t('prices.tier.badge') }}</span>
        <SInput v-model.trim="tier.name" size="sm" mono class="w-44" :disabled="disabled" :aria-label="t('prices.tier.name')" />
        <SSelect v-model="tier.unit" size="sm" class="ml-auto w-36" :options="unitOptions" :disabled="disabled" :aria-label="t('prices.tier.unit')" />
      </div>

      <div v-if="tier.unit === 'request'">
        <div class="mb-2 flex items-center justify-between">
          <span class="text-sm font-semibold text-fg">{{ t('prices.perRequestPrice') }}</span>
          <span class="rounded-md bg-surface-2 px-2 py-0.5 text-[11px] text-fg-muted">${{ t('prices.per.request') }}</span>
        </div>
        <SInput v-model="tier.price" size="sm" type="number" min="0" step="any" class="w-44" :placeholder="t('prices.tier.required')" :disabled="disabled" :aria-label="t('prices.perRequestPrice')" />
        <p class="mt-1 text-xs text-fg-subtle">{{ t('prices.tier.requestHint') }}</p>
      </div>
      <div v-else>
        <div class="mb-2 flex items-center justify-between">
          <span class="text-sm font-semibold text-fg">{{ t('prices.tier.tokenPrices') }}</span>
          <span class="rounded-md bg-surface-2 px-2 py-0.5 text-[11px] text-fg-muted">${{ t('prices.per.token') }}</span>
        </div>
        <div v-for="(row, ri) in TOKEN_ROWS" :key="ri" class="grid grid-cols-2 gap-x-4 gap-y-3 sm:grid-cols-3" :class="ri ? 'mt-3' : ''">
          <div v-for="f in row" :key="f.key" class="min-w-0">
            <SCheckbox :model-value="on(f.key)" size="xs" :label="t(`prices.vars.${f.key}`)" :disabled="disabled" class="mb-1" @update:model-value="setPriceOn(tier, f.key, $event)" />
            <SInput
              v-model="tier[f.key]"
              size="sm"
              type="number"
              min="0"
              step="any"
              :disabled="!on(f.key) || disabled"
              :placeholder="placeholder(f)"
              :aria-label="t(`prices.vars.${f.key}`)"
              :data-testid="`tier-${f.key}`"
            />
          </div>
        </div>
        <button type="button" class="mt-3 inline-flex items-center gap-1 text-xs text-fg-muted hover:text-fg" :aria-expanded="mediaOpen" @click="mediaOpen = !mediaOpen">
          <SIcon :name="mediaOpen ? 'chevron-down' : 'chevron-right'" class="h-3.5 w-3.5" />{{ t('prices.tier.media') }}
        </button>
        <div v-if="mediaOpen" class="mt-2 grid grid-cols-2 gap-x-4 gap-y-3 sm:grid-cols-3">
          <div v-for="f in MEDIA_FIELDS" :key="f.key" class="min-w-0">
            <SCheckbox :model-value="on(f.key)" size="xs" :label="t(`prices.vars.${f.key}`)" :disabled="disabled" class="mb-1" @update:model-value="setPriceOn(tier, f.key, $event)" />
            <SInput
              v-model="tier[f.key]"
              size="sm"
              type="number"
              min="0"
              step="any"
              :disabled="!on(f.key) || disabled"
              :placeholder="placeholder(f)"
              :aria-label="t(`prices.vars.${f.key}`)"
              :data-testid="`tier-${f.key}`"
            />
          </div>
        </div>
      </div>
    </div>
  </li>
</template>
