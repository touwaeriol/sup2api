<script setup lang="ts">
// Claude authorization of a Claude Code (CCGateway) managed account on a draft
// runtime, shown in the account editor (docs/CCGATEWAY-DRAFT-RUNTIMES.md §5,
// docs/CCGATEWAY-REAUTH.md). Same steps and state machine (./ccgAuthFlow) for
// two cases:
//
// - `draft` (new account): ① a proxy is picked in the editor → ② "start the
//   container" creates a draft runtime (POST /system/ccgateway/drafts) → ③
//   the Claude link is requested by itself once the container is ready → ④
//   the code (code#state) is pasted back → authorized. The editor saves only
//   then, sending the draft key (`ccgateway_runtime`) and calling adopt().
//   A proxy change calls PUT drafts/:key; unmounting (editor closed, account
//   type switched) deletes a draft that was not adopted (best effort, the
//   core's sweep removes the rest).
// - `reauthKey` + `reauthAccountId` (saved account): use the existing account
//   runtime. A successful OAuth callback refreshes its credentials in place;
//   closing the editor cancels only the login session, never the runtime.
//
// While a draft is shown it is touched every minute so the core's sweep does
// not take it while the user is busy elsewhere. Every state and error is a
// code (status / details.reason) translated via ccgateway.status.* and
// ccgateway.reason.*; an unknown reason shows the API's English message.
// `canEdit`: the caller may edit this account (account:update / own:update).
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, isApiError } from '@sub2api/host'
import { SAlert, SButton, SCopyButton, SField, SHint, SIcon, SInput, SLink, SSteps, toast } from '@sub2api/ui'
import { useAuthStore } from '@/stores/auth'
import type { CcgAuthSession, CcgDraft, CcgRuntimeHealth, CcgRuntimeStatus, CcgSyncResult } from '@/api/types'
import { useProxiesLookup } from '@/composables/lookups'
import { ACCOUNT_KEYS } from '@/composables/useOwnership'
import BusyText from '@/components/BusyText.vue'
import { CCGATEWAY_PAGE } from '@/views/plugins/pluginUtil'
import CcgAuthHeader from './CcgAuthHeader.vue'
import CcgBlockedAlert from './CcgBlockedAlert.vue'
import CcgErrorAlert from './CcgErrorAlert.vue'
import CredentialStatusNotice from './CredentialStatusNotice.vue'
import { canAutoStartAuthorization } from './credentialStatus'
import { usableSession, sessionExpired } from './validation'
import { useCcgText } from './ccgError'
import { DRAFTS, discardDraft, longCall } from './ccgApi'
import {
  containerPhase,
  currentStep,
  isDraftKey,
  isProxyReason,
  looksLikeAuthCode,
  readyToSave,
  reasonOf,
  secondsLeft,
  sessionEnded,
  setupProblems,
  stepStates,
  stepsOf,
  type CcgMode,
  type CcgSetupProblem,
  type CcgStep
} from './ccgAuthFlow'

const props = defineProps<{ draft?: boolean; reauthKey?: string | null; reauthAccountId?: number; proxyId?: number | null; canEdit?: boolean; preparationUrl?: string; settings?: Record<string, unknown>; configurationReady?: boolean }>()
const emit = defineEmits<{
  (e: 'state', s: { key: string | null; authorized: boolean; subscription_type?: string }): void
  (e: 'authorized'): void
  (e: 'fix-proxy'): void
  /** Re-authorization: the draft is gone (reason draft_not_found) or broken, the parent starts a new one. */
  (e: 'restart', reason: string): void
}>()
const { t } = useI18n()
const auth = useAuthStore()
const { proxies } = useProxiesLookup()
const { describe, statusLabel } = useCcgText()
const draftsBase = computed(() => (props.preparationUrl ? '/account-preparations' : DRAFTS))
const POLL_MS = 2000
/** Draft keep-alive: the core sweeps drafts untouched for 15 minutes. */
const HEARTBEAT_MS = 60_000
/** The CCGateway page, scrolled to the runtime card (install / upgrade the containers). */
const settingsLink = `${CCGATEWAY_PAGE}#ccgateway-runtime`

