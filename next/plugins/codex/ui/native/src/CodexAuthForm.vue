<script setup lang="ts">
// Native account form of the codex plugin (manifest accountTypes[].form
// {mode: "native", component: "CodexAuthForm"}). The account editor mounts it
// as <component v-model="credentials" :mode :account :errors> (plus
// :account-type and, UI 3.20, :proxy-ticket) and calls validate() before
// saving. Every call of a login (start, each poll, the exchange) asks
// proxyTicket for the form's current proxy choice, so each request to OpenAI
// leaves through the proxy the form names at that moment.
//
// - Only the flow of the account type is shown (codex_device: device code,
//   codex_oauth: Sign in with Codex); both, as tabs, when the type is not known.
// - A finished flow writes the credentials it returned into v-model.
// - Editing: the current email / plan / expiry and "Re-authorize". Until a
//   re-authorization finishes v-model is left as loaded, so the masked
//   sensitive fields go back as "******" and the core keeps the stored tokens.
// - The account's own User-Agent and originator (credentials user_agent /
//   originator, empty for the plugin settings' or built-in values), kept
//   across re-authorizations. Every call of a login sends their current
//   values too, so the authorization requests already use them.
import { computed, nextTick, ref, watch } from 'vue'
import { SAlert, SBadge, SButton, SDescItem, SDescriptions, SField, SHint, SInput, STabs } from '@sub2api/ui'
import type { ProxyTicketSource } from '@sub2api/host'
import { useCodexHost, type CodexCredentials } from './host'
import { agentOf, flowsFor, hasAccessToken, KNOWN_PLANS, mergeCredentials, summarize, typeOf, withAgent, type AgentField, type Flow } from './flow'
import DeviceFlow from './DeviceFlow.vue'
import OAuthFlow from './OAuthFlow.vue'

const props = withDefaults(
  defineProps<{
    modelValue: Record<string, unknown> | null | undefined
    mode?: 'create' | 'edit'
    account?: ({ id?: number; type?: string } & Record<string, unknown>) | null
    errors?: Record<string, string>
    /** The editor's account type (the auth method picked when creating). */
    accountType?: { type?: string } | null
    /** A proxy ticket of the editor's current proxy choice (UI 3.20, CONTRACTS §117). */
    proxyTicket: ProxyTicketSource
  }>(),
  { mode: 'create', account: null, errors: () => ({}), accountType: null }
)
const emit = defineEmits<{ (e: 'update:modelValue', value: Record<string, unknown>): void }>()
const host = useCodexHost()
const t = host.t

const type = computed(() => typeOf(props.accountType, props.account))
const flows = computed(() => flowsFor(type.value))
const editing = computed(() => props.mode === 'edit')

/** v-model as the editor loaded it (edit: the masked credentials), restored when a re-authorization is undone. */
const original = ref<Record<string, unknown>>({ ...(props.modelValue || {}) })
/** Credentials a flow returned in this form. */
const fresh = ref<CodexCredentials | null>(null)
const reauthOpen = ref(false)
const tab = ref<Flow>(flows.value[0])
const flowBusy = ref(false)
const localError = ref('')
const root = ref<HTMLElement>()

// Another account, or another auth method picked while creating (the editor
// resets the credentials then): start over.
watch(
  () => [props.account?.id, type.value] as const,
  () => {
    original.value = { ...(props.modelValue || {}) }
    fresh.value = null
    reauthOpen.value = false
    flowBusy.value = false
    localError.value = ''
    tab.value = flows.value[0]
  }
)
// The editor cleared the credentials under us (e.g. reopened for a new account).
watch(
  () => props.modelValue,
  (v) => {
    if (fresh.value && !hasAccessToken(v)) fresh.value = null
  }
)

const current = computed(() => summarize(original.value))
const hasCurrent = computed(() => editing.value && hasAccessToken(original.value))
/** The flows are shown: creating without fresh tokens, or re-authorizing. */
const showFlows = computed(() => !fresh.value && (!hasCurrent.value || reauthOpen.value))
const freshSummary = computed(() => summarize(fresh.value as Record<string, unknown> | null))
const tabs = computed(() => flows.value.map((f) => ({ key: f, label: t(`tabs.${f}`) })))
const errorList = computed(() => Object.entries(props.errors || {}).filter(([, v]) => !!v))

function planLabel(plan: string): string {
  if (!plan) return ''
  return (KNOWN_PLANS as readonly string[]).includes(plan.toLowerCase()) ? t(`plan.${plan.toLowerCase()}`) : plan
}

function formatTime(ms: number | null): string {
  return ms ? host.i18n.formatDateTime(ms) : t('current.unknown')
}

function onBusy(busy: boolean) {
  flowBusy.value = busy
  // A flow started after a refused save: the hint has been read.
  if (busy) localError.value = ''
}

function onAuthorized(creds: CodexCredentials) {
  fresh.value = creds
  flowBusy.value = false
  localError.value = ''
  emit('update:modelValue', mergeCredentials(props.modelValue, creds))
}

/** Throws the fresh tokens away and shows the flows again. */
function again() {
  fresh.value = null
  localError.value = ''
  emit('update:modelValue', withAgent(original.value, props.modelValue))
  reauthOpen.value = editing.value && hasCurrent.value
}

