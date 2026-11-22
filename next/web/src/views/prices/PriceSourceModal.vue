<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ApiError, api } from '@sub2api/host'
import { SCheckbox, SField, SHint, SInput, SFormModal, SSegmented, SSettingRow, SSwitch, STagInput, toast, type SelectOption } from '@sub2api/ui'
import type { PriceSource, PriceSourceKind } from '@/api/types'
import { applyFormError } from '@/utils/formErrors'
import { DEFAULT_PROVIDERS, DEFAULT_URLS, SOURCE_KINDS, isHttpUrl } from './priceSources'

const props = defineProps<{ open: boolean; source: PriceSource | null }>()
const emit = defineEmits<{ (e: 'update:open', v: boolean): void; (e: 'saved', s: PriceSource | null): void }>()
const { t } = useI18n()

const editing = computed(() => props.source)
const saving = ref(false)
const errors = ref<Record<string, string>>({})

/** The form for `s` (null = a new litellm source). */
function formFor(s: PriceSource | null) {
  const kind: PriceSourceKind = s?.kind ?? 'litellm'
  const providers = s?.options?.providers
  return {
    name: s?.name ?? '',
    kind,
    url: s?.url ?? DEFAULT_URLS[kind],
    apiKey: '',
    /** Edit only: clear the stored key. */
    clearKey: false,
    providers: Array.isArray(providers) ? [...providers] : kind === 'sup2api' ? [] : [...DEFAULT_PROVIDERS[kind]],
    applyMultiplier: s?.options?.apply_multiplier !== false,
    enabled: s?.enabled ?? true
  }
}
const form = reactive(formFor(null))

const hasStoredKey = computed(() => !!editing.value?.has_api_key)
const usesProviders = computed(() => form.kind === 'litellm' || form.kind === 'models_dev')

watch(
  () => props.open,
  (open) => {
    if (!open) return
    errors.value = {}
    Object.assign(form, formFor(props.source))
  },
  { immediate: true }
)

// When editing, the type is fixed: the other types are shown disabled.
const kindOptions = computed<SelectOption[]>(() =>
  SOURCE_KINDS.map((k) => ({ value: k, label: t(`prices.sources.kind.${k}`), disabled: !!editing.value && form.kind !== k }))
)

/** Picking a type fills in its default URL and vendor list (unless edited by hand). */
function pickKind(k: PriceSourceKind) {
  if (editing.value || form.kind === k) return
  const prev = form.kind
  form.kind = k
  if (!form.url.trim() || form.url === DEFAULT_URLS[prev]) form.url = DEFAULT_URLS[k]
  if (k !== 'sup2api') {
    const prevDefault = prev === 'sup2api' ? [] : DEFAULT_PROVIDERS[prev]
    if (!form.providers.length || form.providers.join(',') === prevDefault.join(',')) form.providers = [...DEFAULT_PROVIDERS[k]]
  }
  if (!form.name.trim() || SOURCE_KINDS.some((x) => form.name === t(`prices.sources.kind.${x}`))) form.name = k === 'sup2api' ? '' : t(`prices.sources.kind.${k}`)
  errors.value = {}
}

function options(): Record<string, unknown> {
  return usesProviders.value ? { providers: form.providers.map((s) => s.trim()).filter(Boolean) } : { apply_multiplier: form.applyMultiplier }
}

/** What is wrong with the form before it is sent; empty when nothing is. */
function formErrors(): Record<string, string> {
  const e: Record<string, string> = {}
  if (!form.name.trim()) e.name = t('common.required')
  if (!form.url.trim()) e.url = t('common.required')
  else if (!isHttpUrl(form.url)) e.url = t('prices.sources.urlInvalid')
  const willHaveKey = !!form.apiKey.trim() || (hasStoredKey.value && !form.clearKey)
  if (form.kind === 'sup2api' && !willHaveKey) e.api_key = t('prices.sources.apiKeyRequired')
  return e
}

/** What a save sends: a new source also sends its kind; an edit omits the key to keep it, sends "" to clear it, any other to replace it. */
function bodyOf(): Record<string, unknown> {
  const body: Record<string, unknown> = { name: form.name.trim(), url: form.url.trim(), options: options(), enabled: form.enabled }
  if (editing.value) {
    if (form.apiKey.trim()) body.api_key = form.apiKey.trim()
    else if (hasStoredKey.value && form.clearKey) body.api_key = ''
  } else {
    body.kind = form.kind
    if (form.kind === 'sup2api') body.api_key = form.apiKey.trim()
  }
  return body
}