/** A re-authorization of a saved account (the draft is given); otherwise a new account's draft. */
const inPlace = computed(() => Number.isSafeInteger(props.reauthAccountId) && (props.reauthAccountId ?? 0) > 0)
const reauthCompleted = ref(false)
const reauth = computed(() => !props.draft && (inPlace.value || isDraftKey(props.reauthKey)))
const mode = computed<CcgMode>(() => (reauth.value ? 'reauth' : 'draft'))
const canManage = computed(() => auth.has('settings:manage') || (reauth.value ? !!props.canEdit : auth.has([...ACCOUNT_KEYS.create])))

/** New account: draft key once created, and the proxy the draft runs with. */
const draftKey = ref<string | null>(null)
const draftProxy = ref<number | null | undefined>(undefined)
/** The draft shown: the new account's, or the re-authorization's. */
const key = computed(() => (reauth.value ? props.reauthKey! : draftKey.value))
/** Base path of the draft runtime, '' while there is none. */
const target = computed(() => inPlace.value ? `/system/ccgateway/accounts/${props.reauthAccountId}` : (key.value ? `${draftsBase.value}/${key.value}` : ''))
const pickedProxy = () => props.proxyId ?? null

const status = ref<CcgRuntimeStatus | null>(null)
const health = ref<CcgRuntimeHealth | null>(null)
const session = ref<CcgAuthSession | null>(null)
const opened = ref(false)
const code = ref('')
/** Which action runs: drives the spinners and disables the buttons. */
const busy = ref<'' | 'create' | 'proxy' | 'sync' | 'status' | 'start' | 'complete' | 'cancel'>('')
/** A user action runs (the background status poll does not count). */
const acting = computed(() => !!busy.value && busy.value !== 'status')
/** The failed step: generic title, the translated cause and (container step) the setup problems found. */
const failure = ref<{ step: CcgStep; title: string; detail?: string; reason?: string; setup?: CcgSetupProblem[] } | null>(null)
/** The link was requested automatically once for this draft. */
let autoStarted = false
/** GET .../session was asked once for this runtime (resumes an unfinished authorization). */
let sessionChecked = false
/** The account was saved with this draft: it is not deleted any more. */
let adopted = false
let alive = true
let serial = 0
let lastCall = 0
let preparationAttempt = ''
let pollTimer: ReturnType<typeof setInterval> | undefined
let clockTimer: ReturnType<typeof setInterval> | undefined
const now = ref(Date.now())

const phase = computed(() => containerPhase(status.value?.status))
const flow = computed(() => ({
  mode: mode.value,
  proxy: true, // no proxy is direct egress
  created: !!target.value,
  container: failure.value?.step === 'container' ? ('error' as const) : phase.value,
  loggedIn: inPlace.value && !reauthCompleted.value ? false : (health.value ? health.value.logged_in : null),
  hasSession: !!session.value,
  opened: opened.value,
  errorStep: failure.value?.step ?? null
}))
const step = computed(() => currentStep(flow.value))
const states = computed(() => stepStates(flow.value))
/** The flow's steps for SSteps (its "pending" is the flow's "todo"). */
const stepItems = computed(() => stepsOf(mode.value).map(key => ({ key, title: t(`ccgateway.accountAuth.steps.${key}`), state: states.value[key] === 'todo' ? ('pending' as const) : states.value[key] })))
const authorizedDraft = computed(() => readyToSave(flow.value))
const headerState = computed(() => {
  if (step.value === 'done') return 'authorized'
  return target.value && health.value && !health.value.logged_in ? 'loggedOut' : null
})
const containerName = computed(() => status.value?.container || (key.value ? `ccg-${key.value}` : ''))
const proxyName = computed(() => {
  const id = props.proxyId
  if (id == null) return t('ccgateway.accountAuth.proxyDirect')
  return proxies.value.find((p) => p.id === id)?.name || `#${id}`
})
/** The link step shows its error, the open session, or the button that asks for one. */
const showLogin = computed(() => !!target.value && (['current', 'error'].includes(states.value.login) || failure.value?.step === 'login' || (states.value.login === 'done' && !!session.value)))
const left = computed(() => (session.value ? secondsLeft(session.value.expires_at, now.value) : 0))
const expired = computed(() => !!session.value && left.value <= 0)
const leftText = computed(() => {
  const s = left.value
  return s >= 60 ? `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}` : `${s}s`
})
const codeShapeWarn = computed(() => !!code.value.trim() && !looksLikeAuthCode(code.value))