/** The account's own client headers as the form holds them now. */
const agent = computed(() => agentOf(props.modelValue))

function setAgent(k: AgentField, v: string) {
  emit('update:modelValue', { ...(props.modelValue || {}), [k]: v })
}

function openReauth() {
  reauthOpen.value = true
  localError.value = ''
}

function cancelReauth() {
  reauthOpen.value = false
  flowBusy.value = false
}

async function validate(): Promise<boolean> {
  if (hasAccessToken(props.modelValue)) {
    localError.value = ''
    return true
  }
  localError.value = t(flowBusy.value ? 'validate.inProgress' : 'validate.required')
  await nextTick()
  root.value?.scrollIntoView?.({ block: 'nearest', behavior: 'smooth' })
  return false
}

defineExpose({ validate })
</script>

<template>
  <div ref="root" class="cx-form" data-testid="codex-auth-form" :data-type="type || 'unknown'" :data-mode="mode">
    <!-- Editing: what the account holds now -->
    <div v-if="hasCurrent && !fresh" class="cx-panel" data-testid="codex-current">
      <div class="cx-panel-head">
        <span class="cx-title">{{ t('current.title') }}</span>
        <SBadge v-if="current.plan" tone="accent" size="xs">{{ planLabel(current.plan) }}</SBadge>
      </div>
      <SDescriptions dense label-width="8rem">
        <SDescItem :label="t('current.email')" :value="current.email || t('current.unknown')" />
        <SDescItem :label="t('current.expires')">
          <span class="cx-num">{{ formatTime(current.expiresAt) }}</span>
          <span v-if="current.expiresAt && current.expiresAt < Date.now()" class="cx-muted cx-ml">{{ t('current.expired') }}</span>
        </SDescItem>
        <SDescItem v-if="current.accountId" :label="t('current.accountId')" :value="current.accountId" mono />
      </SDescriptions>
      <div class="cx-actions">
        <SButton v-if="!reauthOpen" size="sm" icon="refresh" data-testid="codex-reauth" @click="openReauth">{{ t('current.reauth') }}</SButton>
        <SButton v-else size="sm" variant="ghost" data-testid="codex-reauth-cancel" @click="cancelReauth">{{ t('current.cancelReauth') }}</SButton>
      </div>
      <SHint size="xs">{{ reauthOpen ? t('current.reauthHint') : t('current.kept') }}</SHint>
    </div>

    <!-- A flow finished: the new tokens are in v-model -->
    <SAlert v-if="fresh" tone="success" :title="hasCurrent ? t('done.titleReauth') : t('done.title')" data-testid="codex-authorized">
      <span v-if="freshSummary.email" class="cx-break" data-testid="codex-authorized-email">{{ freshSummary.plan ? t('done.detail', { email: freshSummary.email, plan: planLabel(freshSummary.plan) }) : freshSummary.email }}</span>
      <span v-else>{{ t('done.noEmail') }}</span>
      <span v-if="freshSummary.expiresAt" class="cx-sub cx-num">{{ t('current.expires') }}: {{ formatTime(freshSummary.expiresAt) }}</span>
      <template #actions>
        <SButton size="sm" variant="ghost" icon="refresh" data-testid="codex-again" @click="again">{{ t('done.again') }}</SButton>
      </template>
    </SAlert>

    <!-- The flows: creating, or re-authorizing -->
    <STabs v-if="showFlows" v-model="tab" :tabs="tabs" size="sm" :aria-label="t('tabsLabel')" data-testid="codex-flows">
      <template #default="{ active }">
        <DeviceFlow v-if="active === 'device'" :key="`device-${type}`" :proxy-ticket="proxyTicket" :agent="agent" @authorized="onAuthorized" @busy="onBusy" />
        <OAuthFlow v-else-if="active === 'oauth'" :key="`oauth-${type}`" :proxy-ticket="proxyTicket" :agent="agent" @authorized="onAuthorized" @busy="onBusy" />
      </template>
    </STabs>

    <!-- The account's own client headers -->
    <div class="cx-panel" data-testid="codex-agent">
      <span class="cx-title">{{ t('agent.title') }}</span>
      <SField :label="t('agent.userAgent')" :hint="t('agent.userAgentHint')" size="sm">
        <SInput :model-value="agent.user_agent" mono autocomplete="off" spellcheck="false" placeholder="sup2api-codex/<version>" data-testid="codex-user-agent" @update:model-value="setAgent('user_agent', String($event ?? ''))" />
      </SField>
      <SField :label="t('agent.originator')" :hint="t('agent.originatorHint')" size="sm">
        <SInput :model-value="agent.originator" mono autocomplete="off" spellcheck="false" placeholder="sup2api" data-testid="codex-originator" @update:model-value="setAgent('originator', String($event ?? ''))" />
      </SField>
    </div>

    <SAlert v-if="localError" tone="danger" dense data-testid="codex-validate-error">{{ localError }}</SAlert>
    <SAlert v-if="errorList.length" tone="danger" dense :title="t('serverErrors')" data-testid="codex-server-errors">
      <ul class="cx-list">
        <li v-for="[field, message] in errorList" :key="field">
          <code class="cx-field">{{ field }}</code> {{ message }}
        </li>
      </ul>
    </SAlert>
  </div>
</template>
