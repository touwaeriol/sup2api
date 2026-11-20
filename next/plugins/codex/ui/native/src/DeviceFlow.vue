<script setup lang="ts">
// Codex device code login (codex_device): Start → POST /auth/device/start →
// the verification page and the code, a countdown, and POST
// /auth/device/poll every `interval` seconds until done / expired / error.
// A poll that could not ask OpenAI (the form's proxy refused or unusable, the
// network) shows why and polls on. Polling stops when the code expires, the
// operator cancels or the component unmounts.
import { computed, onBeforeUnmount, ref } from 'vue'
import { SAlert, SButton, SCopyButton, SHint, SIcon, SLink, SSpinner, SSteps, useNow } from '@sub2api/ui'
import type { ProxyTicketSource } from '@sub2api/host'
import { errorMessage, pollDevice, startDevice, useCodexHost, type CodexCredentials, type DeviceStart } from './host'
import { CHATGPT_SECURITY_URL, COUNTDOWN_TICK_MS, DEFAULT_CODE_TTL_SECONDS, DEFAULT_POLL_MS, formatCountdown, MAX_POLL_FAILURES, pollDelay, type FormAgent } from './flow'

const props = defineProps<{
  /** A proxy ticket of the account form's current proxy choice, taken afresh for every call of the login. */
  proxyTicket: ProxyTicketSource
  /** The account form's current User-Agent and originator, sent with every call of the login. */
  agent: FormAgent
}>()
const emit = defineEmits<{ (e: 'authorized', creds: CodexCredentials): void; (e: 'busy', busy: boolean): void }>()
const host = useCodexHost()
const t = host.t

type Phase = 'idle' | 'starting' | 'waiting' | 'expired' | 'error'
const phase = ref<Phase>('idle')
const session = ref<DeviceStart | null>(null)
const expiresAt = ref(0)
const failure = ref('')
/** Why the last poll could not ask OpenAI (the form's proxy, the network); polling goes on. */
const notice = ref('')
const now = useNow(() => (phase.value === 'waiting' ? COUNTDOWN_TICK_MS : 0))
const left = computed(() => Math.max(0, Math.round((expiresAt.value - now.value) / 1000)))

// Each start gets a generation; a reply of an older one (after cancel,
// restart or unmount) is dropped.
let generation = 0
let timer: ReturnType<typeof setTimeout> | null = null
let delay = DEFAULT_POLL_MS
let failures = 0

function setPhase(p: Phase) {
  phase.value = p
  emit('busy', p === 'starting' || p === 'waiting')
}

function stop() {
  generation++
  if (timer) clearTimeout(timer)
  timer = null
}

async function start() {
  stop()
  const gen = generation
  failure.value = ''
  notice.value = ''
  session.value = null
  setPhase('starting')
  try {
    const s = await startDevice(await props.proxyTicket(), props.agent)
    if (gen !== generation) return
    session.value = s
    expiresAt.value = Date.now() + (Number(s.expires_in) > 0 ? Number(s.expires_in) : DEFAULT_CODE_TTL_SECONDS) * 1000
    delay = pollDelay(s.interval)
    failures = 0
    setPhase('waiting')
    schedule(gen)
  } catch (e) {
    if (gen !== generation) return
    failure.value = errorMessage(e, t('actionFailed'))
    setPhase('error')
  }
}

function schedule(gen: number) {
  if (timer) clearTimeout(timer)
  timer = setTimeout(() => void poll(gen), delay)
}

async function poll(gen: number) {
  if (gen !== generation || !session.value) return
  if (Date.now() >= expiresAt.value) {
    stop()
    setPhase('expired')
    return
  }
  const id = session.value.session_id
  let ticket: string
  try {
    ticket = await props.proxyTicket()
  } catch (e) {
    if (gen !== generation) return
    // The form's proxy cannot be used, not a dropped request: say so and
    // poll on, so the login continues once the operator fixes the proxy.
    notice.value = errorMessage(e, t('actionFailed'))
    return schedule(gen)
  }
  try {
    const r = await pollDevice(id, ticket, props.agent)
    if (gen !== generation) return
    failures = 0
    if (r.interval) delay = pollDelay(r.interval)
    if (r.status === 'pending') {
      notice.value = r.message || ''
      return schedule(gen)
    }
    stop()
    if (r.status === 'done' && r.credentials) {
      setPhase('idle')
      session.value = null
      emit('authorized', r.credentials)
    } else if (r.status === 'expired') {
      setPhase('expired')
    } else {
      failure.value = r.message || t('actionFailed')
      setPhase('error')
    }
  } catch (e) {
    if (gen !== generation) return
    notice.value = ''
    // A dropped request or a restarting node: try again a few times before giving up.
    if (++failures < MAX_POLL_FAILURES) return schedule(gen)
    stop()
    failure.value = errorMessage(e, t('actionFailed'))
    setPhase('error')
  }
}

