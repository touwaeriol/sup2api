<script setup lang="ts">
import { computed, inject, onMounted, reactive, ref, watch } from 'vue'
import { isApiError } from '@sub2api/host'
import {
  SAlert,
  SButton,
  SchemaField,
  SCode,
  SHint,
  SInput,
  SSegmented,
  SSelect,
  SSettingRow,
  SSettingsGroup,
  SSpinner,
  SCHEMA_WIDGETS_KEY,
  childUI,
  fieldHelp,
  fieldLabel,
  validateSchema
} from '@sub2api/ui'
import JevQuestionsEditor from './JevQuestionsEditor.vue'
import VerdictCacheRow from './VerdictCacheRow.vue'
import { BLOCK_FORMATS, blockResponse, type BlockFormat } from './blockPreview'
import { useCatalog } from './catalog'
import { enumOptions, joinList } from './format'
import { applyErrorsOrToast, clearErrors, type FieldErrors } from './forms'
import { mergeSettings, type PageValues } from './settingsMerge'
import { CACHE_DAYS_KEY, displayValues, editableKeys, LEGACY_KEY, schemaKeys, sectionsOf, sentSetting, settingSections, settingsKey, verdictCacheOff, withRetiredMirrored, type SettingSection } from './settingsForm'
import type { SectionKey } from './nav'
import { useAsync } from './useAsync'
import { fetchDefaults, fetchPluginSettings, jevPayload, savePluginSettings, SETTINGS_MASK, useModHost } from './host'
import type { JevQuestions } from './types'

// One page of the moderation settings (CONTRACTS §72.15; the plugin page is
// where they are edited, ui.settings mode native, §75.5): the fields of
// forms/settings.schema.json in the ui:section groups of
// forms/settings.ui.json whose page is this one (nav.ts), rendered by the schema widgets (the console's own
// among them: the allowlist and the blocklist pick their users with
// entity-select), except a few with their own editor here: the block
// response preview per API format, the AI analysis channel picker, the
// webhook address and secret ("set" / clear) and the Jev default question
// set. The converted 0.1 fields are hidden and go back as stored; a stored
// 0.1 API key can only be cleared.
//
// 0.8.0: `mode` is the switch and the default level; the allowlist starts
// from the retired exempt_user_ids until it is saved, and the verdict cache
// days from the retired cache_ttl_seconds (settingsForm.ts). Of the retired
// keys only exempt_user_ids is written: a save of the allowlist or of the
// blocklist puts the allowlist without the blocklisted users there, for
// nodes still on 0.7.2 (withRetiredMirrored). Under the cache days,
// moderation:manage users can empty the verdict cache of every node
// (VerdictCacheRow).
//
// A save re-reads the stored values and writes back only the keys of this
// page edited since it loaded (settingsMerge.ts), so a change made elsewhere
// meanwhile is kept, also to a key of this page left alone (the core
// settings API has no version to compare). A key someone else changed that
// this page changed too is confirmed first, then this page's value wins. A
// number left empty is not stored, so the schema default applies.
const props = defineProps<{ page: SectionKey }>()
const host = useModHost()
const t = host.t
const text = (v: unknown) => (v ? host.i18n.text(v as never) : '')
const catalog = useCatalog()
/** The console's schema widgets (a SchemaForm injects them itself; the fields here are rendered one by one). */
const widgets = inject(SCHEMA_WIDGETS_KEY, undefined)

const WEBHOOK = ['notify_webhook_url', 'notify_webhook_secret'] as const
type WebhookKey = (typeof WEBHOOK)[number]

/** Values shown by the schema widgets. */
const form = reactive<Record<string, unknown>>({})
/** The AI analysis channel picker (0 = none). */
const channelId = ref(0)
/** Webhook inputs: what is typed; kept = the stored value is kept while its input stays empty. */
const webhook = reactive<Record<WebhookKey, string>>({ notify_webhook_url: '', notify_webhook_secret: '' })
const kept = reactive<Record<string, boolean>>({})
const jev = ref<JevQuestions | null>(null)
const errs = reactive<FieldErrors>({})
const saving = ref(false)
/** Collapsed groups opened by the user. */
const opened = reactive<Record<string, boolean>>({})