function fail(stepKey: CcgStep, titleKey: string, e?: unknown, setup?: CcgSetupProblem[]) {
  failure.value = { step: stepKey, title: t(`ccgateway.accountAuth.${titleKey}`), detail: e === undefined ? undefined : describe(e), reason: reasonOf(e) || undefined, setup }
}

/** Container failures: tell "not set up" (link to the settings) apart from a runtime error. */
async function containerFailure(titleKey: string, e: unknown, stamp: number) {
  if (isApiError(e) && e.status === 403) {
    fail('container', reauth.value ? 'readOnly' : 'noCreate', e)
    return
  }
  const reason = reasonOf(e)
  let setup: CcgSetupProblem[] = []
  if (!reason || reason === 'not_configured') {
    try {
      setup = setupProblems(await api.get<{ account_runtimes?: boolean; mode?: string; has_admin_key?: boolean }>('/system/ccgateway/remote-config'))
    } catch {
      // the settings are unreadable too: keep the generic message
    }
  }
  if (stamp === serial && alive) fail('container', titleKey, e, setup)
}

/** Reads container status and, when ready, the Claude login state. */
async function refresh(): Promise<void> {
  const base = target.value
  if (!base || !canManage.value) return
  const stamp = ++serial
  lastCall = Date.now()
  let s: CcgRuntimeStatus
  try {
    s = await api.get<CcgRuntimeStatus>(`${base}/status`)
  } catch (e) {
    if (stamp === serial) await containerFailure('statusFailed', e, stamp)
    return
  }
  if (stamp !== serial) return
  status.value = s
  if (failure.value?.step === 'container') failure.value = null
  if (containerPhase(s.status) !== 'ready') {
    health.value = null
    return
  }
  try {
    const h = await api.get<CcgRuntimeHealth>(`${base}/health`)
    if (stamp !== serial) return
    health.value = h
  } catch (e) {
    if (stamp === serial) fail('container', 'healthFailed', e)
    return
  }
  if (!sessionChecked && !session.value) {
    sessionChecked = true
    await resumeSession(base, stamp)
  }
  maybeAutoStart()
}

/** The authorization session the container holds (link + code box), if it is still usable. */
async function pendingSession(base: string): Promise<CcgAuthSession | null> {
  try {
    const s = await api.get<CcgAuthSession | null>(`${base}/session`)
    return usableSession(s) ? s : null
  } catch {
    return null
  }
}

/** Resumes an unfinished authorization session after a reload. */
async function resumeSession(base: string, stamp: number) {
  const s = await pendingSession(base)
  if (stamp !== serial || !s) return
  session.value = s
  // The link was most likely opened already: show the code box too.
  opened.value = true
}

/** Replaces the shown session with the one the container holds now; false when there is none. */
async function adoptPendingSession(base: string): Promise<boolean> {
  const s = await pendingSession(base)
  if (base !== target.value || !s || s.session_id === session.value?.session_id) return false
  session.value = s
  opened.value = true
  return true
}

/** The link is requested as soon as the draft's container is ready (once per draft). */
function maybeAutoStart() {
  if (autoStarted || !canManage.value) return
  if (phase.value !== 'ready' || (!inPlace.value && !canAutoStartAuthorization(health.value)) || session.value || acting.value) return
  autoStarted = true
  void start()
}

function clearSession() {
  session.value = null
  code.value = ''
  opened.value = false
}

/** Forgets everything about the current runtime (not the draft key itself). */
function resetTarget() {
  ++serial
  status.value = null
  health.value = null
  clearSession()
  failure.value = null
  autoStarted = false
  reauthCompleted.value = false
  sessionChecked = false
}

