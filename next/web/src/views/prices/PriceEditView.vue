<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { ApiError, api } from '@sub2api/host'
import {
  SAlert,
  SBadge,
  SButton,
  SCard,
  SChoiceCards,
  SCode,
  SDescItem,
  SDescriptions,
  SDisclosure,
  SField,
  SGrid,
  SHint,
  SInput,
  SLink,
  SPageHeader,
  SSettingRow,
  SSpinner,
  SSwitch,
  confirm,
  toast
} from '@sub2api/ui'
import type { Price } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { notifyError } from '@/utils/errors'
import { applyFormError } from '@/utils/formErrors'
import { formatDateTime } from '@/utils/format'
import ExprHistoryModal from './ExprHistoryModal.vue'
import PriceExprEditor from './PriceExprEditor.vue'
import PriceTrialPanel from './PriceTrialPanel.vue'
import TokenPriceEditor from './TokenPriceEditor.vue'
import { issueText, type Issue } from './issues'
import PriceValidationCard from './PriceValidationCard.vue'
import { usePriceValidation, type PricePayload } from './usePriceValidation'
import {
  checkPerRequest,
  checkTokenConfig,
  compactTokenConfig,
  isSet,
  num,
  perRequestExpr,
  tokenExpr,
  type PriceMode,
  type TokenConfig
} from './priceExpr'
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
const mode = ref<PriceMode>('per_token')
const perRequest = ref<number | null>(0.01)
const token = ref<TokenConfig>(defaultTokenConfig())
const sourceText = ref('')
const errors = ref<Record<string, string>>({})
const historyOpen = ref(false)

const readonly = computed(() => !auth.has('price:manage'))
const MODES = ['per_request', 'per_token', 'expression'] as const
const modeOptions = computed(() => MODES.map(m => ({ value: m, label: t(`prices.mode.${m}`), description: t(`prices.modeDesc.${m}`) })))
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
  mode.value = 'per_token'
  perRequest.value = 0.01
  token.value = defaultTokenConfig()
  sourceText.value = ''
  errors.value = {}
  validator.reset()
}