/** Shows a refusal of a save: a name taken, or the fields (the vendor list's under providers). */
function showError(err: unknown) {
  if (err instanceof ApiError && err.code === 'conflict') errors.value = { name: err.message }
  else if (applyFormError(err, errors) && errors.value['options.providers']) errors.value.providers ??= errors.value['options.providers']
}

async function submit() {
  errors.value = formErrors()
  if (Object.keys(errors.value).length) return
  saving.value = true
  try {
    const body = bodyOf()
    const r = editing.value
      ? await api.patch<PriceSource>(`/price-sources/${editing.value.id}`, body)
      : await api.post<PriceSource>('/price-sources', body)
    toast(t(editing.value ? 'common.updated' : 'common.created'), 'success')
    emit('update:open', false)
    emit('saved', r || null)
  } catch (err) {
    showError(err)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <SFormModal
    :open="open"
    :title="editing ? t('prices.sources.editTitle', { name: editing.name }) : t('prices.sources.create')"
    width="lg"
    :loading="saving"
    :submit-text="editing ? t('common.save') : t('common.create')"
    :submit-attrs="{ 'data-testid': 'price-source-submit' }"
    data-testid="price-source-form"
    @update:open="emit('update:open', $event)"
    @submit="submit"
  >
      <SField :label="t('prices.sources.kindCol')" required>
        <SSegmented
          :model-value="form.kind"
          :options="kindOptions"
          block
          :aria-label="t('prices.sources.kindCol')"
          data-testid="price-source-kind"
          @update:model-value="pickKind($event as PriceSourceKind)"
        />
        <template #hint><span data-testid="price-source-kind-hint">{{ t(`prices.sources.kindHint.${form.kind}`) }}</span></template>
      </SField>

      <SField :label="t('common.name')" required :error="errors.name">
        <SInput v-model="form.name" name="name" />
      </SField>

      <SField :label="t('prices.sources.url')" required :error="errors.url" :hint="t(`prices.sources.urlHint.${form.kind}`)">
        <SInput v-model="form.url" mono class="text-xs" name="url" :placeholder="form.kind === 'sup2api' ? 'https://up.example.com' : DEFAULT_URLS[form.kind]" />
      </SField>

      <SField
        v-if="form.kind === 'sup2api'"
        :label="t('prices.sources.apiKey')"
        :required="!hasStoredKey"
        :error="errors.api_key"
        :hint="editing && hasStoredKey ? t('prices.sources.apiKeyKeep') : t('prices.sources.apiKeyHint')"
      >
        <SInput
          v-model="form.apiKey"
          type="password"
          name="api_key"
          mono
          autocomplete="new-password"
          :disabled="form.clearKey"
          :placeholder="editing && hasStoredKey ? t('prices.sources.apiKeyStored') : 'sk-s2a-...'"
        />
        <SCheckbox v-if="editing && hasStoredKey" v-model="form.clearKey" class="mt-1.5" :label="t('prices.sources.apiKeyClear')" data-testid="price-source-clear-key" />
      </SField>

      <SField
        v-if="usesProviders"
        :label="t('prices.sources.providers')"
        :error="errors.providers || errors.options"
        :hint="t(`prices.sources.providersHint.${form.kind}`)"
      >
        <STagInput v-model="form.providers" :placeholder="t('prices.sources.providersPlaceholder')" />
      </SField>

      <SSettingRow v-else :label="t('prices.sources.applyMultiplier')" :description="t('prices.sources.applyMultiplierHint')" dense flush for="price-source-multiplier">
        <SSwitch
          id="price-source-multiplier"
          v-model="form.applyMultiplier"
          :label="form.applyMultiplier ? t('prices.sources.multiplierOn') : t('prices.sources.multiplierOff')"
          data-testid="price-source-multiplier"
        />
      </SSettingRow>
      <SHint v-if="!usesProviders && errors.options" tone="danger" size="xs">{{ errors.options }}</SHint>

      <SSettingRow :label="t('common.status')" dense flush for="price-source-enabled">
        <SSwitch id="price-source-enabled" v-model="form.enabled" :label="form.enabled ? t('common.enabled') : t('common.disabled')" />
      </SSettingRow>
  </SFormModal>
</template>