/** Forgets the new account's draft (not deleted here). */
function forgetDraft() {
  resetTarget()
  draftKey.value = null
  draftProxy.value = undefined
}

const discard = (key: string) => discardDraft(`${draftsBase.value}/${key}`)
/** Idempotency key of the preparation request (retries of one attempt reuse it). */
const randomKey = () => Array.from(crypto.getRandomValues(new Uint8Array(16)), (b) => b.toString(16).padStart(2, '0')).join('')

/** ② of a new account: creates the draft runtime with the picked proxy. */
async function createDraft() {
  if (!props.draft || draftKey.value || !canManage.value || acting.value || props.configurationReady === false) return
  const proxy = pickedProxy()
  resetTarget()
  busy.value = 'create'
  const stamp = serial
  let key = ''
  try {
    if (props.preparationUrl && !preparationAttempt) preparationAttempt = randomKey()
    const body = props.preparationUrl ? { proxy_id: proxy, settings: { ...props.settings }, idempotency_key: preparationAttempt } : { proxy_id: proxy }
    const r = await api.post<CcgDraft>(props.preparationUrl || DRAFTS, body, longCall())
    if (!isDraftKey(r?.key)) throw new Error(t('ccgateway.accountAuth.badDraft'))
    key = r.key
  } catch (e) {
    busy.value = ''
    if (alive && stamp === serial) await containerFailure('createFailed', e, stamp)
    return
  }
  busy.value = ''
  if (!alive || adopted) return discard(key)
  draftKey.value = key
  draftProxy.value = proxy
  status.value = { status: 'creating' }
  await refresh()
}

/** The editor's proxy changed after the draft exists: PUT drafts/:key (re-reconciles the container). */
async function updateProxy() {
  const key = draftKey.value
  const p = pickedProxy()
  if (!props.draft || !key || p === draftProxy.value || acting.value || !canManage.value) return
  busy.value = 'proxy'
  failure.value = null
  health.value = null
  const stamp = ++serial
  try {
    await api.put(`${draftsBase.value}/${key}`, { proxy_id: p }, longCall())
  } catch (e) {
    busy.value = ''
    if (alive && stamp === serial && key === draftKey.value) await containerFailure('proxyFailed', e, stamp)
    return
  }
  busy.value = ''
  if (!alive || key !== draftKey.value) return
  draftProxy.value = p
  status.value = { status: 'pending', container: status.value?.container }
  await refresh()
}

/** Deletes the draft and starts a new one (failed container, or the draft is gone); a re-authorization asks its parent. */
async function restartDraft() {
  if (acting.value) return
  if (reauth.value) return emit('restart', failure.value?.reason || '')
  if (!props.draft) return
  const key = draftKey.value
  forgetDraft()
  preparationAttempt = ''
  if (key) discard(key)
  await createDraft()
}

async function sync() {
  const base = target.value
  if (!base || !canManage.value || acting.value) return
  busy.value = 'sync'
  failure.value = null
  const stamp = ++serial
  lastCall = Date.now()
  let r: CcgSyncResult | null = null
  try {
    r = await api.post<CcgSyncResult>(`${base}/sync`, {}, longCall())
  } catch (e) {
    busy.value = ''
    if (stamp === serial) await containerFailure('syncFailed', e, stamp)
    return
  }
  busy.value = ''
  if (stamp !== serial) return
  // "blocked" is final until the account / draft changes; anything else: the status poll decides
  // (on several nodes another node may be the one doing the work).
  if (r?.status === 'blocked') {
    status.value = { status: 'blocked', container: status.value?.container, reason: r.reason }
    health.value = null
    return
  }
  await refresh()
}

async function start() {
  const base = target.value
  if (!base || !canManage.value || acting.value) return
  busy.value = 'start'
  failure.value = null
  lastCall = Date.now()
  try {
    const data = await api.post<CcgAuthSession>(`${base}/start`, {}, longCall())
    if (!usableSession(data)) throw new Error(t('ccgateway.accountAuth.badSession'))
    if (!alive || base !== target.value) {
      // The editor may close before /start returns its session ID.
      await api.post(`${base}/cancel`, { session_id: data.session_id }).catch(() => {})
      return
    }
    session.value = data
    opened.value = false
    code.value = ''
  } catch (e) {
    if (alive && base === target.value) fail('login', 'startFailed', e)
  } finally {
    busy.value = ''
  }
}

