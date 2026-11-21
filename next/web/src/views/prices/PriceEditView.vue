<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import { ApiError, api } from '@sub2api/host'
import {
  SAlert,
  SBadge,
  SButton,
  SDrawer,
  SField,
  SInput,
  SLink,
  SPageHeader,
  SSegmented,
  SSpinner,
  SSwitch,
  confirm,
  toast
} from '@sub2api/ui'
import type { Price } from '@/api/types'
import { confirmDiscard } from '@/composables/confirmDiscard'
import { useAuthStore } from '@/stores/auth'
import { notifyError } from '@/utils/errors'
import { applyFormError } from '@/utils/formErrors'
import { formatDateTime } from '@/utils/format'
import ExprGuide from './ExprGuide.vue'
import ExprHistoryModal from './ExprHistoryModal.vue'
import PriceExprEditor from './PriceExprEditor.vue'
import PriceEstimator from './PriceEstimator.vue'
import PriceModelList from './PriceModelList.vue'
import TokenPriceEditor from './TokenPriceEditor.vue'
import { issueText, type Issue } from './issues'
import PriceCheckBar from './PriceCheckBar.vue'
import { usePriceValidation, type PricePayload } from './usePriceValidation'
import { checkPrice, compactTokenConfig, PRICE_MODES, tokenExpr, type PriceMode, type VisualConfig } from './priceExpr'
import { visualOf } from './exprParse'
import { defaultTokenConfig, editableTokenConfig } from './tokenPrice'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const priceId = computed(() => {
  const v = route.params.id
  const n = Number(Array.isArray(v) ? v[0] : v)
  return Number.isFinite(n) && n > 0 ? n : null
})

const loading = ref(false)
const saving = ref(false)
const price = ref<Price | null>(null)
const form = reactive({ model: '', note: '', enabled: true })
const mode = ref<PriceMode>('visual')
const token = ref<VisualConfig>(defaultTokenConfig())
const sourceText = ref('')
const errors = ref<Record<string, string>>({})
const historyOpen = ref(false)
const exprEditor = ref<InstanceType<typeof PriceExprEditor>>()
const modelList = ref<InstanceType<typeof PriceModelList>>()
const readonly = computed(() => !auth.has('price:manage'))

// The expression guide opens in a drawer.
const guideOpen = ref(false)
function guideReplace(src: string) {
  if (readonly.value) return
  sourceText.value = src
  guideOpen.value = false
}

const modeOptions = computed(() => PRICE_MODES.map(m => ({ value: m, label: t(`prices.mode.${m}`) })))
const isSynced = computed(() => price.value?.source === 'sync')
const syncedNotice = computed(() => {
  const p = price.value
  if (!p || p.source !== 'sync') return ''
  return t('prices.syncedNotice', {
    source: p.sync_source_name || t('prices.syncedNoticeDeleted'),
    time: p.synced_at ? formatDateTime(p.synced_at) : '—'
  })
})

// ------------------------------------------------------------------ load

function resetForm() {
  price.value = null
  Object.assign(form, { model: '', note: '', enabled: true })
  mode.value = 'visual'
  token.value = defaultTokenConfig()
  sourceText.value = ''
  errors.value = {}
  validator.reset()
  markSaved()
}

function applyPrice(p: Price) {
  price.value = p
  Object.assign(form, { model: p.model, note: p.note || '', enabled: p.enabled })
  mode.value = p.mode
  sourceText.value = p.expression || ''
  if (p.mode === 'visual') token.value = editableTokenConfig(p.config)
  markSaved()
}

async function load() {
  resetForm()
  if (!priceId.value) return
  loading.value = true
  try {
    applyPrice(await api.get<Price>(`/prices/${priceId.value}`))
  } catch (e) {
    notifyError(e)
  } finally {
    loading.value = false
  }
}

// ------------------------------------------------------------------ derived payload

/** The visual config as it is sent: prices and condition values typed. */
const tokenConfig = computed(() => compactTokenConfig(token.value))

const payload = computed<PricePayload & { mode: PriceMode }>(() => {
  if (mode.value === 'visual') return { mode: 'visual', config: tokenConfig.value, expression: tokenExpr(tokenConfig.value) }
  return { mode: 'expression', config: {}, expression: sourceText.value }
})

const localIssues = computed<string[]>(() => checkPrice(payload.value).map(x => t(x.key, x.params || {})))

const previewText = computed(() => payload.value.expression)
const previewSource = computed(() => (localIssues.value.length || validation.value?.errors.length ? null : { ...payload.value }))

/** A single request tier prices no tokens: the estimator then asks for nothing. */
const perRequestOnly = computed(() => mode.value === 'visual' && token.value.tiers.length === 1 && token.value.tiers[0].unit === 'request')

/**
 * Switches the mode (design 2026-11-22 §0): into the expression mode with the
 * expression the visual config generates; back into the visual mode only with
 * an expression the generator wrote (visualOf), or a blank one, which keeps
 * the visual config there was.
 */
