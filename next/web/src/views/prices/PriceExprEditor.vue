<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { SButton, SField, SHint, STextarea } from '@sub2api/ui'
import { PRICE_KEYS } from './priceExpr'

// The expression mode: the source as written, the variables and functions it
// may use (beside it on the widest screens, under it elsewhere), and examples
// that fill it.
const model = defineModel<string>({ required: true })
defineProps<{ readonly?: boolean; error?: string }>()
const { t } = useI18n()

const VARS = [...PRICE_KEYS, 'len']
const FNS: Array<[string, string]> = [
  ['tier', 'tier(name, cost)'],
  ['flat', 'flat(usd)'],
  ['param', 'param(path)'],
  ['header', 'header(name)'],
  ['u', 'u(field)'],
  ['has', 'has(a, b)'],
  ['hour', 'hour(tz)'],
  ['math', 'max min ceil floor abs']
]
const EXAMPLES: Array<[string, string]> = [
  ['tiers', 'len <= 200000 ? tier("standard", p*3 + c*15) : tier("long", p*6 + c*22.5)'],
  ['priority', 'tier("base", p*2.5 + c*15) * (param("service_tier") == "priority" ? 2 : 1)'],
  ['minimum', 'tier("base", max(p*3 + c*15, 10000))']
]
</script>

<template>
  <div class="grid gap-4 2xl:grid-cols-[minmax(0,1fr)_22rem]" data-testid="price-expr-editor">
    <div class="min-w-0">
      <SField :label="t('prices.expression')" :error="error" :hint="t('prices.expr.hint')">
        <STextarea v-model="model" :rows="10" mono spellcheck="false" class="text-xs" :readonly="readonly" />
      </SField>
      <div v-if="!readonly" class="mt-3">
        <SHint size="xs" class="mb-1">{{ t('prices.expr.examples') }}</SHint>
        <div class="flex flex-wrap gap-2">
          <SButton v-for="[key, src] in EXAMPLES" :key="key" size="xs" :data-example="key" @click="model = src">
            {{ t(`prices.expr.example.${key}`) }}
          </SButton>
        </div>
      </div>
    </div>
    <aside class="rounded-xl border border-line p-3 text-xs">
      <SHint size="xs" class="mb-1.5 font-medium">{{ t('prices.expr.vars') }}</SHint>
      <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
        <template v-for="k in VARS" :key="k">
          <dt class="font-mono text-fg">{{ k }}</dt>
          <dd class="text-fg-muted">{{ t(`prices.expr.var.${k}`) }}</dd>
        </template>
      </dl>
      <SHint size="xs" class="mb-1.5 mt-3 font-medium">{{ t('prices.expr.fns') }}</SHint>
      <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
        <template v-for="[key, sig] in FNS" :key="key">
          <dt class="font-mono text-fg">{{ sig }}</dt>
          <dd class="text-fg-muted">{{ t(`prices.expr.fn.${key}`) }}</dd>
        </template>
      </dl>
    </aside>
  </div>
</template>
