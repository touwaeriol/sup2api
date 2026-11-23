// Where a consent page's review comes from and where its approval goes: a
// version registered and awaiting consent, or an install not registered yet
// (a market version, an uploaded file). An install is reviewed read-only and
// registered only with its consent, so cancelling it leaves nothing behind.
import type { RouteLocationNormalizedLoaded } from 'vue-router'
import { api } from '@sub2api/host'
import type { ConsentRequest, ConsentResult, ConsentedInstall, InstallConsent, PluginReview } from '@/api/types'
import { MARKET_INSTALL_PATH, UPLOAD_INSTALL_PATH } from './paths'
import { pendingUpload } from './pendingUpload'

export type ConsentSourceKind = 'registered' | 'market' | 'upload'

export interface ConsentSource {
  kind: ConsentSourceKind
  /** The version asked for, before its review says it ('' for a file). */
  version: string
  /** The page cancelling (an install) or rejecting (a registered version) returns to. */
  back: string
  review(): Promise<PluginReview>
  approve(review: PluginReview, consent: ConsentRequest): Promise<ConsentResult>
  /** Discards a registered version; an install has nothing to discard. */
  reject?: () => Promise<unknown>
}

/** The uploaded file is gone (the page was reloaded): there is nothing to review. */
export class UploadMissingError extends Error {}

const enc = encodeURIComponent

/** The consent an install carries: the reviewed package, so a changed one is refused (409 package_changed). */
const forPackage = (r: PluginReview, consent: ConsentRequest): InstallConsent => ({ ...consent, package_sha256: r.package_sha256 ?? '' })

function registeredSource(key: string, version: string): ConsentSource {
  const base = `/plugins/${enc(key)}/versions/${enc(version)}`
  return {
    kind: 'registered',
    version,
    back: '/plugins',
    review: () => api.get<PluginReview>(`${base}/review`),
    approve: (_r, consent) => api.post<ConsentResult>(`${base}/consent`, consent),
    reject: () => api.post(`${base}/reject`)
  }
}

function marketSource(query: RouteLocationNormalizedLoaded['query']): ConsentSource {
  const text = (name: string) => String(query[name] ?? '')
  const target = { source_id: Number(text('source_id')), key: text('key'), version: text('version') }
  const install = (consent: InstallConsent) => api.post<ConsentedInstall>('/plugins/install-from-market', { ...target, consent })
  return {
    kind: 'market',
    version: target.version,
    back: '/market',
    review: () => api.post<PluginReview>('/plugins/review-from-market', target),
    approve: async (r, consent) => (await install(forPackage(r, consent))).consent
  }
}

/** The chosen file as the multipart body, with the consent when it installs. */
function packageForm(consent?: InstallConsent): FormData {
  const file = pendingUpload()
  if (!file) throw new UploadMissingError()
  const fd = new FormData()
  fd.append('file', file)
  if (consent) fd.append('consent', JSON.stringify(consent))
  return fd
}

function uploadSource(): ConsentSource {
  return {
    kind: 'upload',
    version: '',
    back: '/plugins',
    review: async () => api.upload<PluginReview>('/plugins/review-upload', packageForm()),
    approve: async (r, consent) => (await api.upload<ConsentedInstall>('/plugins/upload', packageForm(forPackage(r, consent)))).consent
  }
}

export function consentSourceOf(route: Pick<RouteLocationNormalizedLoaded, 'path' | 'params' | 'query'>): ConsentSource {
  if (route.path === MARKET_INSTALL_PATH) return marketSource(route.query)
  if (route.path === UPLOAD_INSTALL_PATH) return uploadSource()
  return registeredSource(String(route.params.key), String(route.params.version))
}
