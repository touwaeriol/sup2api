<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SButton, SDropdown, SHint, SIcon } from '@sub2api/ui'
import { num, type TokenConfig } from './priceExpr'
import { PRESETS, addTier, newRule, removeTier } from './tokenPrice'
import MarkupRuleCard from './MarkupRuleCard.vue'
import TokenTierCard from './TokenTierCard.vue'

// The per_token price editor: tiers by context length, each priced per token
// or per request, and surcharge rules of AND conditions. Edits the bound
// config in place.
const model = defineModel<TokenConfig>({ required: true })
defineProps<{ disabled?: boolean }>()
const { t } = useI18n()

const presetActions = computed(() => Object.keys(PRESETS).map(key => ({ key, label: t(`prices.token.preset.${key}`) })))
const applyPreset = (key: string) => (model.value.tiers = PRESETS[key]())
const lower = (i: number) => (i === 0 ? 0 : num(model.value.tiers[i - 1].max_len))
const addRule = () => model.value.rules.push(newRule(t('prices.token.ruleDefault', { n: model.value.rules.length + 1 })))
</script>

<template>
  <fieldset :disabled="disabled" class="min-w-0 space-y-6" data-testid="token-price-editor">
    <section>
      <div class="mb-3 flex items-end justify-between gap-3">
        <div class="min-w-0">
          <h4 class="text-sm font-semibold text-fg">{{ t('prices.token.tiers') }}</h4>
          <SHint size="xs">{{ t('prices.token.tiersHint') }}</SHint>
        </div>
        <SDropdown :actions="presetActions" width="18rem" :disabled="disabled" @select="applyPreset">
          <template #trigger="{ toggle, attrs }">
            <SButton size="sm" v-bind="attrs" data-testid="token-presets" @click.stop="toggle">
              {{ t('prices.token.fromPreset') }}<SIcon name="chevron-down" class="ml-1 h-3.5 w-3.5" />
            </SButton>
          </template>
        </SDropdown>
      </div>
      <ol class="space-y-3">
        <TokenTierCard
          v-for="(_, i) in model.tiers"
          :key="i"
          v-model="model.tiers[i]"
          :index="i"
          :count="model.tiers.length"
          :lower="lower(i)"
          @remove="removeTier(model.tiers, i)"
        />
      </ol>
      <SButton size="sm" icon="plus" class="mt-3" data-testid="token-add-tier" @click="addTier(model.tiers)">{{
        t('prices.token.addTier')
      }}</SButton>
    </section>

    <section>
      <div class="mb-3 flex items-end justify-between gap-3">
        <div class="min-w-0">
          <h4 class="text-sm font-semibold text-fg">{{ t('prices.token.rules') }}</h4>
          <SHint size="xs">{{ t('prices.token.rulesHint') }}</SHint>
        </div>
        <SButton size="sm" icon="plus" data-testid="token-add-rule" @click="addRule">{{ t('prices.token.addRule') }}</SButton>
      </div>
      <SHint v-if="!model.rules.length" size="xs">{{ t('prices.token.noRules') }}</SHint>
      <ul class="space-y-3">
        <MarkupRuleCard v-for="(_, i) in model.rules" :key="i" v-model="model.rules[i]" :index="i" @remove="model.rules.splice(i, 1)" />
      </ul>
    </section>
  </fieldset>
</template>
