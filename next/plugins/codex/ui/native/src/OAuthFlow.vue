<script setup lang="ts">
// Sign in with Codex (codex_oauth), the Codex CLI's browser login: Generate →
// POST /auth/codex/start → the link, opened in a new window → the operator
// pastes the localhost callback address the browser could not open → POST
// /auth/codex/exchange → credentials. Re-authorizing runs the same flow.
// Not a <form>: the panel sits inside the account editor's form.
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { isApiError, type ProxyTicketSource } from '@sub2api/host'
import { SAlert, SButton, SCopyButton, SField, SHint, SIcon, SInput, SLink, SSteps } from '@sub2api/ui'
import { errorMessage, exchangeOAuth, startOAuth, useCodexHost, type CodexCredentials, type OAuthStart } from './host'
import { callbackShape, type FormAgent } from './flow'

const props = defineProps<{
  /** A proxy ticket of the account form's current proxy choice, taken afresh for every call of the login. */
  proxyTicket: ProxyTicketSource
  /** The account form's current User-Agent and originator, sent with every call of the login. */
  agent: FormAgent
}>()
const emit = defineEmits<{ (e: 'authorized', creds: CodexCredentials): void; (e: 'busy', busy: boolean): void }>()
const host = useCodexHost()
const t = host.t

const session = ref<OAuthStart | null>(null)
const callback = ref('')
const busy = ref<'' | 'start' | 'exchange'>('')
const failure = ref('')
// 'session': the exchange failed because the session is gone (expired, or
// the consent was denied); only a new link helps.
const failedStep = ref<'' | 'start' | 'exchange' | 'session'>('')
/**
 * Exchange refusals that end the session (the plugin's error codes). A
 * proxy_refused keeps it: completing again sends the next call with a fresh
 * ticket of the form's proxy.
 */
const SESSION_GONE = new Set(['session_expired', 'access_denied'])
const shape = computed(() => callbackShape(callback.value))
const shapeHint = computed(() => (shape.value === 'invalid' || shape.value === 'denied' || shape.value === 'no_code' ? t(`oauth.shape.${shape.value}`) : ''))
// A failed exchange is stale once the operator pastes another address.
watch(callback, () => {
  if (failedStep.value === 'exchange') {
    failure.value = ''
    failedStep.value = ''
  }
})

// Each call gets a generation; a reply of an older one (after another call
// or unmount) is dropped.
let generation = 0
onBeforeUnmount(() => generation++)

function setBusy(b: '' | 'start' | 'exchange') {
  busy.value = b
  emit('busy', b !== '' || !!session.value)
}

async function generate() {
  const gen = ++generation
  failure.value = ''
  failedStep.value = ''
  setBusy('start')
  try {
    const s = await startOAuth(await props.proxyTicket(), props.agent)
    if (gen !== generation) return
    session.value = s
    callback.value = ''
  } catch (e) {
    if (gen !== generation) return
    failure.value = errorMessage(e, t('actionFailed'))
    failedStep.value = 'start'
  } finally {
    if (gen === generation) setBusy('')
  }
}

async function complete() {
  if (!session.value || !callback.value.trim() || busy.value) return
  const gen = ++generation
  failure.value = ''
  failedStep.value = ''
  setBusy('exchange')
  try {
    const r = await exchangeOAuth(session.value.session_id, callback.value.trim(), await props.proxyTicket(), props.agent)
    if (gen !== generation) return
    session.value = null
    callback.value = ''
    setBusy('')
    emit('authorized', r.credentials)
  } catch (e) {
    if (gen !== generation) return
    failure.value = errorMessage(e, t('actionFailed'))
    failedStep.value = isApiError(e) && SESSION_GONE.has(e.code) ? 'session' : 'exchange'
    setBusy('')
  }
}

const steps = computed(() => [
  { key: 'open', title: t('oauth.step1') },
  { key: 'paste', title: t('oauth.step2') }
])
</script>

<template>
  <div class="cx-flow" data-testid="codex-oauth-flow" :data-phase="session ? 'link' : busy || 'idle'">
    <SHint size="xs">{{ t('intro.oauth') }}</SHint>

    <div v-if="!session" class="cx-actions">
      <SButton variant="primary" icon="link" :loading="busy === 'start'" data-testid="codex-oauth-start" @click="generate">{{ t('oauth.generate') }}</SButton>
    </div>

    <SSteps v-else :steps="steps" direction="vertical" :current="callback.trim() ? 'paste' : 'open'">
      <template #step-open>
        <div class="cx-step-body">
          <div class="cx-link-row">
            <SInput
              :model-value="session.authorize_url"
              readonly
              mono
              size="sm"
              class="cx-grow"
              :aria-label="t('oauth.linkLabel')"
              data-testid="codex-oauth-url"
              @focus="($event.target as HTMLInputElement).select()"
            />
            <SCopyButton :text="session.authorize_url" :label="t('oauth.copyLink')" icon-only variant="secondary" size="sm" />
          </div>
          <div class="cx-actions">
            <SLink :href="session.authorize_url" external class="cx-link" data-testid="codex-oauth-open">
              <SIcon name="external" class="cx-icon" />{{ t('oauth.openLink') }}
            </SLink>
            <SButton size="xs" variant="ghost" icon="refresh" :loading="busy === 'start'" :disabled="busy === 'exchange'" data-testid="codex-oauth-regenerate" @click="generate">{{ t('oauth.regenerate') }}</SButton>
          </div>
        </div>
      </template>
      <template #step-paste>
        <div class="cx-step-body">
          <SAlert tone="info" dense icon="info" data-testid="codex-oauth-note">{{ t('oauth.redirectNote') }}</SAlert>
          <SField :label="t('oauth.callbackLabel')" size="sm">
            <div class="cx-link-row cx-wrap">
              <SInput
                v-model="callback"
                mono
                class="cx-grow cx-min"
                inputmode="url"
                autocomplete="off"
                :disabled="busy === 'exchange'"
                :placeholder="t('oauth.callbackPlaceholder')"
                data-testid="codex-oauth-callback"
                @keydown.enter.prevent="complete"
              />
              <SButton variant="primary" :loading="busy === 'exchange'" :disabled="!callback.trim() || shape === 'invalid'" data-testid="codex-oauth-complete" @click="complete">{{ t('oauth.complete') }}</SButton>
            </div>
          </SField>
          <SHint v-if="shapeHint" tone="warning" size="xs" data-testid="codex-oauth-shape">{{ shapeHint }}</SHint>
        </div>
      </template>
    </SSteps>

    <SAlert v-if="failure" tone="danger" dense :title="t('oauth.failed')" data-testid="codex-oauth-error">
      <span class="cx-break">{{ failure }}</span>
      <template v-if="failedStep === 'start' || failedStep === 'session'" #actions>
        <SButton size="sm" icon="refresh" :loading="busy === 'start'" data-testid="codex-oauth-retry" @click="generate">{{
          t(failedStep === 'session' ? 'oauth.regenerate' : 'oauth.generate')
        }}</SButton>
      </template>
    </SAlert>
  </div>
</template>
