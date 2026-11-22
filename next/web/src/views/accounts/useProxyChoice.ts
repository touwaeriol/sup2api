// The account editor's proxy (CONTRACTS §21.4): pick an existing one, or
// paste a URL. "url" sends proxy_url instead of proxy_id; the server parses
// it, reuses a matching proxy the caller can see or creates one, and answers
// proxy_created. A pasted one can also be tested and saved first (probe).
// AccountProxyField renders it; the save, the model fetch and the native
// form's proxy tickets read the choice from here.
import { computed, ref, watch, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '@sub2api/host'
import type { AccountType, ProxyProbeResult } from '@/api/types'
import { useProxiesLookup } from '@/composables/lookups'
import { fieldErrors, notifyError } from '@/utils/errors'
import { looksLikeProxyURL, parseProxyURL } from '@/utils/proxyUrl'

export type ProxyMode = 'existing' | 'url'
type T = ReturnType<typeof useI18n>['t']

export interface ProxyChoiceDeps {
  /** The form's proxy_id. */
  basic: { proxy_id: number | null }
  /** The editor's field errors: proxy_id / proxy_url land there. */
  errors: Ref<Record<string, string>>
  /** Pasting a URL may create a proxy: only with a proxy management key. */
  canPasteUrl: Ref<boolean>
  accountType: () => AccountType | null
}

/** Shape hint only (the server validates): '' once the text looks like scheme://host:port. */
function urlShapeHint(t: T, text: string): string {
  const raw = text.trim()
  if (!raw) return ''
  const r = parseProxyURL(raw)
  if (r.ok) return ''
  // host:port:user:pass and user:pass@host:port have no scheme: the test detects it.
  if (r.error === 'scheme') return looksLikeProxyURL(raw) ? t('accounts.proxyUrlSchemeHint') : t('accounts.proxyUrlProbeHint')
  if (r.error === 'port') return t('accounts.proxyUrlPortHint')
  if (r.error === 'extra') return t('accounts.proxyUrlExtraHint')
  return t('accounts.proxyUrlShapeHint')
}

/** Sets and clears single field errors of the editor. */
function fieldErrorEdit(errors: Ref<Record<string, string>>) {
  return {
    set: (field: string, message: string) => (errors.value = { ...errors.value, [field]: message }),
    clear(...keys: string[]) {
      if (keys.some((k) => errors.value[k])) errors.value = Object.fromEntries(Object.entries(errors.value).filter(([k]) => !keys.includes(k)))
    }
  }
}

/** Picking or pasting, and the pasted text; switching clears the proxy errors. */
function useProxyMode(canPasteUrl: Ref<boolean>, clearErrors: (...keys: string[]) => void) {
  const mode = ref<ProxyMode>('existing')
  const url = ref('')
  function setMode(m: string) {
    mode.value = m as ProxyMode
    clearErrors('proxy_url', 'proxy_id')
  }
  // The paste option withdrawn (permissions reloaded): back to picking one.
  watch(canPasteUrl, (ok) => {
    if (!ok && mode.value === 'url') setMode('existing')
  })
  /** Editing starts at "pick existing" with the current proxy selected. */
  function reset() {
    mode.value = 'existing'
    url.value = ''
  }
  return { mode, url, setMode, reset }
}

export function useProxyChoice(deps: ProxyChoiceDeps) {
  const { t } = useI18n()
  const { basic, errors } = deps
  const edit = fieldErrorEdit(errors)
  const { mode, url, setMode, reset } = useProxyMode(deps.canPasteUrl, edit.clear)
  /** The proxy_url that will be sent, or '' (existing mode / empty input). */
  const urlToSend = computed(() => (mode.value === 'url' ? url.value.trim() : ''))
  // The choice as the core takes it for an account not saved yet (models/fetch, proxy-ticket): a pasted
  // proxy_url is only parsed and used, nothing is looked up or created (CONTRACTS §21.2); proxy_id null is none.
  const choice = () => (urlToSend.value ? { proxy_url: urlToSend.value } : { proxy_id: basic.proxy_id })
  return {
    mode, url, urlToSend, setMode, reset, choice,
    canPasteUrl: deps.canPasteUrl,
    error: computed(() => (mode.value === 'url' ? errors.value.proxy_url : errors.value.proxy_id)),
    urlHint: computed(() => urlShapeHint(t, url.value)),
    ticket: proxyTicket(deps.accountType, choice, edit.set),
    ...useProxyProbe({ t, url, mode, basic, setError: edit.set, clearErrors: edit.clear })
  }
}

/**
 * The native form's `proxyTicket` (CONTRACTS §23.5, §117): a proxy ticket of
 * the current choice, taken by an authorization flow for every call it makes
 * to its plugin before the account exists. A refused choice is shown under
 * the proxy field too, and the error goes back to the form.
 */
function proxyTicket(accountType: () => AccountType | null, choice: () => Record<string, unknown>, setError: (field: string, message: string) => void) {
  return async function ticket(): Promise<string> {
    const at = accountType()!
    try {
      const r = await api.post<{ ticket: string }>(`/account-types/${encodeURIComponent(at.plugin_key)}/${encodeURIComponent(at.type)}/proxy-ticket`, choice())
      return r.ticket
    } catch (e) {
      const fe = fieldErrors(e)
      const field = fe.proxy_url ? 'proxy_url' : fe.proxy_id ? 'proxy_id' : ''
      if (field) setError(field, fe[field])
      throw e
    }
  }
}

export type ProxyChoice = ReturnType<typeof useProxyChoice>

interface ProbeDeps {
  t: T
  url: Ref<string>
  mode: Ref<ProxyMode>
  basic: { proxy_id: number | null }
  setError: (field: string, message: string) => void
  clearErrors: (...keys: string[]) => void
}

/**
 * POST /proxies/probe {save}: tries the pasted proxy (socks5, http, then https
 * without a scheme), finds or creates the proxy that works and selects it.
 */
function useProxyProbe(deps: ProbeDeps) {
  const { url, mode, basic, clearErrors } = deps
  /** Last successful test of a pasted proxy, shown under the proxy field while it is selected. */
  const probed = ref<Probed | null>(null)
  const probing = ref(false)
  const showProbed = computed(() => !!probed.value && mode.value === 'existing' && basic.proxy_id === probed.value.id)

  async function probe() {
    const raw = url.value.trim()
    if (!raw || probing.value) return
    clearErrors('proxy_url')
    probed.value = null
    probing.value = true
    try {
      probed.value = await selectProbed(deps, await api.post<ProxyProbeResult>('/proxies/probe', { proxy_url: raw, save: true }))
    } catch (e) {
      const fe = fieldErrors(e)
      if (fe.proxy_url) deps.setError('proxy_url', fe.proxy_url)
      else notifyError(e)
    } finally {
      probing.value = false
    }
  }
  return { probed, probing, showProbed, probe }
}

interface Probed {
  protocol: string
  ip: string
  id: number
  created: boolean
}

/** Selects the proxy a probe found (or created); null with the attempts as the error when none worked. */
async function selectProbed({ t, url, mode, basic, setError }: ProbeDeps, r: ProxyProbeResult): Promise<Probed | null> {
  if (!r.ok || !r.proxy_id) {
    const tried = r.attempts.map((a) => `${a.protocol}: ${a.message || a.status || t('proxies.failed')}`).join('; ')
    setError('proxy_url', t('proxies.probeFailed', { error: tried }))
    return null
  }
  await useProxiesLookup(true).ready
  url.value = ''
  mode.value = 'existing'
  basic.proxy_id = r.proxy_id
  return { protocol: (r.protocol || '').toUpperCase(), ip: r.ip || '', id: r.proxy_id, created: r.proxy_created }
}
