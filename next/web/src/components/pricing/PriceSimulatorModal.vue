<script setup lang="ts">
import { computed, reactive, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SButton, SField, SGrid, SHint, SInput, SModal, SSegmented, SSelect, SSpinner, type SelectOption } from '@sub2api/ui'
import { QUOTE_MAX_TOKENS, type GroupModelPrices, type PricingQuote, type PricingQuoteRequest } from '@/api/pricing'
import type { Group, User } from '@/api/types'
import EntityPicker from '@/components/EntityPicker.vue'
import GroupPicker from '@/components/GroupPicker.vue'
import { fetchFirstPage, useGroupsLookup } from '@/composables/lookups'
import { useLatestRequest } from '@/composables/useLatestRequest'
import { useAuthStore } from '@/stores/auth'
import { formatNumber } from '@/utils/format'
import PriceQuoteBreakdown from './PriceQuoteBreakdown.vue'
import { quoteSubjects, type QuoteSubject } from './quoteSubjects'
import SegmentBadge from './SegmentBadge.vue'
import SegmentSelect from './SegmentSelect.vue'

// Price simulator (POST /pricing/quote): what a user, or any member of a
// segment, pays for a sample call of a model in a group (PriceQuoteBreakdown
// shows where that comes from). A tool in a dialog: it asks again whenever a
// condition changes and saves nothing. `preset` fills conditions in on
// opening; a model named without a group brings a group that provides it.
// It reads the groups as it is set up: an opener mounts it only for a caller
// who may simulate (canSimulatePrices).
interface Preset {
  userId?: number
  segmentId?: number
  groupId?: number
  model?: string
}
const props = defineProps<{ open: boolean; preset?: Preset }>()
const emit = defineEmits<{ (e: 'update:open', v: boolean): void }>()
const { t } = useI18n()
const auth = useAuthStore()
const { groups } = useGroupsLookup()

// ------------------------------------------------------------------ conditions

// The quote is for whom the caller may pick: a user (user:read), else a segment (segment:read).
const allowed = quoteSubjects(auth)
const subject = ref<QuoteSubject>(allowed[0] ?? 'segment')
/** The user quoted for, as the picker hands him over ('' = none). */
const userId = ref('')
const segmentId = ref<number | null>(null)
const groupId = ref<number | null>(null)
const model = ref<string | null>(null)
const usage = reactive<{ p: number | null; c: number | null }>({ p: 100_000, c: 10_000 })
/** The most tokens of a kind the server quotes for, as it takes a request's (core.ValidatePricedUsage). */
/** Why a token count cannot be quoted for: it must be a whole number from 0 to QUOTE_MAX_TOKENS; blank is 0. */
const tokensError = (v: number | null) => (v === null || (Number.isInteger(v) && v >= 0 && v <= QUOTE_MAX_TOKENS) ? undefined : t('pricingTools.sim.tokensInvalid', { max: formatNumber(QUOTE_MAX_TOKENS) }))
const usageErrors = computed(() => ({ p: tokensError(usage.p), c: tokensError(usage.c) }))
const usageInvalid = computed(() => !!(usageErrors.value.p || usageErrors.value.c))

const subjects = computed<SelectOption[]>(() => allowed.map((value) => ({ value, label: t(`pricingTools.sim.subject.${value}`) })))

// The row of the user picked (GET /users/:id) says which user group he is in. Only the latest pick counts.
const picked = useLatestRequest(
  () => (props.open && subject.value === 'user' && userId.value ? userId.value : null),
  (id) => api.get<User>(`/users/${id}`),
  0
)
/** The user quoted for, once his row is here; null while it loads. */
const user = computed(() => {
  const loaded = picked.result.value
  return loaded && String(loaded.id) === userId.value ? loaded : null
})

/** A preset model that no group provides: the result says so until a model is chosen. */
const unprovided = ref<string | null>(null)
/** A model the opener names without a group. A new object each time: naming the same model again asks again. */
const named = shallowRef<{ name: string } | null>(null)
// The groups that provide it (GET /groups?model=). Only the latest model named counts.
const providers = useLatestRequest(named, async ({ name }) => ({ name, groups: await fetchFirstPage<Group>('/groups', { model: name }) }), 0)
// Chooses the model in the chosen group when that one provides it, else in the first group that does.
watch(providers.result, (found) => {
  if (!found) return
  const group = found.groups.find((g) => g.id === groupId.value) ?? found.groups[0]
  if (group) groupId.value = group.id
  model.value = group ? found.name : null
  unprovided.value = group ? null : found.name
})

/** Takes what the opener names and leaves the other conditions as they were; whom the caller may not quote for is left out. */
function applyPreset(preset: Preset) {
  unprovided.value = null
  if (preset.groupId !== undefined) groupId.value = preset.groupId
  if (preset.model !== undefined && preset.groupId !== undefined) model.value = preset.model
  named.value = preset.model !== undefined && preset.groupId === undefined ? { name: preset.model } : null
  if (preset.segmentId !== undefined && allowed.includes('segment')) {
    subject.value = 'segment'
    segmentId.value = preset.segmentId
  }
  if (preset.userId === undefined || !allowed.includes('user')) return
  subject.value = 'user'
  userId.value = String(preset.userId)
}
// Compared by value: an opener may hand over a new object on every render.
const presetKey = computed(() => (props.open && props.preset ? JSON.stringify(props.preset) : ''))
watch(
  presetKey,
  () => {
    if (props.open && props.preset) applyPreset(props.preset)
  },
  { immediate: true }
)

