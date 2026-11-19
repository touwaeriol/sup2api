<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { SInput, SSettingRow, SSettingsGroup, SSpinner, SSwitch } from '@sub2api/ui'
import { CLAUDE_MAX_TOKENS, GATEWAY_SETTINGS_RANGES, type GatewayNumberSetting, type GatewaySettings } from '@/api/types'
import SettingsSaveBar from '@/components/SettingsSaveBar.vue'
import { useSettingsForm } from '@/composables/useSettingsForm'
import { useAuthStore } from '@/stores/auth'
import ClaudeMaxTokensTable from './ClaudeMaxTokensTable.vue'
import { maxTokensMap, maxTokensRows, type MaxTokensRow } from './claudeMaxTokens'

// Gateway settings (GET/PUT /settings/gateway, CONTRACTS §8, §14.4), with the
// conversion settings of CONVERT-MATRIX §6.1 (Claude max_tokens rule, media download cap).
const { t } = useI18n()
const auth = useAuthStore()
const canManage = computed(() => auth.has('settings:manage'))

type Key = GatewayNumberSetting
// The form stays flat: the rule's switch and its rows are two fields.
const MAX_TOKENS = 'claude_max_tokens'
const MAX_TOKENS_ON = 'claude_max_tokens_enabled'
type Form = Record<Key, number | null> & { [MAX_TOKENS]: MaxTokensRow[]; [MAX_TOKENS_ON]: boolean }
const DEFAULTS: Omit<GatewaySettings, typeof MAX_TOKENS> = {
  max_attempts: 3,
  platform_call_timeout_ms: 2000,
  default_hook_timeout_ms: 300,
  platform_hotpath_timeout_ms: 300,
  queue_sticky_max_waiting: 3,
  queue_sticky_wait_seconds: 120,
  queue_fallback_max_waiting: 100,
  queue_fallback_wait_seconds: 30,
  media_download_max_mb: 64,
  max_request_body_mb: 128
}
const fields: Array<{ key: Key; unit?: string }> = [
  { key: 'platform_call_timeout_ms', unit: 'ms' },
  { key: 'default_hook_timeout_ms', unit: 'ms' },
  { key: 'platform_hotpath_timeout_ms', unit: 'ms' },
  { key: 'queue_sticky_max_waiting' },
  { key: 'queue_sticky_wait_seconds', unit: 's' },
  { key: 'queue_fallback_max_waiting' },
  { key: 'queue_fallback_wait_seconds', unit: 's' },
  { key: 'max_request_body_mb', unit: 'MB' },
  { key: 'media_download_max_mb', unit: 'MB' }
]

const { form, loading, saving, dirty, errors, isLoaded, load, save, reset } = useSettingsForm<Form, GatewaySettings>({
  path: '/settings/gateway',
  defaults: {
    ...DEFAULTS,
    [MAX_TOKENS]: maxTokensRows({ [CLAUDE_MAX_TOKENS.fallbackKey]: CLAUDE_MAX_TOKENS.fallback }),
    [MAX_TOKENS_ON]: false
  },
  fromServer: (r) => ({
    ...(Object.fromEntries(fields.map((f) => [f.key, Number(r[f.key] ?? DEFAULTS[f.key])])) as Record<Key, number>),
    [MAX_TOKENS]: maxTokensRows(r[MAX_TOKENS]?.models),
    [MAX_TOKENS_ON]: !!r[MAX_TOKENS]?.enabled
  }),
  // Client-side check for whole numbers only; ranges are enforced by the server (field errors).
  toBody: (f, errs) => {
    const body = {} as GatewaySettings
    for (const { key } of fields) {
      const n = f[key]
      if (n === null || !Number.isInteger(n)) errs[key] = t('settings.gateway.notInteger')
      else body[key] = n
    }
    // The rows are saved, and so checked, while the rule is off too.
    const maxTokens = maxTokensMap(f[MAX_TOKENS])
    if ('error' in maxTokens) errs[MAX_TOKENS] = t(`settings.gateway.maxTokens.errors.${maxTokens.error}`, CLAUDE_MAX_TOKENS)
    else body[MAX_TOKENS] = { enabled: f[MAX_TOKENS_ON], models: maxTokens.map }
    return body
  }
})

function description(k: Key): string {
  const [min, max] = GATEWAY_SETTINGS_RANGES[k]
  return `${t(`settings.gateway.hints.${k}`)} ${t('settings.gateway.range', { min, max })}`
}

onMounted(load)
</script>

<template>
  <SSettingsGroup :title="t('settings.gateway.title')" :description="t('settings.gateway.subtitle')" data-testid="gateway-settings">
    <template v-if="canManage" #actions><SettingsSaveBar :dirty="dirty" :saving="saving" :can-manage="canManage" @save="save" @reset="reset" /></template>
    <SSpinner v-if="loading && !isLoaded" block />
    <template v-else>
      <SSettingRow v-for="f in fields" :key="f.key" :label="t(`settings.gateway.fields.${f.key}`)" :description="description(f.key)" :error="errors[f.key]">
        <SInput
          v-model="form[f.key]"
          type="number"
          step="1"
          :min="GATEWAY_SETTINGS_RANGES[f.key][0]"
          :max="GATEWAY_SETTINGS_RANGES[f.key][1]"
          :suffix="f.unit"
          :name="f.key"
          class="w-full sm:w-48"
          :disabled="!canManage"
        />
      </SSettingRow>
      <SSettingRow
        :label="t('settings.gateway.fields.claude_max_tokens')"
        :description="t('settings.gateway.hints.claude_max_tokens', CLAUDE_MAX_TOKENS)"
      >
        <SSwitch
          v-model="form[MAX_TOKENS_ON]"
          :disabled="!canManage"
          :aria-label="t('settings.gateway.fields.claude_max_tokens')"
          data-testid="gateway-claude-max-tokens-enabled"
        />
      </SSettingRow>
      <!-- Rows that cannot be saved stay in sight after the rule is turned off. -->
      <SSettingRow
        v-if="form[MAX_TOKENS_ON] || errors[MAX_TOKENS]"
        :label="t('settings.gateway.maxTokens.title')"
        :description="t('settings.gateway.maxTokens.hint', CLAUDE_MAX_TOKENS)"
        :error="errors[MAX_TOKENS]"
        stacked
        data-testid="gateway-claude-max-tokens"
      >
        <ClaudeMaxTokensTable v-model="form[MAX_TOKENS]" :disabled="!canManage" :error="!!errors[MAX_TOKENS]" class="max-w-xl" />
      </SSettingRow>
    </template>
  </SSettingsGroup>
</template>