async function complete() {
  const base = target.value
  if (!base || !session.value || !canManage.value || acting.value || !code.value.trim()) return
  if (expired.value) return fail('code', 'expired')
  busy.value = 'complete'
  failure.value = null
  lastCall = Date.now()
  try {
    await api.post(`${base}/complete`, { session_id: session.value.session_id, code: code.value.trim() }, longCall())
  } catch (e) {
    busy.value = ''
    if (base === target.value) await completeRefused(base, e)
    return
  }
  const loggedIn = await waitForLogin(base)
  busy.value = ''
  if (!alive || base !== target.value) return
  if (!loggedIn) return fail('code', 'notConfirmed')
  reauthCompleted.value = true
  clearSession()
  // A re-authorization is announced once committed (by the parent).
  if (!reauth.value) toast(t('ccgateway.accountAuth.authorizedToast'), 'success')
  emit('authorized')
}

/** The code was refused: retry it, pick up another pending login, or (the login ended) ask for a new link. */
async function completeRefused(base: string, e: unknown) {
  const reason = reasonOf(e)
  // session_not_found also answers a stale session id while another login is pending: pick that one up.
  if (reason === 'session_not_found' && (await adoptPendingSession(base))) return fail('code', 'completeFailed', e)
  if (!sessionEnded(reason)) return fail('code', 'completeFailed', e)
  // The container ended (or never knew) this login: a new link is needed.
  clearSession()
  fail('login', 'completeFailed', e)
}

/** The container writes the credentials asynchronously: confirms the login (a few tries). */
async function waitForLogin(base: string): Promise<boolean> {
  for (let i = 0; i < 6 && alive; i++) {
    if (i) await new Promise((ok) => setTimeout(ok, POLL_MS))
    try {
      const h = await api.get<CcgRuntimeHealth>(`${base}/health`)
      if (base !== target.value) return false
      health.value = h
      if (h.logged_in) return true
    } catch {
      // tried again
    }
  }
  return false
}

async function cancel() {
  const base = target.value
  const s = session.value
  clearSession()
  failure.value = null
  if (!base || !s || sessionExpired(s.expires_at)) return
  busy.value = 'cancel'
  try {
    await api.post(`${base}/cancel`, { session_id: s.session_id })
  } catch {
    // the session expires by itself
  } finally {
    busy.value = ''
  }
}

/** Retry of the failed step. */
function retry() {
  const f = failure.value
  if (f?.step === 'login') return void start()
  if (f?.step === 'code') return void (expired.value || !session.value ? start() : complete())
  if (f?.step !== 'container') return
  if ((props.draft && !draftKey.value) || f.reason === 'draft_not_found') void restartDraft()
  else if (props.draft && pickedProxy() !== draftProxy.value) void updateProxy()
  else void (canManage.value ? sync() : refresh())
}

function tick() {
  if (!alive || document.hidden) return
  // A proxy picked while another action ran is applied once it is done.
  if (props.draft && draftKey.value && !acting.value && !failure.value && pickedProxy() !== draftProxy.value) return void updateProxy()
  if (!target.value || busy.value) return
  // Poll while the container converges (a known login state does not change by
  // itself, a blocked container waits for a change), and keep a draft alive.
  const converging = !session.value && failure.value?.step !== 'container' && phase.value !== 'blocked' && (phase.value !== 'ready' || !health.value)
  if (!converging && Date.now() - lastCall < HEARTBEAT_MS) return
  busy.value = 'status'
  void refresh().finally(() => {
    if (busy.value === 'status') busy.value = ''
  })
}

async function boot() {
  resetTarget()
  if (reauth.value) await refresh()
}