function cancel() {
  stop()
  session.value = null
  failure.value = ''
  notice.value = ''
  setPhase('idle')
}

onBeforeUnmount(stop)

const steps = computed(() => [
  { key: 'open', title: t('device.step1') },
  { key: 'code', title: t('device.step2') }
])
</script>

<template>
  <div class="cx-flow" data-testid="codex-device-flow" :data-phase="phase">
    <SHint size="xs">{{ t('intro.device') }}</SHint>

    <SAlert tone="info" dense :title="t('device.guideTitle')" data-testid="codex-device-guide">
      <p>{{ t('device.guidePath') }}</p>
      <SLink :href="CHATGPT_SECURITY_URL" external class="cx-link" data-testid="codex-device-guide-link">
        <SIcon name="external" class="cx-icon" />{{ t('device.guideOpen') }}
      </SLink>
    </SAlert>

    <div v-if="phase === 'idle' || phase === 'starting'" class="cx-actions">
      <SButton variant="primary" icon="play" :loading="phase === 'starting'" data-testid="codex-device-start" @click="start">{{ t('device.start') }}</SButton>
    </div>

    <template v-else-if="phase === 'waiting' && session">
      <SSteps :steps="steps" direction="vertical" current="code">
        <template #step-open>
          <div class="cx-step-body">
            <div class="cx-link-row">
              <SLink :href="session.verification_url" external class="cx-link" data-testid="codex-device-url">
                <SIcon name="external" class="cx-icon" />
                <span class="cx-break">{{ session.verification_url }}</span>
              </SLink>
              <SCopyButton :text="session.verification_url" :label="t('device.copyLink')" icon-only variant="secondary" size="sm" data-testid="codex-device-url-copy" />
            </div>
          </div>
        </template>
        <template #step-code>
          <div class="cx-step-body">
            <div class="cx-code-row">
              <code class="cx-code" data-testid="codex-device-code" :aria-label="t('device.codeLabel')">{{ session.user_code }}</code>
              <SCopyButton :text="session.user_code" :label="t('device.copyCode')" variant="secondary" size="md" data-testid="codex-device-copy" />
            </div>
          </div>
        </template>
      </SSteps>
      <SAlert v-if="notice" tone="warning" dense data-testid="codex-device-notice"><span class="cx-break">{{ notice }}</span></SAlert>
      <div class="cx-status" role="status">
        <SSpinner size="sm" decorative />
        <span>{{ t('device.waiting') }}</span>
        <span class="cx-num cx-muted" data-testid="codex-device-countdown">{{ t('device.expiresIn', { time: formatCountdown(left) }) }}</span>
        <SButton size="sm" variant="ghost" data-testid="codex-device-cancel" @click="cancel">{{ t('device.cancel') }}</SButton>
      </div>
    </template>

    <SAlert v-else-if="phase === 'expired'" tone="warning" dense data-testid="codex-device-expired">
      {{ t('device.expired') }}
      <template #actions>
        <SButton size="sm" icon="refresh" data-testid="codex-device-retry" @click="start">{{ t('device.retry') }}</SButton>
      </template>
    </SAlert>

    <SAlert v-else-if="phase === 'error'" tone="danger" dense :title="t('device.failed')" data-testid="codex-device-error">
      <span class="cx-break">{{ failure }}</span>
      <template #actions>
        <SButton size="sm" icon="refresh" data-testid="codex-device-retry" @click="start">{{ t('device.retry') }}</SButton>
      </template>
    </SAlert>
  </div>
</template>
