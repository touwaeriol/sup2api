<script setup lang="ts">
import { SButton, SDropdown, SIcon, SInput, SSelect } from '@sub2api/ui'
import { NUMERIC_OPS, type CondOp, type CondSource, type FieldCond, type MarkupRule } from './priceExpr'
import { newCond, setOp } from './tokenPrice'

// 请求规则计费: groups of AND conditions; a group that matches multiplies the
// whole charge by its multiplier, and the multipliers of every group that
// matches multiply. A value is held as typed (compactTokenConfig types it).
// PROTOTYPE: new texts are Chinese literals until adopted.
const rules = defineModel<MarkupRule[]>({ required: true })
defineProps<{ disabled?: boolean }>()

const SOURCE_OPTIONS = [
  { value: 'param', label: '请求体参数' },
  { value: 'header', label: '请求头' },
  { value: 'usage', label: '用量字段' },
  { value: 'time', label: '时间' }
]
const OP_LABEL: Record<CondOp, string> = { eq: '等于', ne: '不等于', contains: '包含', exists: '存在', gt: '>', gte: '≥', lt: '<', lte: '≤' }
const OPS = Object.entries(OP_LABEL).map(([value, label]) => ({ value: value as CondOp, label }))
const opsOf = (s: FieldCond['source']) => (s === 'header' ? OPS.filter(o => !NUMERIC_OPS.includes(o.value)) : OPS)
const KEY_PLACEHOLDER: Record<FieldCond['source'], string> = { param: 'service_tier', header: 'anthropic-beta', usage: 'service_tier' }
const HOURS = Array.from({ length: 25 }, (_, h) => ({ value: h, label: `${h}:00` }))

const setSource = (r: MarkupRule, i: number, s: CondSource) => r.conds.splice(i, 1, newCond(s))
function addGroup() {
  rules.value.push({ name: `规则组 ${rules.value.length + 1}`, multiplier: 1, conds: [newCond('param')] })
}

// Common groups to start from.
const COMMON: Record<string, { label: string; make: () => MarkupRule }> = {
  priority: {
    label: 'OpenAI priority ×2（请求体 service_tier）',
    make: () => ({ name: 'priority', multiplier: 2, conds: [{ source: 'param', key: 'service_tier', op: 'eq', value: 'priority' }] })
  },
  flex: {
    label: 'OpenAI flex ×0.5',
    make: () => ({ name: 'flex', multiplier: 0.5, conds: [{ source: 'param', key: 'service_tier', op: 'eq', value: 'flex' }] })
  },
  fast: {
    label: 'Claude fast mode ×2（请求头）',
    make: () => ({ name: 'fast mode', multiplier: 2, conds: [{ source: 'header', key: 'anthropic-beta', op: 'contains', value: 'fast-mode' }] })
  },
  night: {
    label: '夜间 0–8 点 ×0.8（上海时间）',
    make: () => ({ name: '夜间折扣', multiplier: 0.8, conds: [{ source: 'time', tz: 'Asia/Shanghai', from_hour: 0, to_hour: 8 }] })
  }
}
const commonActions = Object.entries(COMMON).map(([key, x]) => ({ key, label: x.label }))
const addCommon = (key: string) => COMMON[key] && rules.value.push(COMMON[key].make())
</script>

<template>
  <section data-testid="price-rule-groups">
    <div class="text-sm font-medium text-fg">请求规则计费</div>
    <p class="mt-0.5 text-xs text-fg-muted">条件满足时，最终价格乘以倍率；多组命中时倍率相乘；小于 1 的倍率为折扣。</p>

    <ul class="mt-3 space-y-3">
      <li v-for="(r, ri) in rules" :key="ri" class="rounded-xl border border-line bg-surface-2/40 p-3" :data-rule="ri">
        <div class="mb-2.5 flex items-center gap-2">
          <span class="rounded-md bg-surface px-2 py-0.5 text-xs text-fg-muted ring-1 ring-line">规则组 #{{ ri + 1 }}</span>
          <SInput v-model="r.name" size="sm" class="w-40" aria-label="规则组名称" placeholder="名称（可选）" />
          <SButton v-if="!disabled" variant="ghost" size="sm" tone="danger" icon="trash" class="ml-auto" aria-label="删除规则组" @click="rules.splice(ri, 1)" />
        </div>

        <div class="space-y-2">
          <div v-for="(c, ci) in r.conds" :key="ci" class="flex flex-wrap items-center gap-2" :data-cond="ci">
            <SSelect :model-value="c.source" size="sm" class="w-28" :options="SOURCE_OPTIONS" aria-label="条件来源" @update:model-value="setSource(r, ci, $event as CondSource)" />
            <template v-if="c.source === 'time'">
              <SInput v-model="c.tz" size="sm" mono class="w-36" placeholder="Asia/Shanghai" aria-label="时区" />
              <SSelect v-model="c.from_hour" size="sm" class="w-24" :options="HOURS.slice(0, 24)" aria-label="开始" />
              <span class="text-xs text-fg-muted">到</span>
              <SSelect v-model="c.to_hour" size="sm" class="w-24" :options="HOURS.slice(1)" aria-label="结束" />
            </template>
            <template v-else>
              <SInput v-model="c.key" size="sm" mono class="min-w-0 flex-1 basis-32" :placeholder="KEY_PLACEHOLDER[c.source]" aria-label="字段" />
              <SSelect :model-value="c.op" size="sm" class="w-24" :options="opsOf(c.source)" aria-label="比较" @update:model-value="setOp(c, $event as CondOp)" />
              <SInput
                v-if="c.op !== 'exists'"
                :model-value="String(c.value ?? '')"
                size="sm"
                mono
                class="min-w-0 flex-1 basis-28"
                placeholder="值"
                aria-label="值"
                @update:model-value="c.value = String($event ?? '')"
              />
            </template>
            <SButton v-if="!disabled" variant="ghost" size="sm" tone="danger" icon="trash" aria-label="删除条件" :disabled="r.conds.length === 1" @click="r.conds.splice(ci, 1)" />
          </div>
        </div>

        <div v-if="!disabled" class="mt-2 flex flex-wrap gap-1">
          <SButton variant="ghost" size="sm" icon="plus" @click="r.conds.push(newCond('param'))">新增参数 / 请求头</SButton>
          <SButton variant="ghost" size="sm" icon="plus" @click="r.conds.push(newCond('time'))">新增时间条件</SButton>
        </div>

        <div class="mt-2 flex flex-wrap items-center gap-2 border-t border-line pt-2.5">
          <span class="text-sm text-fg">倍率</span>
          <SInput v-model="r.multiplier" size="sm" type="number" min="0" step="any" class="w-24" aria-label="倍率" />
          <span class="text-xs text-fg-muted">条件全部满足时，最终费用 = 基础费用 × 倍率</span>
        </div>
      </li>
    </ul>

    <div v-if="!disabled" class="mt-3 flex flex-wrap items-center gap-2">
      <SButton size="sm" icon="plus" data-testid="token-add-rule" @click="addGroup">新增规则组</SButton>
      <SDropdown :actions="commonActions" width="19rem" align="start" @select="addCommon">
        <template #trigger="{ toggle, attrs }">
          <SButton size="sm" variant="ghost" v-bind="attrs" @click.stop="toggle">常用规则<SIcon name="chevron-down" class="ml-1 h-3.5 w-3.5" /></SButton>
        </template>
      </SDropdown>
    </div>
  </section>
</template>
