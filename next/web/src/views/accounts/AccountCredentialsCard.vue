<script setup lang="ts">
// The credentials card of the account editor's connection section: the auth
// method of a new account (types of one creation group), and the plugin's
// form for the type, an iframe page or a native component (CONTRACTS §23.5).
// v-model is the credentials; `errors` the server's errors of them, shown on
// the form (pushed into an iframe) or listed here when no form is mounted to
// show them. `collect()` (exposed) validates and returns what to save.
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { SAlert, SBadge, SCard, SChoiceCards, SField, SHint, SSpinner, toast } from '@sub2api/ui'
import type { Account, AccountType } from '@/api/types'
import PluginComponent from '@/components/plugin/PluginComponent.vue'
import PluginIframe from '@/components/plugin/PluginIframe.vue'
import PluginSlot from '@/components/plugin/PluginSlot.vue'
import { useAccountCaps } from '@/composables/useAccountCaps'
import { lt } from '@/i18n'
import { assetURL, usePluginStore } from '@/stores/plugins'

const props = defineProps<{
  accountType: AccountType | null
  /** The types a new account may switch to (its creation group). */
  accountTypeOptions?: AccountType[]
  account?: Account | null
  errors: Record<string, string>
  saving: boolean
  /** The native form's proxyTicket (useProxyChoice). */
  proxyTicket: () => Promise<string>
}>()
const credentials = defineModel<Record<string, unknown>>({ required: true })
const emit = defineEmits<{ (e: 'change-type', at: AccountType): void }>()
const { t } = useI18n()
const plugins = usePluginStore()
const editing = computed(() => !!props.account?.id)
const caps = useAccountCaps(editing)
const mode = computed(() => props.accountType?.form.mode)

const authOptions = computed(() => (props.accountTypeOptions || []).map((at) => ({ value: at.type, label: lt(at.auth_method_label) || at.type, disabled: !caps.creatable(at) })))
/** Guarded settings stay at the official values for this caller (the server enforces it). */
const settingsGuarded = computed(() => !!props.accountType?.guarded_settings?.length && !caps.customSettings.value)
function changeAuth(value: unknown) {
  const at = props.accountTypeOptions?.find((at) => at.type === value)
  if (!editing.value && at && at.type !== props.accountType?.type) emit('change-type', at)
}

const iframe = ref<InstanceType<typeof PluginIframe>>()
const nativeRef = ref<{ validate?: () => boolean | Promise<boolean> }>()
const uiPlugin = computed(() => (props.accountType ? plugins.plugin(props.accountType.plugin_key) : undefined))
const iframeSrc = computed(() => (uiPlugin.value && props.accountType?.form.page ? assetURL(uiPlugin.value, props.accountType.form.page) : ''))
// The plugin list is still loading: show a spinner, not "form unavailable"
// (a native form waits for its plugin's module too, in PluginComponent).
const pluginFormWaiting = computed(() => !plugins.listed)
/** The errors no plugin form is mounted to show (an older form mode, a form that failed to load). */
const unshownCredentialErrors = computed(() => (nativeRef.value || iframe.value ? [] : Object.entries(props.errors)))
watch(
  () => props.errors,
  (e) => {
    if (mode.value === 'iframe' && Object.keys(e).length) iframe.value?.setErrors(e)
  }
)

/** The credentials to save, once the plugin's form accepts them; null when it refuses them (it shows why). */
async function collect(): Promise<Record<string, unknown> | null> {
  if (!props.accountType) return credentials.value
  if (mode.value === 'native') {
    const validate = nativeRef.value?.validate
    return typeof validate === 'function' && !(await validate()) ? null : credentials.value
  }
  if (mode.value !== 'iframe') return credentials.value
  if (!iframe.value?.isReady()) {
    toast(t('pluginHost.frameNotReady'), 'warning')
    return null
  }
  const v = await iframe.value.validate().catch(() => ({ ok: false }))
  return v?.ok ? (await iframe.value.getValue()) || {} : null
}
defineExpose({ collect })
</script>

<template>
  <SCard :title="t('accounts.credentials')" :subtitle="accountType ? t('accounts.editorUi.credentialsHint', { plugin: lt(accountType.plugin_name) || accountType.plugin_key }) : ''">
    <template v-if="accountType?.auth_method_label && (editing || authOptions.length <= 1)" #actions>
      <SBadge tone="accent">{{ lt(accountType.auth_method_label) }}</SBadge>
    </template>

    <SField v-if="!editing && authOptions.length > 1" :label="t('accounts.editorUi.authMethod')" class="mb-5">
      <SChoiceCards :model-value="accountType?.type" :options="authOptions" :cols="2" :disabled="saving" data-testid="auth-method" @update:model-value="changeAuth" />
    </SField>

    <SHint v-if="settingsGuarded" size="xs" class="mb-3" data-testid="guarded-settings-hint">{{ t('accounts.guardedSettingsHint') }}</SHint>
    <SAlert v-if="account?.orphaned || !accountType" tone="warning">{{ t('accounts.orphanedEdit') }}</SAlert>
    <template v-else-if="mode === 'iframe'">
      <PluginIframe
        v-if="iframeSrc"
        ref="iframe"
        :plugin-key="accountType.plugin_key"
        :page="accountType.form.page"
        :src="iframeSrc"
        mode="account-form"
        :value="credentials"
        :min-height="200"
        @change="credentials = $event || {}"
      />
      <div v-else-if="pluginFormWaiting" class="flex justify-center py-6"><SSpinner /></div>
      <SHint v-else tone="danger">{{ t('accounts.formUnavailable') }}</SHint>
    </template>
    <template v-else-if="mode === 'native'">
      <PluginComponent v-slot="{ component }" :plugin-key="accountType.plugin_key" :name="accountType.form.component">
        <component :is="component" ref="nativeRef" v-model="credentials" :mode="editing ? 'edit' : 'create'" :account="account" :account-type="accountType" :errors="errors" :proxy-ticket="proxyTicket" />
      </PluginComponent>
    </template>
    <!-- An older package's form mode: the credentials are sent back unchanged. -->
    <SAlert v-else tone="warning" data-testid="form-unavailable-old">{{ t('pluginHost.outdated') }}</SAlert>
    <SAlert v-if="unshownCredentialErrors.length" tone="danger" :title="t('accounts.credentialErrors')" class="mt-3" data-testid="credential-errors">
      <p v-for="[field, message] in unshownCredentialErrors" :key="field"><code>{{ field }}</code>: {{ message }}</p>
    </SAlert>

    <div class="mt-4 space-y-3 empty:hidden">
      <PluginSlot name="account.form.widgets" :props="{ account, accountType, credentials }" />
    </div>
  </SCard>
</template>
