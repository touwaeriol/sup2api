<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SButton, SDropdown, SIcon, SInput, SSelect } from '@sub2api/ui'
import { NUMERIC_OPS, type CondOp, type CondSource, type FieldCond, type MarkupRule } from './priceExpr'
import { newCond, setOp } from './tokenPrice'

// Request rules: groups of AND conditions; a group that matches multiplies the
// whole charge by its multiplier, and the multipliers of every group that
// matches multiply. A value is held as typed (compactTokenConfig types it).
const rules = defineModel<MarkupRule[]>({ required: true })
defineProps<{ disabled?: boolean }>()
const { t } = useI18n()

const SOURCES: CondSource[] = ['param', 'header', 'usage', 'time']
const OPS: CondOp[] = ['eq', 'ne', 'contains', 'exists', 'gt', 'gte', 'lt', 'lte']
const sourceOptions = computed(() => SOURCES.map(value => ({ value, label: t(`prices.rules.source.${value}`) })))
const opOptions = computed(() => OPS.map(value => ({ value, label: t(`prices.rules.ops.${value}`) })))
const opsOf = (s: FieldCond['source']) => (s === 'header' ? opOptions.value.filter(o => !NUMERIC_OPS.includes(o.value)) : opOptions.value)
const KEY_PLACEHOLDER: Record<FieldCond['source'], string> = { param: 'service_tier', header: 'anthropic-beta', usage: 'service_tier' }
const HOURS = Array.from({ length: 25 }, (_, h) => ({ value: h, label: `${h}:00` }))

const setSource = (r: MarkupRule, i: number, s: CondSource) => r.conds.splice(i, 1, newCond(s))
function addGroup() {
  rules.value.push({ name: t('prices.rules.defaultName', { n: rules.value.length + 1 }), multiplier: 1, conds: [newCond('param')] })
}

// Common groups to start from (named by prices.rules.preset.<key>).
const COMMON: Record<string, () => MarkupRule> = {
  priority: () => ({ name: 'priority', multiplier: 2, conds: [{ source: 'param', key: 'service_tier', op: 'eq', value: 'priority' }] }),
  flex: () => ({ name: 'flex', multiplier: 0.5, conds: [{ source: 'param', key: 'service_tier', op: 'eq', value: 'flex' }] }),
  fast: () => ({ name: 'fast mode', multiplier: 2, conds: [{ source: 'header', key: 'anthropic-beta', op: 'contains', value: 'fast-mode' }] }),
  night: () => ({ name: 'night', multiplier: 0.8, conds: [{ source: 'time', tz: 'Asia/Shanghai', from_hour: 0, to_hour: 8 }] })
}
const commonActions = computed(() => Object.keys(COMMON).map(key => ({ key, label: t(`prices.rules.preset.${key}`) })))
const addCommon = (key: string) => COMMON[key] && rules.value.push(COMMON[key]())
</script>

<template>
  <section data-testid="price-rule-groups">
    <div class="text-sm font-medium text-fg">{{ t('prices.rules.title') }}</div>
    <p class="mt-0.5 text-xs text-fg-muted">{{ t('prices.rules.hint') }}</p>

    <ul class="mt-3 space-y-3">
      <li v-for="(r, ri) in rules" :key="ri" class="rounded-xl border border-line bg-surface-2/40 p-3" :data-rule="ri">
        <div class="mb-2.5 flex items-center gap-2">
          <span class="rounded-md bg-surface px-2 py-0.5 text-xs text-fg-muted ring-1 ring-line">{{ t('prices.rules.group', { n: ri + 1 }) }}</span>
          <SInput v-model="r.name" size="sm" class="w-40" :aria-label="t('prices.rules.name')" :placeholder="t('prices.rules.namePlaceholder')" />
          <SButton v-if="!disabled" variant="ghost" size="sm" tone="danger" icon="trash" class="ml-auto" :aria-label="t('prices.rules.remove')" @click="rules.splice(ri, 1)" />
        </div>

        <div class="space-y-2">
          <div v-for="(c, ci) in r.conds" :key="ci" class="flex flex-wrap items-center gap-2" :data-cond="ci">
            <SSelect :model-value="c.source" size="sm" class="w-28" :options="sourceOptions" :aria-label="t('prices.rules.condSource')" @update:model-value="setSource(r, ci, $event as CondSource)" />
            <template v-if="c.source === 'time'">
              <SInput v-model="c.tz" size="sm" mono class="w-36" placeholder="Asia/Shanghai" :aria-label="t('prices.rules.tz')" />
              <SSelect v-model="c.from_hour" size="sm" class="w-24" :options="HOURS.slice(0, 24)" :aria-label="t('prices.rules.fromHour')" />
              <span class="text-xs text-fg-muted">{{ t('prices.rules.to') }}</span>
              <SSelect v-model="c.to_hour" size="sm" class="w-24" :options="HOURS.slice(1)" :aria-label="t('prices.rules.toHour')" />
            </template>
            <template v-else>
              <SInput v-model="c.key" size="sm" mono class="min-w-0 flex-1 basis-32" :placeholder="KEY_PLACEHOLDER[c.source]" :aria-label="t('prices.rules.field')" />
              <SSelect :model-value="c.op" size="sm" class="w-24" :options="opsOf(c.source)" :aria-label="t('prices.rules.op')" @update:model-value="setOp(c, $event as CondOp)" />
              <SInput
                v-if="c.op !== 'exists'"
                :model-value="String(c.value ?? '')"
                size="sm"
                mono
                class="min-w-0 flex-1 basis-28"
                :placeholder="t('prices.rules.value')"
                :aria-label="t('prices.rules.value')"
                @update:model-value="c.value = String($event ?? '')"
              />
            </template>
            <SButton v-if="!disabled" variant="ghost" size="sm" tone="danger" icon="trash" :aria-label="t('prices.rules.removeCond')" :disabled="r.conds.length === 1" @click="r.conds.splice(ci, 1)" />
          </div>
        </div>

        <div v-if="!disabled" class="mt-2 flex flex-wrap gap-1">
          <SButton variant="ghost" size="sm" icon="plus" @click="r.conds.push(newCond('param'))">{{ t('prices.rules.addField') }}</SButton>
          <SButton variant="ghost" size="sm" icon="plus" @click="r.conds.push(newCond('time'))">{{ t('prices.rules.addTime') }}</SButton>
        </div>

        <div class="mt-2 flex flex-wrap items-center gap-2 border-t border-line pt-2.5">
          <span class="text-sm text-fg">{{ t('prices.rules.multiplier') }}</span>
          <SInput v-model="r.multiplier" size="sm" type="number" min="0" step="any" class="w-24" :aria-label="t('prices.rules.multiplier')" />
          <span class="text-xs text-fg-muted">{{ t('prices.rules.multiplierHint') }}</span>
        </div>
      </li>
    </ul>

    <div v-if="!disabled" class="mt-3 flex flex-wrap items-center gap-2">
      <SButton size="sm" icon="plus" data-testid="token-add-rule" @click="addGroup">{{ t('prices.rules.add') }}</SButton>
      <SDropdown :actions="commonActions" width="19rem" align="start" @select="addCommon">
        <template #trigger="{ toggle, attrs }">
          <SButton size="sm" variant="ghost" v-bind="attrs" @click.stop="toggle">{{ t('prices.rules.common') }}<SIcon name="chevron-down" class="ml-1 h-3.5 w-3.5" /></SButton>
        </template>
      </SDropdown>
    </div>
  </section>
</template>