const copyQuestions = (q: JevQuestions): JevQuestions => ({ preamble: q.preamble || '', items: q.items.map((x) => ({ ...x })) })
/** The stored values when the page loaded, and this page's values then (what a save compares with). */
let loaded: Record<string, unknown> = {}
/** The stored settings keep the verdict cache off (the retired cache_ttl_seconds = 0). */
const cacheOff = ref(false)
const initial = ref<PageValues>({})

const { data, loading, error, reload } = useAsync(() => Promise.all([fetchPluginSettings(), fetchDefaults()]), () => t('settingsTab.loadFailed'))
const settings = computed(() => data.value?.[0] ?? null)
const defaults = computed(() => data.value?.[1] ?? null)
const schema = computed(() => settings.value?.schema ?? null)
const ui = computed(() => settings.value?.ui_schema ?? null)
const sections = computed<SettingSection[]>(() => sectionsOf(settingSections(schema.value, ui.value), props.page))
/** The keys of this page (errors on other keys are toasted; the other pages' keys go back as stored). */
const keys = computed(() => editableKeys(sections.value, settings.value?.values || {}, props.page))
const widgetKeys = computed(() => schemaKeys(keys.value))

const prop = (k: string) => schema.value?.properties?.[k] ?? {}
const fieldUI = (k: string) => childUI(ui.value || undefined, k)
const locale = () => host.i18n.locale.value
const label = (k: string) => fieldLabel(k, prop(k), fieldUI(k), locale())
const help = (k: string) => fieldHelp(prop(k), fieldUI(k), locale())
/** The schema default of a key, for an empty input. */
const schemaDefault = (k: string) => {
  const v = prop(k).default
  return typeof v === 'number' || typeof v === 'string' ? v : ''
}

watch(data, (v) => {
  if (!v) return
  const stored = v[0].values || {}
  for (const k of Object.keys(form)) delete form[k]
  Object.assign(form, displayValues(v[0].schema, stored, widgetKeys.value))
  // the picker has no empty choice: an unset channel shows its default
  const ch = stored.analysis_channel_id
  channelId.value = typeof ch === 'number' ? ch : Number(schemaDefault('analysis_channel_id')) || 0
  for (const k of [...WEBHOOK, LEGACY_KEY]) kept[k] = stored[k] === SETTINGS_MASK
  for (const k of WEBHOOK) {
    const x = stored[k]
    webhook[k] = kept[k] || typeof x !== 'string' ? '' : x
  }
  const set = stored.jev_default_set as JevQuestions | null | undefined
  jev.value = set ? copyQuestions(set) : null
  clearErrors(errs)
  loaded = { ...stored }
  cacheOff.value = verdictCacheOff(stored)
  initial.value = pageValues()
})
onMounted(reload)
defineExpose({ reload })

/** A webhook value as sent: the new value, the mask to keep the stored one, or undefined to clear it (a secret is sent as typed). */
function webhookValue(k: WebhookKey) {
  const v = k === 'notify_webhook_url' ? webhook[k].trim() : webhook[k]
  return v || (kept[k] ? SETTINGS_MASK : undefined)
}

/** The keys of this page as sent; undefined = removed (its default applies). */
function pageValues(): PageValues {
  const out: PageValues = {}
  for (const k of widgetKeys.value) out[k] = sentSetting(prop(k), loaded, k, form[k])
  if (keys.value.includes('analysis_channel_id')) out.analysis_channel_id = Number(channelId.value) || 0
  for (const k of WEBHOOK) if (keys.value.includes(k)) out[k] = webhookValue(k)
  if (keys.value.includes('jev_default_set')) out.jev_default_set = jev.value ? jevPayload(jev.value) : null
  if (keys.value.includes(LEGACY_KEY)) out[LEGACY_KEY] = kept[LEGACY_KEY] ? SETTINGS_MASK : undefined
  return out
}
const dirty = computed(() => !!settings.value && JSON.stringify(pageValues()) !== JSON.stringify(initial.value))

function setField(k: string, v: unknown) {
  form[k] = v
  delete errs[k]
}
const clearError = (k: string) => delete errs[k]
function toggleLegacyKey() {
  kept[LEGACY_KEY] = !kept[LEGACY_KEY]
  clearError(LEGACY_KEY)
}