function switchMode(next: PriceMode) {
  if (next === mode.value) return
  if (next === 'expression') {
    sourceText.value = payload.value.expression
    mode.value = next
    return
  }
  if (!sourceText.value.trim()) {
    mode.value = next
    return
  }
  const cfg = visualOf(sourceText.value)
  if (!cfg) return toast(t('prices.edit.notVisual'), 'error')
  token.value = editableTokenConfig(cfg)
  mode.value = next
}

// ------------------------------------------------------------------ validation

const validator = usePriceValidation(payload, localIssues)
const { validation, error: validationError, validating, serverExpr } = validator

// ------------------------------------------------------------------ unsaved changes

/** What a save would send, as text. */
const savedState = () => JSON.stringify([form.model.trim(), form.note, form.enabled, payload.value])
const snapshot = ref('')
/** Takes what the editor holds as saved: once it is loaded and after each save. */
function markSaved() {
  snapshot.value = savedState()
}
const dirty = computed(() => savedState() !== snapshot.value)
// Another model (the same page) or another page: asked first while there are unsaved changes.
onBeforeRouteUpdate((to, from) => to.path === from.path || confirmDiscard(dirty.value))
onBeforeRouteLeave(() => confirmDiscard(dirty.value))

// ------------------------------------------------------------------ actions

async function submit(body: Record<string, unknown>): Promise<void> {
  try {
    if (priceId.value) {
      const r = await api.patch<Price>(`/prices/${priceId.value}`, body)
      toast(t('common.saved'), 'success')
      if (r && r.id) applyPrice(r)
      else await load()
      modelList.value?.reload()
    } else {
      const r = await api.post<Price>('/prices', body)
      toast(t('common.created'), 'success')
      markSaved()
      if (r?.id) router.replace(`/prices/${r.id}`)
      else router.push('/prices')
    }
  } catch (e) {
    if (e instanceof ApiError && e.code === 'invalid_argument') {
      const d = e.details
      validator.showSaveIssues(d)
      // Big-cost warning: ask, then resend the same body with confirm=true.
      if (d.confirmation_required && !body.confirm) {
        const list = ((d.warnings || []) as Issue[]).map(issueText).join('\n')
        const ok = await confirm({
          title: t('prices.validate.warnTitle'),
          message: t('prices.validate.warnConfirm', { list: list || e.message }),
          danger: true
        })
        if (ok) await submit({ ...body, confirm: true })
        return
      }
    }
    applyFormError(e, errors)
  }
}

async function save() {
  if (readonly.value) return
  errors.value = {}
  if (!form.model.trim()) {
    errors.value = { model: t('common.required') }
    return
  }
  // Prices match complete model ids exactly (same rule as the server).
  if (!/^[A-Za-z0-9._:/@+-]{1,200}$/.test(form.model.trim())) {
    errors.value = { model: t('prices.modelInvalid') }
    return
  }
  if (localIssues.value.length) {
    toast(localIssues.value[0], 'error')
    return
  }
  saving.value = true
  try {
    await submit({
      model: form.model.trim(),
      mode: payload.value.mode,
      config: payload.value.config,
      expression: payload.value.expression,
      enabled: form.enabled,
      note: form.note
    })
  } finally {
    saving.value = false
  }
}

watch(priceId, load, { immediate: true })

</script>

