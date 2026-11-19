<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import { SBadge, SButton, SField, SHint, SInput, SSettingRow, SSettingsGroup, toast } from '@sub2api/ui'
import { applyFormError } from '@/utils/formErrors'
import { useAuthStore } from '@/stores/auth'
import { lt } from '@/i18n'
import type { LText } from '@/api/types'
import { accountTypesOf, pick, type PluginDetail, type PluginResources, type PluginResourcesInfo } from '../pluginUtil'

const props = defineProps<{ detail: PluginDetail }>()
const emit = defineEmits<{ (e: 'changed'): void }>()
const { t } = useI18n()
const auth = useAuthStore()
const canManage = computed(() => auth.has('plugin:manage'))

type Field = 'memory_mb' | 'cpu' | 'max_threads' | 'max_open_files'

/**
 * limitOnly: only the operator limits it (memory). The manifest figure is the
 * plugin's suggestion, not prefilled; empty or 0 is unlimited.
 */
const fields: Array<{ key: Field; label: string; step: string; manifest: string[]; unit?: string; limitOnly?: boolean }> = [
  { key: 'memory_mb', label: 'plugins.resources.memoryMB', step: '1', manifest: ['memoryMB', 'memory_mb'], unit: 'MB', limitOnly: true },
  { key: 'cpu', label: 'plugins.resources.cpu', step: '0.05', manifest: ['cpu'] },
  { key: 'max_threads', label: 'plugins.resources.threads', step: '1', manifest: ['maxProcs', 'max_threads', 'maxThreads'] },
  { key: 'max_open_files', label: 'plugins.resources.files', step: '1', manifest: ['maxOpenFiles', 'max_open_files'] }
]

// SInput type="number" emits a number (null when cleared); reset() stores strings.
const form = reactive<Record<Field, string | number | null>>({ memory_mb: '', cpu: '', max_threads: '', max_open_files: '' })
const errors = ref<Record<string, string>>({})
const saving = ref(false)

/** Manifest request: `resources.requested` of the detail, else the manifest's resources. */
const requested = computed<Record<string, any>>(() => {
  const fromInfo = props.detail.resources?.requested
  if (fromInfo && Object.values(fromInfo).some((v) => Number(v) > 0)) return fromInfo
  return pick<Record<string, any>>(props.detail.manifest, 'resources') || {}
})

function requestedValue(f: (typeof fields)[number]): unknown {
  const v = pick(requested.value, f.key, ...f.manifest)
  return v === 0 ? undefined : v
}

function reset() {
  const r: PluginResourcesInfo = props.detail.resources || {}
  // Admin overrides (0 = none) of the {requested, overrides, effective} shape; flat shape otherwise.
  const own: PluginResources = r.overrides || r
  for (const f of fields) {
    const o = own[f.key]
    const v = o !== undefined && o !== null && Number(o) > 0 ? o : f.limitOnly ? undefined : requestedValue(f)
    form[f.key] = v === undefined || v === null ? '' : String(v)
  }
  errors.value = {}
}
watch(() => props.detail.resources, reset, { immediate: true })

function requestedOf(f: (typeof fields)[number]): string {
  const v = requestedValue(f)
  return v === undefined ? '—' : `${v}${f.unit ? ' ' + f.unit : ''}`
}

/** The memory the plugin runs with now: a limit, or unlimited (0). */
const memoryLimit = computed(() => {
  const mb = Number(props.detail.resources?.effective?.memory_mb)
  return mb > 0 ? `${mb} MB` : t('plugins.resources.unlimited')
})

/** A platform plugin holds the bodies of the requests it serves. */
const servesRequests = computed(() => accountTypesOf(props.detail.manifest).length > 0)

function description(f: (typeof fields)[number]): string {
  if (!f.limitOnly) return t('plugins.resources.requestedValue', { value: requestedOf(f) })
  const text = t('plugins.resources.memoryLimit', { value: requestedOf(f), limit: memoryLimit.value })
  return servesRequests.value ? `${text} ${t('plugins.resources.platformMemoryHint')}` : text
}

function differs(f: (typeof fields)[number]): boolean {
  if (f.limitOnly) return Number(form[f.key]) > 0
  const v = requestedValue(f)
  return v !== undefined && form[f.key] !== '' && form[f.key] !== null && Number(form[f.key]) !== Number(v)
}

async function save() {
  const body: Record<string, number | null> = {}
  for (const f of fields) {
    const s = String(form[f.key] ?? '').trim()
    if (s === '') body[f.key] = null
    else {
      const n = Number(s)
      if (!Number.isFinite(n) || n < 0) {
        errors.value = { [f.key]: t('plugins.resources.invalid') }
        return
      }
      body[f.key] = f.key === 'cpu' ? n : Math.round(n)
    }
  }
  saving.value = true
  errors.value = {}
  try {
    const r = await api.put<{ message?: LText; restart_notified?: boolean } | null>(`/plugins/${encodeURIComponent(props.detail.key)}/resources`, body)
    // The server explains how the limits take effect (restart notified, or on next enable).
    toast((r?.message && lt(r.message)) || t('plugins.resources.saved'), 'success')
    emit('changed')
  } catch (e) {
    applyFormError(e, errors)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <SSettingsGroup :title="t('plugins.detail.tabs.resources')" :description="t('plugins.resources.hint')" data-testid="plugin-resources">
    <template v-if="canManage" #actions>
      <SButton size="sm" :disabled="saving" @click="reset">{{ t('common.reset') }}</SButton>
      <SButton size="sm" variant="primary" :loading="saving" @click="save">{{ t('common.save') }}</SButton>
    </template>
    <template v-if="canManage" #details>{{ t('plugins.resources.restartHint') }}</template>
    <SSettingRow
      v-for="f in fields"
      :key="f.key"
      :label="t(f.label)"
      :description="description(f)"
      :for="`plugin-resource-${f.key}`"
    >
      <SField :error="errors[f.key]">
        <div class="flex items-center gap-2">
          <SBadge v-if="differs(f)" tone="primary">{{ t('plugins.resources.overridden') }}</SBadge>
          <SInput
            :id="`plugin-resource-${f.key}`"
            v-model="form[f.key]"
            type="number"
            min="0"
            :step="f.step"
            :placeholder="f.limitOnly ? t('plugins.resources.unlimited') : undefined"
            class="w-32"
            :disabled="!canManage"
          />
          <SHint v-if="f.unit" inline size="xs">{{ f.unit }}</SHint>
        </div>
      </SField>
    </SSettingRow>
  </SSettingsGroup>
</template>
