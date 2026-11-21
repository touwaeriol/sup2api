<script setup lang="ts">
import { computed } from 'vue'
import { SInput, SLink } from '@sub2api/ui'
import { formatMoney } from '@/utils/format'
import { isSet, num } from './priceExpr'

// The per_request mode: one price per call, common prices to pick and what it
// comes to at volume. Surcharges and mixed tiers live in per_token.
// PROTOTYPE: new texts are Chinese literals until adopted.
const price = defineModel<number | null>({ required: true })
defineProps<{ disabled?: boolean; error?: string }>()
const emit = defineEmits<{ (e: 'to-token'): void }>()

const COMMON = [0.001, 0.005, 0.01, 0.02, 0.04, 0.1]
const p = computed(() => (isSet(price.value) ? num(price.value) : null))
const volumes = computed(() => (p.value === null ? [] : [['1,000 次', 1e3], ['10 万次', 1e5], ['100 万次', 1e6]].map(([l, n]) => ({ label: l as string, v: p.value! * (n as number) }))))
</script>

<template>
  <div class="rounded-xl border border-line p-3 sm:p-4" data-testid="per-request-editor">
    <div class="mb-2 flex items-center justify-between">
      <label class="text-sm font-semibold text-fg" for="per-request-price">每次价格</label>
      <span class="rounded-md bg-surface-2 px-2 py-0.5 text-[11px] text-fg-muted">$/次</span>
    </div>
    <SInput id="per-request-price" v-model="price" type="number" min="0" step="any" class="max-w-xs" placeholder="0" :disabled="disabled" />
    <p v-if="error" class="mt-1 text-xs text-danger-600">{{ error }}</p>
    <div class="mt-2 flex flex-wrap items-center gap-1.5">
      <span class="text-xs text-fg-muted">常用</span>
      <button
        v-for="c in COMMON"
        :key="c"
        type="button"
        :disabled="disabled"
        class="rounded-full border px-2.5 py-0.5 font-mono text-xs"
        :class="p === c ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-900/40 dark:text-primary-300' : 'border-line text-fg hover:border-primary-400'"
        @click="price = c"
      >
        ${{ c }}
      </button>
    </div>
    <p class="mt-3 text-xs text-fg-muted">
      每次成功的请求收这个价，不看 token 数。
      <template v-if="volumes.length">
        <span v-for="x in volumes" :key="x.label" class="ml-2">{{ x.label }} <b class="font-mono text-fg">{{ formatMoney(x.v) }}</b></span>
      </template>
    </p>
    <p v-if="!disabled" class="mt-1 text-xs text-fg-muted">
      要按条件加价，或短请求按次、长请求按 token？
      <SLink as="button" class="text-xs" data-testid="per-request-to-token" @click="emit('to-token')">改用按 token，保留这个单价</SLink>
    </p>
  </div>
</template>