// The models the chosen group provides: the ones its model prices list.
const groupModels = useLatestRequest(
  () => (props.open ? groupId.value : null),
  async (id) => ({ id, models: (await api.get<GroupModelPrices>(`/groups/${id}/model-prices`)).models.map((m) => m.model) }),
  0
)
/** [] without a group, null while the models of the group load. */
const provided = computed(() => {
  if (groupId.value === null) return []
  const loaded = groupModels.result.value
  return loaded?.id === groupId.value ? loaded.models : null
})
const modelOptions = computed<SelectOption[]>(() => (provided.value ?? []).map((m) => ({ value: m, label: m })))
watch([provided, model], () => {
  if (model.value) unprovided.value = null
  // A model the group does not provide cannot stay chosen; while the models of the group load, it waits.
  if (model.value && groupId.value !== null && provided.value && !provided.value.includes(model.value)) model.value = null
})

// ------------------------------------------------------------------ quote

/** Whom the quote is for, as the request names him; null until chosen. */
const who = computed<Pick<PricingQuoteRequest, 'user_id' | 'segment_id'> | null>(() => {
  if (subject.value === 'user') return user.value && { user_id: user.value.id }
  return segmentId.value === null ? null : { segment_id: segmentId.value }
})
const request = computed<PricingQuoteRequest | null>(() => {
  if (!props.open || !who.value || groupId.value === null || !model.value || !provided.value?.includes(model.value)) return null
  if (usageInvalid.value) return null
  return { ...who.value, group_id: groupId.value, model: model.value, usage: { p: usage.p ?? 0, c: usage.c ?? 0 } }
})
const { result: quote, loading: quoting, error: quoteError } = useLatestRequest(request, (body) => api.post<PricingQuote>('/pricing/quote', body), 400)

const groupName = computed(() => groups.value.find((g) => g.id === groupId.value)?.name)
</script>

<template>
  <SModal :open="open" :title="t('pricingTools.sim.title')" width="xl" @update:open="emit('update:open', $event)">
    <div class="space-y-4" data-testid="price-sim">
      <SHint>{{ t('pricingTools.sim.description') }}</SHint>

      <SField :label="t('pricingTools.sim.who')" :error="picked.error.value">
        <div class="flex flex-wrap items-center gap-3">
          <SSegmented v-if="subjects.length > 1" v-model="subject" :options="subjects" :aria-label="t('pricingTools.sim.who')" data-testid="price-sim-subject" />
          <div class="min-w-0 flex-1 basis-64">
            <SegmentSelect v-if="subject === 'segment'" v-model="segmentId" data-testid="price-sim-segment" />
            <div v-else class="flex flex-wrap items-center gap-2" data-testid="price-sim-user">
              <EntityPicker v-model:id="userId" kind="user" class="min-w-0 flex-1 basis-56" />
              <SegmentBadge v-if="user" :segment="user.segment" class="shrink-0" />
            </div>
          </div>
        </div>
      </SField>
      <SGrid :cols="2">
        <SField :label="t('common.group')" :error="providers.error.value">
          <GroupPicker :model-value="groupId" @update:model-value="groupId = typeof $event === 'number' ? $event : null" />
        </SField>
        <SField :label="t('common.model')" :hint="t('pricingTools.sim.modelHint')" :error="groupModels.error.value">
          <SSelect
            :model-value="model"
            :options="modelOptions"
            searchable
            :placeholder="t('pricingTools.sim.modelPlaceholder')"
            :disabled="!provided?.length"
            data-testid="price-sim-model"
            @update:model-value="model = typeof $event === 'string' ? $event : null"
          />
        </SField>
        <SField :label="t('pricingTools.sim.inputTokens')" :hint="formatNumber(usage.p)" :error="usageErrors.p">
          <SInput v-model="usage.p" type="number" min="0" :max="QUOTE_MAX_TOKENS" step="1" data-testid="price-sim-input-tokens" />
        </SField>
        <SField :label="t('pricingTools.sim.outputTokens')" :hint="formatNumber(usage.c)" :error="usageErrors.c">
          <SInput v-model="usage.c" type="number" min="0" :max="QUOTE_MAX_TOKENS" step="1" data-testid="price-sim-output-tokens" />
        </SField>
      </SGrid>

      <div class="rounded-xl border border-line p-4" data-testid="price-sim-result">
        <SHint v-if="unprovided" tone="warning" data-testid="price-sim-unprovided">{{ t('pricingTools.sim.unprovided', { model: unprovided }) }}</SHint>
        <SHint v-else-if="usageInvalid" tone="warning" data-testid="price-sim-usage-invalid">{{ t('pricingTools.sim.usageInvalid') }}</SHint>
        <SHint v-else-if="!request">{{ t('pricingTools.sim.incomplete') }}</SHint>
        <SHint v-else-if="quoteError" tone="danger">{{ quoteError }}</SHint>
        <PriceQuoteBreakdown v-else-if="quote" :quote="quote" :group-name="groupName" class="transition-opacity" :class="quoting ? 'opacity-60' : ''" :aria-busy="quoting" />
        <SSpinner v-else block />
      </div>
    </div>
    <template #footer>
      <SButton data-testid="price-sim-close" @click="emit('update:open', false)">{{ t('common.close') }}</SButton>
    </template>
  </SModal>
</template>