<template>
  <div class="space-y-4">
    <SPageHeader :title="t('prices.title')">
      <template #actions>
        <SButton size="sm" variant="ghost" @click="router.push('/prices')">{{ t('prices.edit.backToList') }}</SButton>
      </template>
    </SPageHeader>

    <div class="grid items-start gap-4 xl:grid-cols-[18rem_minmax(0,1fr)]">
      <PriceModelList ref="modelList" :current="priceId" :can-manage="!readonly" class="hidden xl:sticky xl:top-4 xl:flex xl:h-[calc(100vh-8rem)]" />

      <div class="min-w-0 rounded-xl border border-line bg-surface" data-testid="price-edit-panel">
        <div class="flex flex-wrap items-center gap-2 border-b border-line px-4 py-3 sm:px-5">
          <h2 class="text-base font-semibold text-fg">{{ t(priceId ? 'prices.editTitleShort' : 'prices.newTitle') }}</h2>
          <SBadge v-if="price" :tone="isSynced ? 'info' : 'gray'" size="sm" data-testid="price-edit-source">
            {{ isSynced && price.sync_source_name ? t('prices.sourceSync', { name: price.sync_source_name }) : t(`prices.source.${isSynced ? 'sync' : 'manual'}`) }}
          </SBadge>
          <SLink v-if="price?.expr_hash" as="button" class="ml-auto text-xs" @click="historyOpen = true">{{ t('prices.viewHistory') }}</SLink>
        </div>

        <SSpinner v-if="loading" size="lg" block class="py-16" />
        <div v-else class="space-y-5 px-4 py-4 sm:px-5">
          <SAlert v-if="syncedNotice" tone="info" dense data-testid="price-synced-notice">{{ syncedNotice }}</SAlert>

          <SField :label="t('prices.model')" :error="errors.model" required :hint="t('prices.modelHint')">
            <SInput v-model.trim="form.model" mono placeholder="claude-sonnet-4-5" :disabled="readonly" />
          </SField>
          <div class="grid gap-x-4 gap-y-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-end">
            <SField :label="t('common.note')" :error="errors.note">
              <SInput v-model="form.note" :disabled="readonly" :placeholder="t('common.optional')" />
            </SField>
            <div class="flex h-9 items-center">
              <SSwitch v-model="form.enabled" :disabled="readonly" :label="t(form.enabled ? 'common.enabled' : 'common.disabled')" />
            </div>
          </div>

          <div>
            <SSegmented
              :model-value="mode"
              block
              :options="modeOptions"
              :disabled="readonly"
              :aria-label="t('prices.billingMode')"
              data-testid="price-mode"
              @update:model-value="switchMode($event as PriceMode)"
            />
            <p class="mt-1.5 text-xs text-fg-muted">{{ t(`prices.edit.modeHint.${mode}`) }}</p>
          </div>

          <TokenPriceEditor v-if="mode === 'visual'" v-model="token" :disabled="readonly" />
          <PriceExprEditor
            v-else
            ref="exprEditor"
            v-model="sourceText"
            :readonly="readonly"
            :error="errors.expression"
            :validation="validation"
            @guide="guideOpen = true"
          />

          <PriceCheckBar
            :local-issues="localIssues"
            :validation="validation"
            :error="validationError"
            :validating="validating"
            :mode="mode"
            :per-request="perRequestOnly"
            @locate="(l, c) => exprEditor?.focusAt(l, c)"
          />

          <PriceEstimator :source="previewSource" :per-request="perRequestOnly" />

          <section class="rounded-xl border border-line" data-testid="price-preview">
            <div class="border-b border-line px-3 py-2 text-sm font-medium text-fg sm:px-4">{{ t('prices.edit.preview') }}</div>
            <div class="border-b border-line px-3 py-2 sm:px-4">
              <div class="text-xs text-fg-muted">{{ t('prices.edit.previewMode') }}</div>
              <div class="text-sm text-fg">{{ t(`prices.mode.${mode}`) }}</div>
            </div>
            <div class="px-3 py-2 sm:px-4">
              <div class="flex items-center gap-2 text-xs text-fg-muted">
                {{ t('prices.edit.previewExpr') }}
                <SLink
                  v-if="mode !== 'expression' && !readonly && !localIssues.length"
                  as="button"
                  class="ml-auto text-xs"
                  data-testid="price-to-expression"
                  @click="switchMode('expression')"
                  >{{ t('prices.toExpression') }}</SLink
                >
              </div>
              <pre v-if="mode !== 'expression'" class="mt-1 whitespace-pre-wrap break-all font-mono text-xs leading-5 text-fg" data-testid="price-generated">{{ previewText || '—' }}</pre>
              <div v-else class="mt-1 text-xs text-fg-muted">{{ t('prices.edit.previewSame') }}</div>
              <template v-if="serverExpr && mode !== 'expression'">
                <div class="mt-2 text-xs text-fg-muted">{{ t('prices.serverGenerated') }}</div>
                <pre class="mt-1 whitespace-pre-wrap break-all font-mono text-xs leading-5 text-fg">{{ serverExpr }}</pre>
              </template>
            </div>
          </section>
        </div>

        <div v-if="!loading" class="bottom-0 z-10 flex sm:sticky flex-wrap items-center gap-3 rounded-b-xl border-t border-line bg-surface/95 px-4 py-3 backdrop-blur sm:px-5">
          <span v-if="price" class="text-xs text-fg-subtle">
            {{ t('prices.expression') }} <span class="font-mono">{{ (price.expr_hash || '').slice(0, 12) }}</span>
            <template v-if="price.updated_at"> · {{ t('prices.edit.updatedAt', { time: formatDateTime(price.updated_at) }) }}</template>
          </span>
          <div v-if="!readonly" class="ml-auto flex items-center gap-2">
            <SButton @click="router.push('/prices')">{{ t('common.cancel') }}</SButton>
            <SButton variant="primary" icon="check" :loading="saving" data-testid="price-save" @click="save">{{ t('prices.edit.save') }}</SButton>
          </div>
        </div>
      </div>
    </div>

    <SDrawer v-model:open="guideOpen" :title="t('prices.edit.guide')" width="lg">
      <ExprGuide :readonly="readonly" @insert="(s, c) => exprEditor?.insert(s, c)" @replace="guideReplace" />
    </SDrawer>
    <ExprHistoryModal v-model:open="historyOpen" :hash="price?.expr_hash" />
  </div>
</template>
