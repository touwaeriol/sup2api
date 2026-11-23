// Mock handlers: a plugin version's review and consent, and installs (upload,
// install-from-market) with their read-only pre-install reviews; an install
// carrying a consent registers and approves at once (CONTRACTS §5.7
// "先授权再安装"). The plugins themselves live in plugins.ts.
import { createHash } from 'node:crypto'
import { fail, on, type MockRequest } from './router'
import { caller, stepUp, type Identity } from './core'
import { bumpAuthz } from './roles'
import { invalid } from './common'
import { diffItem, find, forViewer, grant, guardReview, notFound, plugins, reviewFor, reviewQueue, reviews, settleRoleGrants, stageVersion, type Any } from './plugins'

/** The sha256 of a version's package (made up, the same for every download). */
const packageSha = (r: Any) => createHash('sha256').update(`${r.plugin_key}@${r.version}`).digest('hex')

/** A review as served: the package it describes and, for an upgrade, the version installed now. */
function served(r: Any, who: Identity): Any {
  const active = plugins.get(r.plugin_key)?.active_version
  const from = active && active !== r.version ? { upgrade_from: active } : {}
  return forViewer({ ...r, package_sha256: packageSha(r), ...from }, who)
}

/** A review of a package nothing was registered for. */
const preInstallReview = (r: Any, who: Identity) => ({ ...served(r, who), consent_status: '' })

/** Why a consent cannot install r: no package_sha256, another package, or the version already approved. */
function installRefusal(r: Any, consent: Any): Any | null {
  if (!consent.package_sha256) return invalid('consent.package_sha256', 'package_sha256 is required', 'required')
  if (consent.package_sha256 !== packageSha(r)) return fail(409, 'package_changed', 'the package differs from the one reviewed; review it again', { package_sha256: packageSha(r) })
  const v = plugins.get(r.plugin_key)?.versions.find((x) => x.version === r.version)
  return v?.consent_status === 'approved' ? fail(409, 'conflict', `version ${r.version} is approved`) : null
}

/** Registers a version and approves it with the consent; a refused consent leaves nothing registered. */
function installConsented(r: Any, consent: Any, who: Identity): Any {
  const refused = installRefusal(r, consent)
  if (refused) return refused
  const before = plugins.get(r.plugin_key)
  const hadVersion = !!before?.versions.some((v) => v.version === r.version)
  stageVersion(r)
  const res = consentTo(r.plugin_key, r.version, consent, who)
  if (res?.__status) {
    if (!before) plugins.delete(r.plugin_key)
    else if (!hadVersion) before.versions = before.versions.filter((v) => v.version !== r.version)
    return res
  }
  return { review: { ...served(r, who), consent_status: 'approved' }, consent: res }
}

/** Records a consent on a registered version: the grants chosen, the role grants checked against the viewer. */
function consentTo(key: string, version: string, body: Any, who: Identity): Any {
  const p = find(key)
  if (!p) return notFound(key)
  const grants: Array<{ permission: string; scope?: Any }> = body?.grants || []
  const v = p.versions.find((x) => x.version === version)
  if (!v) return fail(404, 'not_found', 'version not found')
  const settled = settleRoleGrants(reviews.get(`${p.key}@${v.version}`) ?? {}, who, body?.new_permission_roles)
  if ('error' in settled) return settled.error
  v.consent_status = 'approved'
  p.grants = grants.map((g) => grant(g.permission, g.scope ?? null, version))
  // What the consent held back waits on the plugin for a super admin (role_grants_to_review).
  const queued = (reviewQueue.get(p.key) ?? []).filter((g) => g.version !== v.version)
  const held = settled.withheld.map((g: Any) => ({ ...g, version: v.version }))
  if (queued.length || held.length) reviewQueue.set(p.key, [...queued, ...held])
  else reviewQueue.delete(p.key)
  bumpAuthz()
  if (p.status === 'awaiting_consent') p.status = 'installed'
  if (!p.active_version) p.desired_version = version
  return { ok: true, withheld: settled.withheld }
}

on('GET', '/plugins/:key/versions/:version/review', (req) => {
  const r = reviews.get(`${req.params.key}@${req.params.version}`)
  return r ? served(r, caller(req)) : fail(404, 'not_found', 'no review for this version')
})

on('POST', '/plugins/:key/versions/:version/consent', stepUp('plugin.grant', (req) => consentTo(req.params.key, req.params.version, req.body, caller(req))))

// The package is not read: an uploaded file is always guard 0.1.1.
const uploadedReview = () => guardReview('0.1.1', { added: [], widened: [diffItem('gateway.hook', 'high')], removed: [] })
const marketReview = (req: MockRequest) => {
  const { key, version } = req.body || {}
  return key && version ? reviewFor(key, version) : null
}
const needKeyVersion = () => fail(400, 'invalid_argument', 'key and version are required')

// Reviews before anything is registered (no rows, no audit).
on('POST', '/plugins/review-upload', (req) => preInstallReview(uploadedReview(), caller(req)))
on('POST', '/plugins/review-from-market', (req) => {
  const r = marketReview(req)
  return r ? preInstallReview(r, caller(req)) : needKeyVersion()
})

/** upload's consent field: a JSON string (undefined without one, null when it is not JSON). */
function uploadConsent(req: MockRequest): Any | null | undefined {
  if (!req.body?.consent) return undefined
  try {
    return JSON.parse(req.body.consent)
  } catch {
    return null
  }
}

// With a consent an install registers and approves at once (the consent's step-up); without, it only registers.
const install = (review: (req: MockRequest) => Any | null, consentOf: (req: MockRequest) => Any | null | undefined) => (req: MockRequest) => {
  const r = review(req)
  if (!r) return needKeyVersion()
  const consent = consentOf(req)
  if (consent === null) return invalid('consent', 'consent is not JSON')
  if (!consent) return stepUp('plugin.install', () => served(stageVersion(r), caller(req)))(req)
  return stepUp('plugin.grant', () => installConsented(r, consent, caller(req)))(req)
}
on('POST', '/plugins/upload', install(uploadedReview, uploadConsent))
on('POST', '/plugins/install-from-market', install(marketReview, (req) => req.body?.consent ?? undefined))