/** Schema checks of the changed keys before the request (the core checks again). */
function validate(current: PageValues): boolean {
  if (!schema.value) return true
  const v = (key: string, params?: Record<string, unknown>) => host.i18n.t(`ui.schema.v.${key}`, params)
  const found = validateSchema(schema.value, ui.value || undefined, current, {
    required: v('required'),
    minLength: (n) => v('minLength', { n }),
    maxLength: (n) => v('maxLength', { n }),
    pattern: v('pattern'),
    minimum: (n) => v('minimum', { n }),
    maximum: (n) => v('maximum', { n }),
    integer: v('integer'),
    url: v('url'),
    email: v('email'),
    minItems: (n) => v('minItems', { n }),
    maxItems: (n) => v('maxItems', { n }),
    text
  })
  const changed = keys.value.filter((k) => JSON.stringify(initial.value[k] ?? null) !== JSON.stringify(current[k] ?? null))
  for (const [path, msg] of Object.entries(found)) {
    const k = settingsKey(path)
    if (changed.includes(k)) errs[k] ||= msg
  }
  return !Object.keys(errs).length
}

async function save() {
  clearErrors(errs)
  const current = pageValues()
  if (!validate(current)) return
  saving.value = true
  try {
    const latest = await fetchPluginSettings()
    const m = mergeSettings(keys.value, loaded, latest.values || {}, initial.value, current)
    if (!m.changed.length) {
      host.toast(t('settingsTab.noChanges'), 'info')
      return
    }
    if (m.conflicts.length) {
      const names = joinList(m.conflicts.map((k) => (k === LEGACY_KEY ? t('settingsTab.legacyKey') : label(k))))
      if (!(await host.confirm({ message: t('settingsTab.conflictConfirm', { keys: names }), confirmText: t('settingsTab.overwrite') }))) return
    }
    await savePluginSettings(withRetiredMirrored(m.values, m.changed))
    host.toast(t('saved'), 'success')
    await reload()
  } catch (e) {
    // The first error of a key is shown at its input; others are toasted. A
    // kept secret the core asks to re-enter (§75.1) says so in the plugin's
    // words; the hidden 0.1 key cannot be re-entered, only cleared.
    applyErrorsOrToast(errs, e, keys.value, settingsKey)
    if (isApiError(e)) {
      for (const [path, code] of Object.entries(e.fieldCodes)) {
        const k = settingsKey(path)
        if (code === 'reenter') errs[k] = k === LEGACY_KEY ? t('settingsTab.legacyKeyReenter') : t('settingsTab.reenter')
      }
    }
  } finally {
    saving.value = false
  }
}

/** A collapsed group is open while the user keeps it open or one of its fields has an error. */
const isOpen = (g: SettingSection) => !g.collapsed || !!opened[g.id] || g.keys.some((k) => errs[k])

const format = ref<BlockFormat>('openai')
const formatOptions = computed(() => enumOptions('settingsTab.format', BLOCK_FORMATS))
/** The block response as sent, empty inputs taking their defaults. */
const preview = computed(() =>
  blockResponse(format.value, Number(form.block_status || schemaDefault('block_status')), String(form.block_message || schemaDefault('block_message')))
)

const channelOptions = computed(() => [
  { value: 0, label: t('settingsTab.noAnalysis') },
  ...catalog.state.channels.filter((c) => c.type === 'llm').map((c) => ({ value: c.id, label: c.enabled ? c.name : `${c.name} (${t('disabled')})` }))
])
/** The analysis channel is not a usable llm channel (status channel_problem): the server leaves analyses off. */
const channelError = computed(() => {
  const id = Number(channelId.value) || 0
  if (errs.analysis_channel_id || !id || !catalog.state.loaded) return errs.analysis_channel_id || ''
  const ch = catalog.state.channels.find((c) => c.id === id && c.type === 'llm')
  if (!ch) return t('settingsTab.analysisChannelInvalid', { id })
  return ch.enabled ? '' : t('statusLine.analysisProblem.unusable')
})

const jevMode = computed({
  get: () => (jev.value ? 'custom' : 'builtin'),
  set: (m: string) => (jev.value = m === 'custom' && defaults.value ? copyQuestions(defaults.value.builtin) : null)
})
const jevOptions = computed(() => [
  { value: 'builtin', label: t('settingsTab.jevBuiltin', { v: defaults.value?.builtin_version || '' }) },
  { value: 'custom', label: t('settingsTab.jevCustom') }
])
</script>

