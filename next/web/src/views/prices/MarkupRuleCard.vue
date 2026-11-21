<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SButton, SInput, SSelect } from '@sub2api/ui'
import { NUMERIC_OPS, type CondOp, type CondSource, type FieldCond, type MarkupRule } from './priceExpr'
import { newCond, setOp } from './tokenPrice'

// One surcharge rule of a per_token price: its name, the multiplier and the
// conditions that must all hold. A value is held as typed (compactTokenConfig types it).
const rule = defineModel<MarkupRule>({ required: true })
defineProps<{ index: number }>()
const emit = defineEmits<{ (e: 'remove'): void }>()
const { t } = useI18n()

const SOURCES: CondSource[] = ['param', 'header', 'usage', 'time']
const OPS: CondOp[] = ['eq', 'ne', 'contains', 'exists', 'gt', 'gte', 'lt', 'lte']
const KEY_PLACEHOLDER: Record<FieldCond['source'], string> = { param: 'service_tier', header: 'anthropic-beta', usage: 'service_tier' }
const sourceOptions = computed(() => SOURCES.map(value => ({ value, label: t(`prices.token.source.${value}`) })))
const opOptions = computed(() => OPS.map(value => ({ value, label: t(`prices.token.op.${value}`) })))
/** A header is text: it takes no number comparison. */
const opsOf = (source: FieldCond['source']) =>
  source === 'header' ? opOptions.value.filter(o => !NUMERIC_OPS.includes(o.value)) : opOptions.value
const hours = computed(() => Array.from({ length: 25 }, (_, h) => ({ value: h, label: t('prices.token.hour', { h }) })))

const setSource = (i: number, source: CondSource) => rule.value.conds.splice(i, 1, newCond(source))

// PROTOTYPE: the rule read back as a sentence, and its effect as a percentage.
const OPWORD: Record<CondOp, string> = { eq: '等于', ne: '不等于', contains: '包含', exists: '存在', gt: '大于', gte: '不小于', lt: '小于', lte: '不大于' }
const SRCWORD = { param: '请求参数', header: '请求头', usage: '用量字段' } as const
const summary = computed(() => {
  const parts = rule.value.conds.map((c) => {
    if (c.source === 'time') return `${c.tz || 'UTC'} 时间 ${c.from_hour}:00–${c.to_hour}:00`
    const key = c.key || '…'
    return c.op === 'exists' ? `${SRCWORD[c.source]} ${key} 存在` : `${SRCWORD[c.source]} ${key} ${OPWORD[c.op]} ${String(c.value ?? '') || '…'}`
  })
  return `${parts.join('，且 ')} 时，整单 ×${rule.value.multiplier ?? '…'}`
})
const effect = computed(() => {
  const m = Number(rule.value.multiplier)
  if (!Number.isFinite(m)) return null
  const pct = Math.round((m - 1) * 1000) / 10
  return pct === 0 ? { text: '不变', tone: 'muted' } : pct > 0 ? { text: `加价 ${pct}%`, tone: 'up' } : { text: m === 0 ? '免费' : `打 ${+(m * 10).toFixed(2)} 折`, tone: 'down' }
})
</script>

<template>
  <li class="rounded-xl border border-line bg-surface" :data-rule="index">
    <div class="flex flex-wrap items-center gap-3 border-b border-line px-4 py-2.5">
      <SInput v-model="rule.name" size="sm" class="w-44" :aria-label="t('prices.token.ruleName')" />
      <span
        v-if="effect"
        class="rounded-full px-2 py-0.5 text-xs font-medium"
        :class="{
          'bg-danger-50 text-danger-700 dark:bg-danger-900/40 dark:text-danger-300': effect.tone === 'up',
          'bg-success-50 text-success-700 dark:bg-success-900/40 dark:text-success-300': effect.tone === 'down',
          'bg-surface-2 text-fg-muted': effect.tone === 'muted'
        }"
        >{{ effect.text }}</span
      >
      <div class="ml-auto flex items-center gap-2">
        <span class="text-sm text-fg-muted">整单 ×</span>
        <SInput
          v-model="rule.multiplier"
          size="sm"
          type="number"
          min="0"
          step="any"
          class="w-24"
          :aria-label="t('prices.token.multiplier')"
        />
        <SButton variant="ghost" size="sm" tone="danger" icon="trash" :aria-label="t('prices.token.removeRule')" @click="emit('remove')" />
      </div>
    </div>
    <div class="space-y-2 px-4 py-3">
      <div v-for="(c, ci) in rule.conds" :key="ci" class="flex flex-wrap items-center gap-2" :data-cond="ci">
        <span class="w-6 text-right text-xs text-fg-muted">{{ ci === 0 ? t('prices.token.when') : t('prices.token.and') }}</span>
        <SSelect
          :model-value="c.source"
          size="sm"
          class="w-28"
          :options="sourceOptions"
          :aria-label="t('prices.token.condSource')"
          @update:model-value="setSource(ci, $event as CondSource)"
        />
        <template v-if="c.source === 'time'">
          <SInput v-model="c.tz" size="sm" mono class="w-40" placeholder="Asia/Shanghai" :aria-label="t('prices.token.tz')" />
          <SSelect v-model="c.from_hour" size="sm" class="w-24" :options="hours.slice(0, 24)" :aria-label="t('prices.token.from')" />
          <span class="text-xs text-fg-muted">{{ t('prices.token.to') }}</span>
          <SSelect v-model="c.to_hour" size="sm" class="w-24" :options="hours.slice(1)" :aria-label="t('prices.token.to')" />
        </template>
        <template v-else>
          <SInput
            v-model="c.key"
            size="sm"
            mono
            class="w-44"
            :placeholder="KEY_PLACEHOLDER[c.source]"
            :aria-label="t('prices.token.condKey')"
          />
          <SSelect
            :model-value="c.op"
            size="sm"
            class="w-24"
            :options="opsOf(c.source)"
            :aria-label="t('prices.token.condOp')"
            @update:model-value="setOp(c, $event as CondOp)"
          />
          <SInput
            v-if="c.op !== 'exists'"
            :model-value="String(c.value ?? '')"
            size="sm"
            mono
            class="w-40"
            :placeholder="t('prices.token.value')"
            :aria-label="t('prices.token.value')"
            @update:model-value="c.value = String($event ?? '')"
          />
        </template>
        <SButton
          v-if="rule.conds.length > 1"
          variant="ghost"
          size="sm"
          icon="x"
          :aria-label="t('prices.token.removeCond')"
          @click="rule.conds.splice(ci, 1)"
        />
      </div>
      <SButton variant="ghost" size="sm" icon="plus" class="ml-8" @click="rule.conds.push(newCond('param'))">{{
        t('prices.token.addCond')
      }}</SButton>
    </div>
    <div class="rounded-b-xl border-t border-line bg-surface-2 px-4 py-2 text-xs text-fg-muted" data-testid="rule-summary">{{ summary }}</div>
  </li>
</template>
