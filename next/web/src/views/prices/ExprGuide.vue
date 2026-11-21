<script setup lang="ts">
import { ref } from 'vue'
import { SButton } from '@sub2api/ui'
import { highlight } from './exprSyntax'

// Usage guide of the expression language: docked beside the editor on wide
// screens, in a drawer elsewhere (PriceEditView). Snippets insert at the
// caret; examples replace the whole expression.
// PROTOTYPE: Chinese only; the texts move to the locale files when adopted.
const emit = defineEmits<{ (e: 'insert', text: string, caret?: number): void; (e: 'replace', text: string): void }>()

const sections = [
  { id: 'basics', label: '基本写法' },
  { id: 'vars', label: '变量' },
  { id: 'ops', label: '运算符' },
  { id: 'fns', label: '函数' },
  { id: 'tiers', label: '档位' },
  { id: 'rules', label: '加价规则' },
  { id: 'examples', label: '例子' },
  { id: 'check', label: '检查' }
]
const active = ref('basics')
function jump(id: string) {
  active.value = id
  document.getElementById(`expr-guide-${id}`)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

const VARS: Array<[string, string]> = [
  ['p', '输入 token，不含缓存读写，也不含单独计价的图片输入'],
  ['c', '输出 token，不含单独计价的图片输出'],
  ['cr', '缓存读 token'],
  ['cc', '缓存写 token（5 分钟）。表达式没写 cc1h 时，1 小时的缓存写也算在这里'],
  ['cc1h', '缓存写 token（1 小时）'],
  ['img', '图片输入 token'],
  ['img_o', '图片输出 token'],
  ['len', '完整上下文长度 = 输入 + 缓存读写 + 图片输入，分档时用']
]
const OPS: Array<[string, string]> = [
  ['+  -  *  /  %', '加减乘除、取余'],
  ['==  !=  <  <=  >  >=', '比较'],
  ['&&  ||  !', '且、或、非（也可写 and、or、not）'],
  ['条件 ? 甲 : 乙', '条件成立取甲，否则取乙，可以连着写'],
  ['a ?? b', 'a 是空（nil）时取 b，常用于 param() 读不到的情况'],
  ['( )', '括号改变先后顺序']
]
type Fn = { sig: string; desc: string; snippet: string; caret?: number }
const FN_GROUPS: Array<{ title: string; items: Fn[] }> = [
  {
    title: '档位与固定价',
    items: [
      { sig: 'tier("名称", 费用)', desc: '返回费用本身，并把这次请求记为命中这个档位（使用记录里会显示）', snippet: 'tier("base", )', caret: 14 },
      { sig: 'flat(美元)', desc: '固定金额，直接按美元计，不用除以百万', snippet: 'flat()', caret: 5 }
    ]
  },
  {
    title: '读请求和用量',
    items: [
      {
        sig: 'param("路径")',
        desc: '读请求体里的字段，路径用点分隔，如 "reasoning.effort"；"messages.#" 是消息条数。读不到时是 nil',
        snippet: 'param("")',
        caret: 7
      },
      { sig: 'header("名称")', desc: '读请求头，名称不分大小写；没有这个头时是空字符串 ""', snippet: 'header("")', caret: 8 },
      {
        sig: 'u("字段")',
        desc: '读插件报的用量字段（应答里来的，如服务等级、生成秒数）；没报时是 0。字段名必须是插件声明过的',
        snippet: 'u("")',
        caret: 3
      },
      { sig: 'has(文本, "片段")', desc: '文本里含有这个片段时为真，如 has(header("anthropic-beta"), "context-1m")', snippet: 'has(, "")', caret: 4 }
    ]
  },
  {
    title: '时间（按请求开始的时刻）',
    items: [
      { sig: 'hour("时区")', desc: '0–23 点', snippet: 'hour("Asia/Shanghai")' },
      { sig: 'minute("时区")', desc: '0–59 分', snippet: 'minute("Asia/Shanghai")' },
      { sig: 'weekday("时区")', desc: '星期，0 是星期日，1–6 是星期一到六', snippet: 'weekday("Asia/Shanghai")' },
      { sig: 'day("时区")', desc: '几号，1–31', snippet: 'day("Asia/Shanghai")' },
      { sig: 'month("时区")', desc: '几月，1–12', snippet: 'month("Asia/Shanghai")' }
    ]
  },
  {
    title: '数学',
    items: [
      { sig: 'max(a, b)  min(a, b)', desc: '取大、取小，可用来设最低收费或封顶', snippet: 'max(, )', caret: 4 },
      { sig: 'ceil(x)  floor(x)  abs(x)', desc: '向上取整、向下取整、绝对值', snippet: 'ceil()', caret: 5 }
    ]
  }
]

const RULE_SAMPLE = 'tier("base", p*3 + c*15)\n* (param("service_tier") == "priority" ? 2 : 1)\n* (hour("Asia/Shanghai") < 8 ? 0.5 : 1)'

const EXAMPLES: Array<{ title: string; desc: string; src: string }> = [
  { title: '单一价格', desc: '输入每百万 3 美元，输出 15 美元，缓存读 0.3 美元。', src: 'tier("base", p*3 + c*15 + cr*0.3)' },
  {
    title: '长上下文两档',
    desc: '上下文不超过 20 万按标准价，超过后整单按长上下文价。',
    src: 'len <= 200000\n  ? tier("standard", p*3 + c*15 + cr*0.3 + cc*3.75 + cc1h*6)\n  : tier("long_context", p*6 + c*22.5 + cr*0.6 + cc*7.5 + cc1h*12)'
  },
  {
    title: 'priority 整单两倍',
    desc: '请求里写了 service_tier=priority 时整单 ×2。',
    src: 'tier("base", p*2.5 + c*15 + cr*0.25)\n* (param("service_tier") == "priority" ? 2 : 1)'
  },
  {
    title: '按应答的服务等级',
    desc: '用插件报的实际等级计费，比只看请求更准。',
    src: 'tier("base", p*2.5 + c*15 + cr*0.25)\n* (u("service_tier") == "priority" ? 2 : 1)\n* (u("service_tier") == "flex" ? 0.5 : 1)'
  },
  {
    title: '夜间五折',
    desc: '上海时间 0 点到 8 点半价。',
    src: 'tier("base", p*3 + c*15)\n* (hour("Asia/Shanghai") < 8 ? 0.5 : 1)'
  },
  { title: '周末八折', desc: '星期六、星期日打八折。', src: 'tier("base", p*3 + c*15)\n* (weekday("Asia/Shanghai") == 0 || weekday("Asia/Shanghai") == 6 ? 0.8 : 1)' },
  { title: '最低收费', desc: '每次至少收 0.001 美元。', src: 'tier("base", max(p*3 + c*15, flat(0.001)))' },
  { title: '按 token 加每次固定费', desc: '每次另收 0.002 美元。', src: 'tier("base", p*3 + c*15 + flat(0.002))' },
  {
    title: '按推理强度分价',
    desc: 'reasoning.effort 为 high 时输出价更高。',
    src: 'param("reasoning.effort") == "high"\n  ? tier("high", p*3 + c*20)\n  : tier("normal", p*3 + c*15)'
  }
]
</script>

<template>
  <div class="min-w-0" data-testid="expr-guide">
    <nav class="sticky -top-px z-10 -mx-1 mb-4 flex flex-wrap gap-1.5 border-b border-line bg-surface px-1 pb-3 pt-1">
      <button
        v-for="s in sections"
        :key="s.id"
        type="button"
        class="rounded-full border px-2.5 py-0.5 text-xs"
        :class="active === s.id ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-900/40 dark:text-primary-300' : 'border-line text-fg-muted hover:border-primary-400'"
        @click="jump(s.id)"
      >
        {{ s.label }}
      </button>
    </nav>

    <div class="space-y-7 text-sm leading-6 text-fg">
      <section id="expr-guide-basics" class="scroll-mt-24">
        <h3 class="mb-2 font-semibold">基本写法</h3>
        <ul class="list-disc space-y-1 pl-5 text-fg-muted">
          <li>表达式算出这一次请求的费用。token 变量是这次用掉的 token 数，单价按<b class="text-fg">美元 / 百万 token</b>写：<code class="code">p*3</code> 就是输入每百万 3 美元。</li>
          <li><code class="code">flat(0.05)</code> 是直接加 0.05 美元，不按百万换算。</li>
          <li>结果不能是负数。最终金额 = 结果 × 分组倍率，<b class="text-fg">进一到 8 位小数</b>（只会多扣，不会少扣）。</li>
          <li>可以随意换行和加空格；“格式化”会把每个乘数排成一行。</li>
          <li>按 token 方式生成的也是表达式，在“查看生成的表达式”里可以“改用表达式编辑”接着改。</li>
        </ul>
      </section>

      <section id="expr-guide-vars" class="scroll-mt-24">
        <h3 class="mb-2 font-semibold">变量</h3>
        <dl class="grid grid-cols-[4.5rem_1fr] gap-x-3 gap-y-1.5">
          <template v-for="[k, d] in VARS" :key="k">
            <dt>
              <button type="button" class="code hover:ring-1 hover:ring-primary-400" @click="emit('insert', k)">{{ k }}</button>
            </dt>
            <dd class="text-fg-muted">{{ d }}</dd>
          </template>
        </dl>
        <p class="mt-2 text-xs text-fg-muted">
          表达式里没写到的缓存类别不收费；没写 img、img_o 时图片 token 留在 p、c 里按文字价算。
        </p>
      </section>

      <section id="expr-guide-ops" class="scroll-mt-24">
        <h3 class="mb-2 font-semibold">运算符</h3>
        <dl class="grid grid-cols-[10rem_1fr] gap-x-3 gap-y-1.5">
          <template v-for="[k, d] in OPS" :key="k">
            <dt class="whitespace-pre font-mono text-xs leading-6">{{ k }}</dt>
            <dd class="text-fg-muted">{{ d }}</dd>
          </template>
        </dl>
        <p class="mt-2 text-xs text-fg-muted">字符串用双引号；比较字符串区分大小写。</p>
      </section>

      <section id="expr-guide-fns" class="scroll-mt-24">
        <h3 class="mb-2 font-semibold">函数</h3>
        <div v-for="g in FN_GROUPS" :key="g.title" class="mb-4 last:mb-0">
          <div class="mb-1.5 text-xs font-medium text-fg-muted">{{ g.title }}</div>
          <ul class="divide-y divide-line rounded-lg border border-line">
            <li v-for="f in g.items" :key="f.sig" class="flex items-start gap-3 px-3 py-2">
              <div class="min-w-0 flex-1">
                <code class="font-mono text-xs" v-html="highlight(f.sig, -1, true)"></code>
                <div class="text-xs text-fg-muted">{{ f.desc }}</div>
              </div>
              <SButton size="xs" variant="ghost" @click="emit('insert', f.snippet, f.caret)">插入</SButton>
            </li>
          </ul>
        </div>
        <p class="text-xs text-fg-muted">param、header、u、tier 和时间函数的参数必须直接写成字符串（如 "Asia/Shanghai"），不能是算出来的值。</p>
      </section>

      <section id="expr-guide-tiers" class="scroll-mt-24">
        <h3 class="mb-2 font-semibold">档位</h3>
        <ul class="list-disc space-y-1 pl-5 text-fg-muted">
          <li>用 <code class="code">tier("名称", 费用)</code> 包住每一档的费用，名称只能用字母、数字和 <code class="code">_ . -</code>，同一个表达式里不能重名。</li>
          <li>按上下文分档就用 <code class="code">len &lt;= 上限 ? … : …</code> 连着写，从小到大，最后一档不写条件。</li>
          <li>不只是 len，任何条件都能分档：请求参数、请求头、时间都可以。</li>
          <li>命中的档位会写进使用记录，试算里也会显示。</li>
        </ul>
      </section>

      <section id="expr-guide-rules" class="scroll-mt-24">
        <h3 class="mb-2 font-semibold">加价规则</h3>
        <p class="text-fg-muted">在基础费用后面乘上 <code class="code">(条件 ? 倍数 : 1)</code>，几条规则就乘几个：</p>
        <pre
          class="my-2 overflow-x-auto rounded-lg bg-surface-2 px-3 py-2 font-mono text-xs leading-5"
          v-html="highlight(RULE_SAMPLE)"
        ></pre>
        <ul class="list-disc space-y-1 pl-5 text-fg-muted">
          <li>写成“条件 ? 数字 : 1”、条件里读了请求、用量或时间的，都算一条规则，使用记录里会写明哪条命中、乘了多少。</li>
          <li>倍数不能是负数；小于 1 就是打折。</li>
          <li>条件读到 nil（如请求里没有这个参数）按不命中算。</li>
        </ul>
      </section>

      <section id="expr-guide-examples" class="scroll-mt-24">
        <h3 class="mb-2 font-semibold">例子</h3>
        <div class="grid grid-cols-1 gap-3">
          <div v-for="x in EXAMPLES" :key="x.title" class="min-w-0 overflow-hidden rounded-lg border border-line">
            <div class="flex items-center justify-between gap-3 px-3 pt-2">
              <div class="min-w-0">
                <div class="text-sm font-medium">{{ x.title }}</div>
                <div class="text-xs text-fg-muted">{{ x.desc }}</div>
              </div>
              <SButton size="xs" @click="emit('replace', x.src)">填入</SButton>
            </div>
            <pre class="m-0 overflow-x-auto px-3 pb-2.5 pt-1.5 font-mono text-xs leading-5" v-html="highlight(x.src)"></pre>
          </div>
        </div>
      </section>

      <section id="expr-guide-check" class="scroll-mt-24">
        <h3 class="mb-2 font-semibold">检查</h3>
        <p class="mb-1 text-fg-muted">编辑时会自动检查，结果在右边的“检查”里，有错误时不能保存：</p>
        <ol class="list-decimal space-y-1 pl-5 text-fg-muted">
          <li><b class="text-fg">语法</b>：能不能编译，写错的地方会标出行号和列号。</li>
          <li><b class="text-fg">字段</b>：函数参数是不是字符串、时区对不对、u() 的字段有没有插件声明、档位有没有重名。</li>
          <li><b class="text-fg">试跑</b>：拿几十组用量（全 0、每个变量单独 1 / 1,000 / 1,000,000、档位边界上下）各算一遍，算不出数或算出负数都算失败。</li>
          <li><b class="text-fg">金额提醒</b>：100 万输入 token 一次超过设置的阈值时提醒你确认，不阻止保存。</li>
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