watch(() => props.reauthKey, () => void boot())
watch(() => props.proxyId, () => {
  // Creating the draft failed (e.g. the proxy was refused): another proxy starts over from the button.
  if (props.draft && !draftKey.value && failure.value?.step === 'container' && !acting.value) failure.value = null
  void updateProxy()
})
watch([key, authorizedDraft, () => health.value?.subscription_type], () => emit('state', {
  key: key.value, authorized: !!key.value && authorizedDraft.value,
  ...(health.value?.subscription_type ? { subscription_type: health.value.subscription_type } : {})
}), { immediate: true })

onMounted(() => {
  void boot()
  pollTimer = setInterval(tick, POLL_MS)
  clockTimer = setInterval(() => (now.value = Date.now()), 1000)
})
onBeforeUnmount(() => {
  alive = false
  ++serial
  clearInterval(pollTimer)
  clearInterval(clockTimer)
  if (inPlace.value) void cancel()
  else clearSession()
  // Editor closed, account type switched, back to the type picker: the entry is abandoned.
  // (A re-authorization draft belongs to the parent, which deletes or commits it.)
  if (props.draft && draftKey.value && !adopted) discard(draftKey.value)
})

defineExpose({
  cancelAuthorization: cancel,
  /** The account was saved with this draft: keep its runtime. */
  adopt() {
    adopted = true
  },
  /** POST /accounts (or the re-authorization commit) refused the draft (details.reason): start over or re-check the login. */
  rejected(reason: string) {
    if (!key.value) return
    if (reason !== 'draft_not_found') {
      health.value = null
      void refresh()
    } else if (props.draft) {
      forgetDraft()
      failure.value = { step: 'container', title: t('ccgateway.accountAuth.createFailed'), detail: t('ccgateway.reason.draft_not_found'), reason }
    }
  }
})
</script>

