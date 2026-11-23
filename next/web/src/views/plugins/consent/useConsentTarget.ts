// The consent page's target: loading its review, approving it and putting
// the approved version into service (enable or upgrade, then the rollout
// page), rejecting a registered version, cancelling an install. The page
// itself only works out what the consent grants (ConsentView).
import { computed, onBeforeUnmount, ref } from 'vue'
import { useRoute, useRouter, type RouteLocationRaw } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { api, isApiError } from '@sub2api/host'
import { confirm, toast } from '@sub2api/ui'
import type { PluginReview } from '@/api/types'
import type { ConsentRequest, ConsentResult } from '@/api/pluginConsent'
import { lt } from '@/i18n'
import { errorMessage, notifyError } from '@/utils/errors'
import { useAuthStore } from '@/stores/auth'
import { rolloutAfterConsent, type PluginDetail } from '../pluginUtil'
import { UploadMissingError, consentSourceOf } from './consentSources'
import { consentedLocation, pluginPath, rolloutPath } from './paths'
import { clearPendingUpload } from './pendingUpload'

/** Why there is no review: the version's package is on no reachable node, the uploaded file is gone, or another error. */
export type ReviewFailure = { kind: 'package_gone' | 'upload_missing' | 'error'; message: string }

function failureOf(e: unknown): ReviewFailure {
  if (e instanceof UploadMissingError) return { kind: 'upload_missing', message: '' }
  return { kind: isApiError(e) && e.code === 'package_gone' ? 'package_gone' : 'error', message: errorMessage(e) }
}

function createTarget() {
  return {
    t: useI18n().t,
    router: useRouter(),
    auth: useAuthStore(),
    source: consentSourceOf(useRoute()),
    review: ref<PluginReview | null>(null),
    loading: ref(true),
    failure: ref<ReviewFailure | null>(null),
    busy: ref<'' | 'approve' | 'reject'>(''),
    /** Approved or rejected: the page goes away without asking about the matrix. */
    settled: ref(false)
  }
}
type Target = ReturnType<typeof createTarget>

const nameOf = (r: PluginReview) => lt(r.name) || r.plugin_key

async function load(c: Target) {
  c.loading.value = true
  c.failure.value = null
  try {
    c.review.value = await c.source.review()
  } catch (e) {
    c.review.value = null
    c.failure.value = failureOf(e)
  } finally {
    c.loading.value = false
  }
}

/** Leaves a page whose consent was sent (or whose version was rejected): nothing is left to ask about. */
function leave(c: Target, to: RouteLocationRaw) {
  c.settled.value = true
  return c.router.push(to)
}

/** Enables or upgrades the approved version; when that cannot start, the plugin's page offers it by hand. */
async function launch(c: Target, key: string, version: string) {
  try {
    const action = c.auth.has('plugin:manage') ? rolloutAfterConsent(await api.get<PluginDetail>(pluginPath(key)), version) : null
    if (!action) return leave(c, consentedLocation(key, version))
    await api.post(`${pluginPath(key)}/${action}`, action === 'upgrade' ? { version } : undefined)
    return leave(c, rolloutPath(key))
  } catch (e) {
    notifyError(e)
    return leave(c, consentedLocation(key, version))
  }
}

function announce(c: Target, r: PluginReview, res: ConsentResult | null | undefined) {
  toast(c.t('plugins.consent.approved', { name: nameOf(r), version: r.version }), 'success')
  if (res?.withheld?.length) toast(c.t('plugins.matrix.withheld', { n: res.withheld.length }), 'info')
}

async function approveFailed(c: Target, e: unknown) {
  if (isApiError(e) && e.code === 'package_changed') {
    toast(c.t('pluginInstall.packageChanged'), 'error')
    return load(c)
  }
  // The matrix locks critical rows for a non-super caller; a refusal still says why (D3).
  if (isApiError(e) && e.details?.reason === 'critical') toast(c.t('plugins.matrix.criticalOnly'), 'error')
  else notifyError(e)
}

async function approve(c: Target, consent: ConsentRequest) {
  const r = c.review.value
  if (!r || c.busy.value) return
  c.busy.value = 'approve'
  let res: ConsentResult | null
  try {
    // The same package approved before takes no consent again (the server refuses it): only its rollout starts.
    res = r.consent_status === 'approved' ? null : await c.source.approve(r, consent)
  } catch (e) {
    c.busy.value = ''
    return approveFailed(c, e)
  }
  // Sent: leaving while the rollout starts no longer asks about the matrix.
  c.settled.value = true
  if (res) announce(c, r, res)
  await launch(c, r.plugin_key, r.version)
  c.busy.value = ''
}

async function reject(c: Target) {
  const r = c.review.value
  if (!r || !c.source.reject) return
  const message = c.t('plugins.consent.rejectConfirm', { name: nameOf(r), version: r.version })
  if (!(await confirm({ title: c.t('plugins.consent.rejectTitle'), message, danger: true, confirmText: c.t('plugins.consent.reject') }))) return
  c.busy.value = 'reject'
  try {
    await c.source.reject()
    toast(c.t('plugins.consent.rejected'), 'success')
    await leave(c, c.source.back)
  } catch (e) {
    notifyError(e)
  } finally {
    c.busy.value = ''
  }
}

export function useConsentTarget() {
  const c = createTarget()
  onBeforeUnmount(clearPendingUpload)
  return {
    source: c.source,
    review: c.review,
    loading: c.loading,
    failure: c.failure,
    busy: c.busy,
    settled: c.settled,
    isUpgrade: computed(() => !!(c.review.value?.upgrade_from || c.review.value?.diff)),
    load: () => load(c),
    approve: (consent: ConsentRequest) => approve(c, consent),
    reject: () => reject(c),
    /** Cancelling an install calls nothing: it was never registered (unsaved matrix edits are asked about on the way out). */
    cancel: () => c.router.push(c.source.back)
  }
}
