<script setup lang="ts">
import { computed } from 'vue'
import { SInput, SLink } from '@sub2api/ui'
import { formatMoney } from '@/utils/format'
import { isSet, num } from './priceExpr'

// The per_request mode: one price per call, common prices to pick, what it
// comes to at volume, and the way to surcharges (per_token with a per-call
// tier) when one price is not enough.
// PROTOTYPE: new texts are Chinese literals until adopted.
const price = defineModel<number | null>({ required: true })
defineProps<{ disabled?: boolean; error?: string }>()
const emit = defineEmits<{ (e: 'to-token'): void }>()

const COMMON = [0.001, 0.005, 0.01, 0.02, 0.04, 0.1]
const p = computed(() => (isSet(price.value) ? num(price.value) : null))
const volumes = computed(() =>
  p.value === null
    ? []
    : [
        { label: '1,000 次', v: p.value * 1e3 },
        { label: '10 万次', v: p.value * 1e5 },
        { label: '100 万次', v: p.value * 1e6 }
      ]
)
</script>

<template>
  <div class="grid gap-5 md:grid-cols-[minmax(0,20rem)_minmax(0,1fr)]" data-testid="per-request-editor">
    <div>
      <label class="mb-1 block text-sm font-medium text-fg" for="per-request-price">每次价格</label>
      <SInput id="per-request-price" v-model="price" type="number" min="0" step="any" suffix="美元 / 次" :disabled="disabled" class="text-lg" />
      <p v-if="error" class="mt-1 text-xs text-danger-600">{{ error }}</p>
      <div class="mt-2 flex flex-wrap items-center gap-1.5">
        <span class="text-xs text-fg-muted">常用</span>
        <button
          v-for="c in COMMON"
          :key="c"
          type="button"
          :disabled="disabled"
          class="rounded-md border px-2 py-0.5 font-mono text-xs"
          :class="p === c ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-900/40 dark:text-primary-300' : 'border-line text-fg hover:border-primary-400'"
          @click="price = c"
        >
          ${{ c }}
        </button>
      </div>
      <dl v-if="volumes.length" class="mt-4 grid grid-cols-3 gap-2">
        <div v-for="x in volumes" :key="x.label" class="rounded-lg bg-surface-2 px-2.5 py-2">
          <dt class="text-[11px] text-fg-muted">{{ x.label }}</dt>
          <dd class="font-mono text-sm font-semibold text-fg">{{ formatMoney(x.v) }}</dd>
        </div>
      </dl>
    </div>

    <div class="rounded-xl border border-line p-4 text-sm leading-6">
      <div class="mb-1 font-medium text-fg">按次计费</div>
      <ul class="list-disc space-y-1 pl-5 text-fg-muted">
        <li>每次成功的请求收这个价，不看 token 数，适合图片生成、搜索、向量化这类按调用收费的模型。</li>
        <li>用户实付 = 每次价格 × 分组倍率，进一到 8 位小数。</li>
        <li>
          要按条件加价（比如高清图 ×2、priority ×2），或长请求按 token、短请求按次混着收，用“按 token”：把档位设成“按次”再加加价规则。
          <SLink v-if="!disabled" as="button" class="text-sm" data-testid="per-request-to-token" @click="emit('to-token')">改用按 token，保留这个单价</SLink>
        </li>
      </ul>
    </div>
  </div>
</template>