<template>
  <div class="space-y-3 text-sm" data-testid="ccgateway-account-auth" :data-mode="mode">
    <CcgAuthHeader :title="t(reauth ? 'ccgateway.reauth.flowTitle' : 'ccgateway.accountAuth.title')" :state="headerState" :badge-testid="step === 'done' ? 'ccgateway-auth-authorized' : undefined" />

    <CredentialStatusNotice :status="health" />
    <SHint v-if="!canManage" tone="warning">{{ t(reauth ? 'ccgateway.accountAuth.readOnly' : 'ccgateway.accountAuth.noCreate') }}</SHint>

    <SSteps :steps="stepItems" direction="vertical" data-testid="ccgateway-auth-steps">
      <!-- ① proxy (new account) -->
      <template #step-proxy>
        <div v-if="states.proxy === 'current'" class="mt-1 space-y-2">
          <SHint size="xs" data-testid="ccgateway-proxy-hint">{{ t('ccgateway.accountAuth.proxyHint') }}</SHint>
          <SButton size="sm" variant="primary" icon="proxy" data-testid="ccgateway-auth-pick-proxy" @click="emit('fix-proxy')">{{ t('ccgateway.accountAuth.blocked.pickProxy') }}</SButton>
        </div>
        <p v-else class="mt-0.5 flex items-center gap-2 text-xs text-fg-muted">
          <span>{{ t('ccgateway.accountAuth.proxyChosen', { name: proxyName }) }}</span>
          <BusyText v-if="busy === 'proxy'">{{ t('ccgateway.accountAuth.proxyUpdating') }}</BusyText>
        </p>
      </template>

      <!-- ② container -->
      <template #step-container>
        <template v-if="target || draft">
          <CcgErrorAlert v-if="failure?.step === 'container'" class="mt-1.5" :title="failure.title" :detail="failure.detail" data-testid="ccgateway-auth-container-error" :data-reason="failure.reason">
            <template v-if="failure.setup?.length" #default>
              <ul class="list-inside list-disc space-y-0.5">
                <li v-for="p in failure.setup" :key="p">{{ t(`ccgateway.accountAuth.setup.${p}`) }}</li>
              </ul>
            </template>
            <template #actions>
              <SButton size="sm" icon="refresh" :loading="busy === 'sync' || busy === 'create' || busy === 'proxy'" :disabled="acting || !canManage" data-testid="ccgateway-auth-retry" @click="retry">{{ t('ccgateway.accountAuth.retry') }}</SButton>
              <SButton v-if="isProxyReason(failure.reason)" size="sm" variant="ghost" icon="proxy" @click="emit('fix-proxy')">{{ t('ccgateway.accountAuth.blocked.pickProxy') }}</SButton>
              <SButton v-if="(draft && draftKey) || reauth" size="sm" variant="ghost" :disabled="acting" data-testid="ccgateway-auth-restart" @click="restartDraft">{{ t('ccgateway.accountAuth.restart') }}</SButton>
              <SButton v-if="failure.setup?.length || failure.reason === 'not_configured'" size="sm" variant="ghost" icon="settings" :to="settingsLink" data-testid="ccgateway-auth-open-settings">{{ t('ccgateway.accountAuth.openSettings') }}</SButton>
            </template>
          </CcgErrorAlert>
          <!-- new account, no draft yet -->
          <template v-else-if="draft && !draftKey">
            <BusyText v-if="busy === 'create'" class="mt-1">{{ t('ccgateway.accountAuth.creating') }}</BusyText>
            <div v-else-if="states.container === 'current'" class="mt-1 space-y-2">
              <SHint size="xs">{{ t('ccgateway.accountAuth.startHint') }}</SHint>
              <SButton variant="primary" icon="play" :disabled="acting || !canManage || configurationReady === false" data-testid="ccgateway-auth-create" @click="createDraft">{{ t('ccgateway.accountAuth.startContainer') }}</SButton>
            </div>
          </template>
          <CcgBlockedAlert v-else-if="phase === 'blocked'" class="mt-1.5" :reason="status?.reason" :can-manage="canManage" :syncing="busy === 'sync'" :disabled="acting" @sync="sync" />
          <SAlert v-else-if="states.container === 'error'" tone="danger" dense class="mt-1.5">
            {{ t('ccgateway.accountAuth.containerError', { status: statusLabel(status?.status) }) }}
            <template #actions>
              <SButton v-if="canManage" size="sm" icon="refresh" :loading="busy === 'sync'" @click="sync">{{ t('ccgateway.accountAuth.resync') }}</SButton>
              <SButton size="sm" variant="ghost" :disabled="acting" @click="restartDraft">{{ t('ccgateway.accountAuth.restart') }}</SButton>
            </template>
          </SAlert>
          <div v-else-if="states.container === 'current'" class="mt-1 flex flex-wrap items-center gap-2" data-testid="ccgateway-auth-preparing">
            <BusyText>{{ busy === 'sync' ? t('ccgateway.accountAuth.containerStarting') : t('ccgateway.accountAuth.containerPreparing', { name: containerName, status: statusLabel(status?.status) }) }}</BusyText>
            <SButton v-if="canManage && busy !== 'sync' && busy !== 'proxy'" size="sm" variant="ghost" @click="sync">{{ t('ccgateway.accountAuth.resync') }}</SButton>
          </div>
          <p v-else-if="states.container === 'done'" class="mt-0.5 font-mono text-xs text-fg-muted">{{ t('ccgateway.accountAuth.containerReady', { name: containerName }) }}</p>
        </template>
      </template>

      <!-- ③ open the link -->
      <template #step-login>
        <template v-if="showLogin">
          <CcgErrorAlert v-if="failure?.step === 'login'" class="mt-1.5" :title="failure.title" :detail="failure.detail" data-testid="ccgateway-auth-login-error" :data-reason="failure.reason">
            <template #actions>
              <SButton size="sm" icon="refresh" :loading="busy === 'start'" data-testid="ccgateway-auth-retry" @click="retry">{{ t('ccgateway.accountAuth.regetLink') }}</SButton>
            </template>
          </CcgErrorAlert>
          <!-- Copy is the main way: the user signs in to Claude in their own
               browser (often another machine or profile); opening here is a fallback. -->
          <div v-else-if="session" class="mt-2 space-y-2">
            <div class="flex items-stretch gap-2" :class="{ 'pointer-events-none opacity-50': expired }">
              <SInput
                :model-value="session.url"
                readonly
                mono
                class="min-w-0 flex-1 text-xs"
                :aria-label="t('ccgateway.accountAuth.linkLabel')"
                data-testid="ccgateway-auth-link"
                @focus="($event.target as HTMLInputElement).select()"
              />
              <SCopyButton :text="session.url" :label="t('ccgateway.accountAuth.copyLink')" variant="secondary" size="md" data-testid="ccgateway-auth-copy" @copied="opened = true" />
            </div>
            <SHint size="xs">
              {{ t('ccgateway.accountAuth.linkHint') }}
              <span v-if="!expired" class="ml-1 tabular-nums">{{ t('ccgateway.accountAuth.expiresIn', { time: leftText }) }}</span>
            </SHint>
            <SLink :href="session.url" external class="inline-flex items-center gap-1 text-xs" :class="{ 'pointer-events-none opacity-50': expired }" data-testid="ccgateway-auth-open" @click="opened = true">
              <SIcon name="external" class="h-3.5 w-3.5" />{{ t('ccgateway.accountAuth.openLink') }}
            </SLink>
          </div>
          <BusyText v-else-if="busy === 'start'" class="mt-1">{{ t('ccgateway.accountAuth.gettingLink') }}</BusyText>
          <SButton v-else-if="canManage" class="mt-2" variant="primary" icon="link" :disabled="acting" data-testid="ccgateway-auth-start" @click="start">{{ t('ccgateway.accountAuth.getLink') }}</SButton>
        </template>
      </template>

      <!-- ④ paste the code (not a <form>: the panel sits inside the account editor's form) -->
      <template #step-code>
        <div v-if="target && session" class="mt-2 space-y-2">
          <SField :label="t('ccgateway.accountAuth.codeLabel')" size="sm">
            <div class="flex flex-wrap gap-2">
              <SInput
                v-model="code"
                type="password"
                class="min-w-[14rem] flex-1"
                mono
                autocomplete="off"
                spellcheck="false"
                :disabled="busy === 'complete' || expired"
                :placeholder="t('ccgateway.accountAuth.codePlaceholder')"
                data-testid="ccgateway-auth-code"
                @focus="opened = true"
                @keydown.enter.prevent="complete"
              />
              <SButton variant="primary" :loading="busy === 'complete'" :disabled="!code.trim() || expired || (acting && busy !== 'complete')" data-testid="ccgateway-auth-complete" @click="complete">{{ t('ccgateway.accountAuth.submit') }}</SButton>
            </div>
          </SField>
          <SHint v-if="codeShapeWarn" tone="warning" size="xs">{{ t('ccgateway.accountAuth.codeShape') }}</SHint>
          <CcgErrorAlert v-if="failure?.step === 'code'" class="mt-1.5" :title="failure.title" :detail="failure.detail" data-testid="ccgateway-auth-code-error" :data-reason="failure.reason">
            <template #actions>
              <SButton v-if="code.trim() && !expired" size="sm" icon="refresh" :loading="busy === 'complete'" @click="retry">{{ t('ccgateway.accountAuth.retry') }}</SButton>
              <SButton size="sm" variant="ghost" :loading="busy === 'start'" @click="start">{{ t('ccgateway.accountAuth.regetLink') }}</SButton>
            </template>
          </CcgErrorAlert>
          <div v-else-if="expired" class="flex flex-wrap items-center gap-2">
            <SHint tone="warning" size="xs" inline>{{ t('ccgateway.accountAuth.expired') }}</SHint>
            <SButton size="sm" :loading="busy === 'start'" @click="start">{{ t('ccgateway.accountAuth.regetLink') }}</SButton>
          </div>
          <SButton size="sm" variant="ghost" :disabled="busy === 'complete'" @click="cancel">{{ t('ccgateway.accountAuth.cancel') }}</SButton>
        </div>
      </template>

      <template #step-done>
        <SHint v-if="states.done === 'done'" tone="success" size="xs" class="mt-0.5" data-testid="ccgateway-auth-done-hint">{{ t(reauth ? 'ccgateway.reauth.signedIn' : 'ccgateway.accountAuth.authorizedDraftHint') }}</SHint>
      </template>
    </SSteps>
  </div>
</template>
