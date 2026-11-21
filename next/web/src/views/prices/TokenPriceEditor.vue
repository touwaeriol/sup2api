<script setup lang="ts">
import { ref, toRaw } from 'vue'
import { useI18n } from 'vue-i18n'
import { SButton } from '@sub2api/ui'
import type { VisualConfig, TokenTier } from './priceExpr'
import { PRESETS, TIER_PRESET_GROUPS } from './presets'
import { addTier, canMoveTier, moveTier, removeTier } from './tokenPrice'
import PricePresets from './PricePresets.vue'
import PriceRuleGroups from './PriceRuleGroups.vue'
import PriceTierCard from './PriceTierCard.vue'

// The visual price editor: preset templates, the tiers
// as numbered cards matched in order (the last one the rest; the others move
// up and down before it, and a new one goes in just before it), and the
// request rule groups. Edits the bound config in place. Read-only, the
// cards still open and close: each part disables its own controls.
const model = defineModel<VisualConfig>({ required: true })
defineProps<{ disabled?: boolean }>()
const { t } = useI18n()

// A card is keyed by its tier object, so a card's own state (its media
// prices open) stays with its tier when one before it goes. The key is held
// here, never in the config that is saved.
const keys = new WeakMap<TokenTier, number>()
let lastKey = 0
function keyOf(tier: TokenTier): number {
  const raw = toRaw(tier)
  if (!keys.has(raw)) keys.set(raw, ++lastKey)
  return keys.get(raw)!
}

const open = ref<number | null>(0)
function applyPreset(key: string) {
  model.value.tiers = PRESETS[key]()
  open.value = 0
}
function add() {
  addTier(model.value.tiers)
  open.value = model.value.tiers.length - 2
}
function move(i: number, by: number) {
  if (!canMoveTier(model.value.tiers, i, by)) return
  moveTier(model.value.tiers, i, by)
  // The open card stays open where its tier goes.
  if (open.value === i) open.value = i + by
  else if (open.value === i + by) open.value = i
}
function remove(i: number) {
  removeTier(model.value.tiers, i)
  open.value = null
}
</script>

<template>
  <div class="min-w-0 space-y-4" data-testid="token-price-editor">
    <PricePresets :groups="TIER_PRESET_GROUPS" :disabled="disabled" @pick="applyPreset" />

    <div class="space-y-4 rounded-xl border border-line p-3 sm:p-4">
      <div>
        <p class="mb-2 text-xs text-fg-muted">{{ t('prices.tier.listHint') }}</p>
        <ul class="space-y-2.5" data-testid="token-tier-list">
          <PriceTierCard
            v-for="(tier, i) in model.tiers"
            :key="keyOf(tier)"
            v-model="model.tiers[i]"
            :open="open === i"
            :index="i"
            :count="model.tiers.length"
            :can-up="canMoveTier(model.tiers, i, -1)"
            :can-down="canMoveTier(model.tiers, i, 1)"
            :disabled="disabled"
            @update:open="open = $event ? i : null"
            @move="move(i, $event)"
            @remove="remove(i)"
          />
        </ul>
        <SButton v-if="!disabled" size="sm" variant="ghost" icon="plus" class="mt-2" data-testid="token-add-tier" @click="add">{{ t('prices.tier.add') }}</SButton>
      </div>
      <div class="border-t border-line pt-4">
        <PriceRuleGroups v-model="model.rules" :disabled="disabled" />
      </div>
    </div>
  </div>
</template>
