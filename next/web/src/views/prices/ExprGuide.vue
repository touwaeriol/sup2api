<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { SButton } from '@sub2api/ui'
import { highlight } from './exprSyntax'
import GuideText from './GuideText.vue'
import { EXPR_EXAMPLES } from './presets'

// Usage guide of the expression language, in a drawer beside the editor
// (PriceEditView). Snippets insert at the caret; examples replace the whole
// expression; read-only, the guide only reads. The texts are under prices.guide.
defineProps<{ readonly?: boolean }>()
const emit = defineEmits<{ (e: 'insert', text: string, caret?: number): void; (e: 'replace', text: string): void }>()
const { t } = useI18n()

const SECTIONS = ['basics', 'vars', 'ops', 'fns', 'tiers', 'rules', 'examples', 'check'] as const
const active = ref('basics')
function jump(id: string) {
  active.value = id
  document.getElementById(`expr-guide-${id}`)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

const BASICS = ['cost', 'flat', 'round', 'layout', 'generated']
const VARS = ['p', 'c', 'cr', 'cc', 'cc1h', 'img', 'img_o', 'len']
const OPS: Array<{ id: string; syntax?: string }> = [
  { id: 'arith', syntax: '+  -  *  /  %' },
  { id: 'compare', syntax: '==  !=  <  <=  >  >=' },
  { id: 'logic', syntax: '&&  ||  !' },
  { id: 'ternary' },
  { id: 'coalesce', syntax: 'a ?? b' },
  { id: 'paren', syntax: '( )' }
]
type Fn = { id: string; snippet: string; caret?: number }
const timeFn = (id: string): Fn => ({ id, snippet: `${id}("Asia/Shanghai")` })
const FN_GROUPS: Array<{ id: string; fns: Fn[] }> = [
  {
    id: 'tier',
    fns: [
      { id: 'tier', snippet: 'tier("base", )', caret: 14 },
      { id: 'flat', snippet: 'flat()', caret: 5 }
    ]
  },
  {
    id: 'read',
    fns: [
      { id: 'param', snippet: 'param("")', caret: 7 },
      { id: 'header', snippet: 'header("")', caret: 8 },
      { id: 'u', snippet: 'u("")', caret: 3 },
      { id: 'has', snippet: 'has(, "")', caret: 4 }
    ]
  },
  { id: 'time', fns: ['hour', 'minute', 'weekday', 'day', 'month'].map(timeFn) },
  {
    id: 'math',
    fns: [
      { id: 'maxMin', snippet: 'max(, )', caret: 4 },
      { id: 'rounding', snippet: 'ceil()', caret: 5 }
    ]
  }
]
const TIERS = ['wrap', 'len', 'any', 'record']
const RULES = ['trace', 'negative', 'nil']
const CHECKS = ['syntax', 'fields', 'smoke', 'cost']

const RULE_SAMPLE = 'tier("base", p*3 + c*15)\n* (param("service_tier") == "priority" ? 2 : 1)\n* (hour("Asia/Shanghai") < 8 ? 0.5 : 1)'

const EXAMPLES = Object.entries(EXPR_EXAMPLES).map(([id, src]) => ({ id, src }))
</script>

<template>
  <div class="min-w-0" data-testid="expr-guide">
    <nav class="sticky -top-px z-10 -mx-1 mb-4 flex flex-wrap gap-1.5 border-b border-line bg-surface px-1 pb-3 pt-1">
      <button
        v-for="id in SECTIONS"
        :key="id"
        type="button"
        class="rounded-full border px-2.5 py-0.5 text-xs"
        :aria-current="active === id ? 'location' : undefined"
        :class="active === id ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-900/40 dark:text-primary-300' : 'border-line text-fg-muted hover:border-primary-400'"
        @click="jump(id)"
      >
        {{ t(`prices.guide.nav.${id}`) }}
      </button>
    </nav>

    <div class="space-y-7 text-sm leading-6 text-fg">
      <section id="expr-guide-basics" class="scroll-mt-24">
        <h3 class="mb-2 font-semibold">{{ t('prices.guide.nav.basics') }}</h3>
        <ul class="list-disc space-y-1 pl-5 text-fg-muted">
          <li v-for="k in BASICS" :key="k"><GuideText :text="t(`prices.guide.basics.${k}`)" /></li>
        </ul>
      </section>

      <section id="expr-guide-vars" class="scroll-mt-24">
        <h3 class="mb-2 font-semibold">{{ t('prices.guide.nav.vars') }}</h3>
        <dl class="grid grid-cols-[4.5rem_1fr] gap-x-3 gap-y-1.5">
          <template v-for="k in VARS" :key="k">
            <dt>
              <code v-if="readonly" class="code">{{ k }}</code>
              <button v-else type="button" class="code hover:ring-1 hover:ring-primary-400" @click="emit('insert', k)">{{ k }}</button>
            </dt>
            <dd class="text-fg-muted">{{ t(`prices.guide.var.${k}`) }}</dd>
          </template>
        </dl>
        <p class="mt-2 text-xs text-fg-muted">{{ t('prices.guide.varsNote') }}</p>
      </section>

      <section id="expr-guide-ops" class="scroll-mt-24">
        <h3 class="mb-2 font-semibold">{{ t('prices.guide.nav.ops') }}</h3>
        <dl class="grid grid-cols-[10rem_1fr] gap-x-3 gap-y-1.5">
          <template v-for="o in OPS" :key="o.id">
            <dt class="whitespace-pre font-mono text-xs leading-6">{{ o.syntax ?? t('prices.guide.ternary') }}</dt>
            <dd class="text-fg-muted">{{ t(`prices.guide.op.${o.id}`) }}</dd>
          </template>
        </dl>
        <p class="mt-2 text-xs text-fg-muted">{{ t('prices.guide.opsNote') }}</p>
      </section>

      <section id="expr-guide-fns" class="scroll-mt-24">
        <h3 class="mb-2 font-semibold">{{ t('prices.guide.nav.fns') }}</h3>
        <div v-for="g in FN_GROUPS" :key="g.id" class="mb-4 last:mb-0">
          <div class="mb-1.5 text-xs font-medium text-fg-muted">{{ t(`prices.guide.fnGroup.${g.id}`) }}</div>
          <ul class="divide-y divide-line rounded-lg border border-line">
            <li v-for="f in g.fns" :key="f.id" class="flex items-start gap-3 px-3 py-2">
              <div class="min-w-0 flex-1">
                <code class="font-mono text-xs" v-html="highlight(t(`prices.guide.fn.${f.id}.sig`), -1, true)"></code>
                <div class="text-xs text-fg-muted">{{ t(`prices.guide.fn.${f.id}.desc`) }}</div>
              </div>
              <SButton v-if="!readonly" size="xs" variant="ghost" @click="emit('insert', f.snippet, f.caret)">{{ t('prices.guide.insert') }}</SButton>
            </li>
          </ul>
        </div>
        <p class="text-xs text-fg-muted">{{ t('prices.guide.fnsNote') }}</p>
      </section>

      <section id="expr-guide-tiers" class="scroll-mt-24">
        <h3 class="mb-2 font-semibold">{{ t('prices.guide.nav.tiers') }}</h3>
        <ul class="list-disc space-y-1 pl-5 text-fg-muted">
          <li v-for="k in TIERS" :key="k"><GuideText :text="t(`prices.guide.tiers.${k}`)" /></li>
        </ul>
      </section>

      <section id="expr-guide-rules" class="scroll-mt-24">
        <h3 class="mb-2 font-semibold">{{ t('prices.guide.nav.rules') }}</h3>
        <p class="text-fg-muted"><GuideText :text="t('prices.guide.rules.intro')" /></p>
        <pre
          class="my-2 overflow-x-auto rounded-lg bg-surface-2 px-3 py-2 font-mono text-xs leading-5"
          v-html="highlight(RULE_SAMPLE)"
        ></pre>
        <ul class="list-disc space-y-1 pl-5 text-fg-muted">
          <li v-for="k in RULES" :key="k">{{ t(`prices.guide.rules.${k}`) }}</li>
        </ul>
      </section>

      <section id="expr-guide-examples" class="scroll-mt-24">
        <h3 class="mb-2 font-semibold">{{ t('prices.guide.nav.examples') }}</h3>
        <div class="grid grid-cols-1 gap-3">
          <div v-for="x in EXAMPLES" :key="x.id" class="min-w-0 overflow-hidden rounded-lg border border-line">
            <div class="flex items-center justify-between gap-3 px-3 pt-2">
              <div class="min-w-0">
                <div class="text-sm font-medium">{{ t(`prices.guide.example.${x.id}.title`) }}</div>
                <div class="text-xs text-fg-muted">{{ t(`prices.guide.example.${x.id}.desc`) }}</div>
              </div>
              <SButton v-if="!readonly" size="xs" @click="emit('replace', x.src)">{{ t('prices.guide.fill') }}</SButton>
            </div>
            <pre class="m-0 overflow-x-auto px-3 pb-2.5 pt-1.5 font-mono text-xs leading-5" v-html="highlight(x.src)"></pre>
          </div>
        </div>
      </section>

      <section id="expr-guide-check" class="scroll-mt-24">
        <h3 class="mb-2 font-semibold">{{ t('prices.guide.nav.check') }}</h3>
        <p class="mb-1 text-fg-muted">{{ t('prices.guide.check.intro') }}</p>
        <ol class="list-decimal space-y-1 pl-5 text-fg-muted">
          <li v-for="k in CHECKS" :key="k"><GuideText :text="t(`prices.guide.check.${k}`)" /></li>
        </ol>
      </section>
    </div>
  </div>
</template>

<style scoped>
.code {
  @apply rounded bg-surface-2 px-1 py-px font-mono text-xs text-fg;
}
</style>
