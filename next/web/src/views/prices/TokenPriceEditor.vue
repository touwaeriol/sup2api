<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SButton, SDropdown, SHint, SIcon } from '@sub2api/ui'
import type { MarkupRule, TokenConfig } from './priceExpr'
import { PRESETS, newRule } from './tokenPrice'
import MarkupRuleCard from './MarkupRuleCard.vue'
import TokenTierTable from './TokenTierTable.vue'

// The per_token price editor: tiers by context length, each priced per token
// or per request, and surcharge rules of AND conditions. Edits the bound
// config in place.
const model = defineModel<TokenConfig>({ required: true })
defineProps<{ disabled?: boolean }>()
const { t } = useI18n()

const presetActions = computed(() => Object.keys(PRESETS).map(key => ({ key, label: t(`prices.token.preset.${key}`) })))
const applyPreset = (key: string) => (model.value.tiers = PRESETS[key]())
const addRule = () => model.value.rules.push(newRule(t('prices.token.ruleDefault', { n: model.value.rules.length + 1 })))

// PROTOTYPE: common surcharge rules to start from.
const COMMON: Record<string, { label: string; make: () => MarkupRule }> = {
  priority: {
    label: 'OpenAI priority（请求里的 service_tier）×2',
    make: () => ({ name: 'priority', multiplier: 2, conds: [{ source: 'param', key: 'service_tier', op: 'eq', value: 'priority' }] })
  },
  priorityUsage: {
    label: 'OpenAI priority（应答里的服务等级）×2',
    make: () => ({ name: 'priority', multiplier: 2, conds: [{ source: 'usage', key: 'service_tier', op: 'eq', value: 'priority' }] })
  },
  flex: {
    label: 'OpenAI flex ×0.5',
    make: () => ({ name: 'flex', multiplier: 0.5, conds: [{ source: 'param', key: 'service_tier', op: 'eq', value: 'flex' }] })
  },
  fast: {
    label: 'Claude fast mode（请求头）×2',
    make: () => ({ name: 'fast mode', multiplier: 2, conds: [{ source: 'header', key: 'anthropic-beta', op: 'contains', value: 'fast-mode' }] })
  },
  night: {
    label: '夜间 0–8 点 ×0.8',
    make: () => ({ name: '夜间折扣', multiplier: 0.8, conds: [{ source: 'time', tz: 'Asia/Shanghai', from_hour: 0, to_hour: 8 }] })
  }
}
const commonActions = Object.entries(COMMON).map(([key, x]) => ({ key, label: x.label }))
const addCommon = (key: string) => COMMON[key] && model.value.rules.push(COMMON[key].make())
</script>

<template>
  <fieldset :disabled="disabled" class="min-w-0 space-y-6" data-testid="token-price-editor">
    <section>
      <div class="mb-3 flex items-end justify-between gap-3">
        <div class="min-w-0">
          <h4 class="text-sm font-semibold text-fg">{{ t('prices.token.tiers') }}</h4>
          <SHint size="xs">按上下文长度 len 分档，每档可以按 token 或按次计。</SHint>
        </div>
        <SDropdown :actions="presetActions" width="18rem" :disabled="disabled" @select="applyPreset">
          <template #trigger="{ toggle, attrs }">
            <SButton size="sm" v-bind="attrs" data-testid="token-presets" @click.stop="toggle">
              {{ t('prices.token.fromPreset') }}<SIcon name="chevron-down" class="ml-1 h-3.5 w-3.5" />
            </SButton>
          </template>
        </SDropdown>
      </div>
      <TokenTierTable v-model="model.tiers" :disabled="disabled" />
    </section>

    <section>
      <div class="mb-3 flex items-end justify-between gap-3">
        <div class="min-w-0">
          <h4 class="text-sm font-semibold text-fg">{{ t('prices.token.rules') }}</h4>
          <SHint size="xs">{{ t('prices.token.rulesHint') }}</SHint>
        </div>
        <div class="flex items-center gap-2">
          <SDropdown :actions="commonActions" width="20rem" :disabled="disabled" @select="addCommon">
            <template #trigger="{ toggle, attrs }">
              <SButton size="sm" v-bind="attrs" @click.stop="toggle">常用规则<SIcon name="chevron-down" class="ml-1 h-3.5 w-3.5" /></SButton>
            </template>
          </SDropdown>
          <SButton size="sm" icon="plus" data-testid="token-add-rule" @click="addRule">{{ t('prices.token.addRule') }}</SButton>
        </div>
      </div>
      <SHint v-if="!model.rules.length" size="xs">{{ t('prices.token.noRules') }}</SHint>
      <ul class="space-y-3">
        <MarkupRuleCard v-for="(_, i) in model.rules" :key="i" v-model="model.rules[i]" :index="i" @remove="model.rules.splice(i, 1)" />
      </ul>
    </section>
  </fieldset>
</template>