function applyPrice(p: Price) {
  price.value = p
  Object.assign(form, { model: p.model, note: p.note || '', enabled: p.enabled })
  mode.value = p.mode
  sourceText.value = p.expression || ''
  if (p.mode === 'per_request') perRequest.value = isSet(p.config?.price) ? num(p.config?.price) : null
  if (p.mode === 'per_token') token.value = editableTokenConfig(p.config)
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

/** The per_token config as it is sent: prices and condition values typed. */
const tokenConfig = computed(() => compactTokenConfig(token.value))

const payload = computed<PricePayload & { mode: PriceMode }>(() => {
  if (mode.value === 'per_request') {
    // A blank price stays blank: checkPerRequest blocks saving it.
    const price = isSet(perRequest.value) ? num(perRequest.value) : null
    return { mode: 'per_request', config: { price }, expression: perRequestExpr(price) }
  }
  if (mode.value === 'per_token') return { mode: 'per_token', config: tokenConfig.value, expression: tokenExpr(tokenConfig.value) }
  return { mode: 'expression', config: {}, expression: sourceText.value }
})

const localIssues = computed<string[]>(() => {
  if (mode.value === 'expression') return sourceText.value.trim() ? [] : [t('prices.local.emptyExpr')]
  const issues = mode.value === 'per_request' ? checkPerRequest(perRequest.value) : checkTokenConfig(tokenConfig.value)
  return issues.map(x => t(x.key, x.params || {}))
})

const previewSource = computed(() => (localIssues.value.length ? null : { ...payload.value }))

/** Takes the generated source into the expression mode. */
function editAsExpression() {
  sourceText.value = payload.value.expression
  mode.value = 'expression'
}

// ------------------------------------------------------------------ validation

const validator = usePriceValidation(payload, localIssues)
const { validation, error: validationError, validating, serverExpr } = validator

// ------------------------------------------------------------------ actions

async function submit(body: Record<string, unknown>): Promise<void> {
  try {
    if (priceId.value) {
      const r = await api.patch<Price>(`/prices/${priceId.value}`, body)
      toast(t('common.saved'), 'success')
      if (r && r.id) applyPrice(r)
      else await load()
    } else {
      const r = await api.post<Price>('/prices', body)
      toast(t('common.created'), 'success')
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

const title = computed(() => {
  if (!priceId.value) return t('prices.newTitle')
  const p = price.value
  return p ? t('prices.editTitle', { name: p.model }) : t('prices.editTitleShort')
})
</script>

<template>
  <div class="max-w-screen-2xl space-y-5">
    <SPageHeader :title="title">
      <template #title-extra>
        <SBadge v-if="price" :tone="isSynced ? 'info' : 'primary'" data-testid="price-edit-source">
          {{
            isSynced && price.sync_source_name
              ? t('prices.sourceSync', { name: price.sync_source_name })
              : t(`prices.source.${isSynced ? 'sync' : 'manual'}`)
          }}
        </SBadge>
      </template>
      <template #actions>
        <template v-if="!readonly">
          <SButton @click="router.push('/prices')">{{ t('common.cancel') }}</SButton>
          <SButton variant="primary" :loading="saving" @click="save">{{ t('common.save') }}</SButton>
        </template>
      </template>
    </SPageHeader>

    <SAlert v-if="syncedNotice && !loading" tone="info" dense data-testid="price-synced-notice">{{ syncedNotice }}</SAlert>

    <SSpinner v-if="loading" size="lg" block />
    <div v-else class="grid items-start gap-5 xl:grid-cols-[minmax(0,1fr)_400px]">
      <div class="min-w-0 space-y-5">
        <!-- basic -->
        <SCard :title="t('prices.basic')">
          <SAlert tone="primary" dense class="mb-4">{{ t('prices.scopeNoteEdit') }}</SAlert>
          <SGrid :cols="1" :md-cols="2">
            <SField :label="t('prices.model')" :hint="t('prices.modelHint')" :error="errors.model" required>
              <SInput v-model.trim="form.model" mono placeholder="claude-sonnet-4-5" :disabled="readonly" />
            </SField>
            <SField :label="t('common.note')" :error="errors.note">
              <SInput v-model="form.note" :disabled="readonly" />
            </SField>
          </SGrid>
          <SSettingRow :label="t('common.status')" dense flush class="mt-2" for="price-enabled">
            <SSwitch
              id="price-enabled"
              v-model="form.enabled"
              :disabled="readonly"
              :label="form.enabled ? t('common.enabled') : t('common.disabled')"
            />
          </SSettingRow>
          <SDescriptions v-if="price" class="mt-4 border-t border-line pt-4" dense>
            <SDescItem :label="t('prices.exprHash')"
              ><div class="flex flex-wrap items-center gap-2">
                <SCode inline :text="price.expr_hash || '—'" class="break-all" />
                <SHint inline size="xs">v{{ price.expr_version || 1 }}</SHint>
                <SLink v-if="price.expr_hash" as="button" class="text-xs" @click="historyOpen = true">{{ t('prices.viewHistory') }}</SLink>
              </div></SDescItem
            >
            <SDescItem v-if="price.updated_at" :label="t('common.updatedAt')">{{ formatDateTime(price.updated_at) }}</SDescItem>
          </SDescriptions>
        </SCard>

        <!-- mode + editor -->
        <SCard :title="t('prices.billingMode')">
          <SChoiceCards
            :model-value="mode"
            :options="modeOptions"
            :cols="3"
            :disabled="readonly"
            :aria-label="t('prices.billingMode')"
            class="mb-5"
            data-testid="price-mode"
            @update:model-value="mode = $event as PriceMode"
          />

          <SField v-if="mode === 'per_request'" class="max-w-xs" :label="t('prices.perRequestPrice')" :error="errors['config.price']">
            <SInput v-model="perRequest" type="number" min="0" step="any" :suffix="t('prices.perRequestUnit')" :disabled="readonly" />
          </SField>
          <TokenPriceEditor v-else-if="mode === 'per_token'" v-model="token" :disabled="readonly" />
          <PriceExprEditor v-else v-model="sourceText" :readonly="readonly" :error="errors.expression" />

          <div v-if="mode !== 'expression'" class="mt-5 border-t border-line pt-3">
            <SDisclosure :label="t('prices.generatedShow')" :open-label="t('prices.generatedHide')">
              <SCode :text="payload.expression" class="mt-2" data-testid="price-generated" />
              <template v-if="serverExpr">
                <SHint size="xs" class="mb-1 mt-2">{{ t('prices.serverGenerated') }}</SHint>
                <SCode :text="serverExpr" />
              </template>
              <SLink
                v-if="!readonly && !localIssues.length"
                as="button"
                class="mt-2 text-xs"
                data-testid="price-to-expression"
                @click="editAsExpression"
                >{{ t('prices.toExpression') }}</SLink
              >
            </SDisclosure>
            <SHint v-if="errors.expression" tone="danger" size="xs" class="mt-1">{{ errors.expression }}</SHint>
          </div>
        </SCard>
      </div>

      <!-- side: validation and trial, kept in view under the sticky top bar on wide screens -->
      <aside class="min-w-0 space-y-5 xl:sticky xl:top-16 xl:max-h-[calc(100dvh-5rem)] xl:overflow-y-auto">
        <PriceValidationCard :local-issues="localIssues" :validation="validation" :error="validationError" :validating="validating" />
        <SCard :title="t('prices.trial.title')">
          <PriceTrialPanel :source="previewSource" />
        </SCard>
      </aside>
    </div>

    <ExprHistoryModal v-model:open="historyOpen" :hash="price?.expr_hash" />
  </div>
</template>
