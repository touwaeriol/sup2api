<script setup lang="ts">
import { ref } from 'vue'
import { SButton } from '@sub2api/ui'
import { num, type TokenConfig } from './priceExpr'
import { PRESETS, addTier, removeTier } from './tokenPrice'
import PricePresets from './PricePresets.vue'
import PriceRuleGroups from './PriceRuleGroups.vue'
import PriceTierCard from './PriceTierCard.vue'

// The per_token price editor (the visual editor): preset templates, the tiers
// as numbered cards matched in order (the last one the rest), and the
// request rule groups. Edits the bound config in place.
// PROTOTYPE: new texts are Chinese literals until adopted.
const model = defineModel<TokenConfig>({ required: true })
defineProps<{ disabled?: boolean }>()

const GROUPS = [
  { label: '固定价格', items: [{ key: 'single', label: '单一价格' }, { key: 'claude', label: 'Claude（含缓存）' }, { key: 'image', label: '图片模型' }] },
  { label: '阶梯', items: [{ key: 'claudeLong', label: 'Claude 长上下文' }, { key: 'gptLong', label: 'GPT 长上下文' }, { key: 'three', label: '三档' }] }
]
const open = ref<number | null>(0)
function applyPreset(key: string) {
  model.value.tiers = PRESETS[key]()
  open.value = 0
}
const lower = (i: number) => (i === 0 ? 0 : num(model.value.tiers[i - 1].max_len))
function add() {
  addTier(model.value.tiers)
  open.value = model.value.tiers.length - 2
}
function remove(i: number) {
  removeTier(model.value.tiers, i)
  open.value = null
}
</script>

<template>
  <fieldset :disabled="disabled" class="min-w-0 space-y-4" data-testid="token-price-editor">
    <PricePresets :groups="GROUPS" :disabled="disabled" @pick="applyPreset" />

    <div class="space-y-4 rounded-xl border border-line p-3 sm:p-4">
      <div>
        <p class="mb-2 text-xs text-fg-muted">按顺序匹配档位，第一个满足条件的档位决定价格；最后一档兜底。</p>
        <ul class="space-y-2.5" data-testid="token-tier-list">
          <PriceTierCard
            v-for="(_, i) in model.tiers"
            :key="i"
            v-model="model.tiers[i]"
            :open="open === i"
            :index="i"
            :count="model.tiers.length"
            :lower="lower(i)"
            :disabled="disabled"
            @update:open="open = $event ? i : null"
            @remove="remove(i)"
          />
        </ul>
        <SButton v-if="!disabled" size="sm" variant="ghost" icon="plus" class="mt-2" data-testid="token-add-tier" @click="add">添加档位</SButton>
      </div>
      <div class="border-t border-line pt-4">
        <PriceRuleGroups v-model="model.rules" :disabled="disabled" />
      </div>
    </div>
  </fieldset>
</template>