<template>
  <div>
    <SSpinner v-if="loading && !data" block />
    <SAlert v-else-if="error" tone="danger">{{ error }}</SAlert>
    <SAlert v-else-if="!sections.length" tone="danger">{{ t('settingsTab.noSchema') }}</SAlert>
    <form v-else class="mod-form mod-form-page" @submit.prevent="save">
      <SAlert v-if="keys.includes(LEGACY_KEY)" :tone="kept[LEGACY_KEY] ? 'warning' : 'info'" data-testid="mod-legacy-key">
        {{ kept[LEGACY_KEY] ? t('settingsTab.legacyKeyKept') : t('settingsTab.legacyKeyCleared') }}
        <template v-if="errs[LEGACY_KEY]">
          <br />
          <span class="mod-text-danger">{{ errs[LEGACY_KEY] }}</span>
        </template>
        <template #actions>
          <SButton size="sm" @click="toggleLegacyKey">{{ kept[LEGACY_KEY] ? t('settingsTab.legacyKeyClear') : t('settingsTab.legacyKeyUndo') }}</SButton>
        </template>
      </SAlert>

      <SSettingsGroup
        v-for="g in sections"
        :key="g.id"
        :title="text(g.title) || undefined"
        :description="text(g.description) || undefined"
        :collapsible="g.collapsed"
        :open="isOpen(g)"
        @update:open="opened[g.id] = $event"
      >
        <template v-for="k in g.keys" :key="k">
          <SSettingRow v-if="k === 'analysis_channel_id'" :label="label(k)" :description="help(k) || undefined" :error="channelError || undefined">
            <SSelect v-model="channelId" class="mod-setting-control" :options="channelOptions" />
          </SSettingRow>
          <SSettingRow
            v-else-if="k === 'notify_webhook_url' || k === 'notify_webhook_secret'"
            :label="label(k)"
            :description="(kept[k] ? t('settingsTab.secretKept') : help(k)) || undefined"
            :error="errs[k]"
            stacked
          >
            <SInput
              v-model="webhook[k]"
              class="mod-grow"
              :type="k === 'notify_webhook_secret' ? 'password' : 'text'"
              :maxlength="k === 'notify_webhook_secret' ? 200 : 1000"
              :placeholder="kept[k] ? SETTINGS_MASK : k === 'notify_webhook_url' ? 'https://' : ''"
              autocomplete="new-password"
              @update:model-value="clearError(k)"
            />
            <SButton v-if="kept[k]" size="sm" variant="ghost" @click="kept[k] = false">
              {{ k === 'notify_webhook_url' ? t('settingsTab.clearUrl') : t('settingsTab.clearSecret') }}
            </SButton>
          </SSettingRow>
          <SSettingRow v-else-if="k === 'jev_default_set'" :label="label(k)" :description="help(k) || undefined" :error="errs[k]" stacked>
            <div class="mod-form mod-setting-wide">
              <SSegmented v-model="jevMode" :options="jevOptions" size="sm" />
              <JevQuestionsEditor v-if="jev" v-model="jev">
                <template #toolbar>
                  <SButton size="sm" @click="jevMode = 'custom'">{{ t('settingsTab.jevCopyBuiltin') }}</SButton>
                </template>
              </JevQuestionsEditor>
            </div>
          </SSettingRow>
          <SchemaField
            v-else
            :name="k"
            :path="k"
            :schema="prop(k)"
            :ui="fieldUI(k)"
            :model-value="form[k]"
            :root="form"
            :errors="errs"
            :widgets="widgets"
            layout="settings"
            @update:model-value="setField(k, $event)"
          />
          <VerdictCacheRow v-if="k === CACHE_DAYS_KEY" :off="cacheOff" />
          <SSettingRow v-if="k === 'block_message'" :label="t('settingsTab.preview')" stacked>
            <div class="mod-form mod-setting-wide">
              <SSegmented v-model="format" :options="formatOptions" size="sm" />
              <SCode :text="`HTTP ${preview.status}\n${preview.body}`" class="mod-text-block" />
              <SHint size="xs">{{ t('settingsTab.streamNote') }}</SHint>
            </div>
          </SSettingRow>
        </template>
      </SSettingsGroup>

      <div class="mod-editor-footer">
        <SHint v-if="dirty" size="xs" tone="warning" class="mod-mr-auto">{{ t('settingsTab.unsaved') }}</SHint>
        <SButton type="submit" variant="primary" :loading="saving">{{ t('save') }}</SButton>
      </div>
    </form>
  </div>
</template>
