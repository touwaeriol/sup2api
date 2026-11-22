// Mock API keys of the signed-in user (/me/api-keys): their limits, live
// use, how their group serves them, and the secrets issued this session.
import { fail, nextId, noContent, now, on, paginate } from './router'
import { caller } from './core'
import { groupPlatforms } from './accounts'
import { accessOf, segmentOfUser } from './pricingStore'
import { invalid } from './common'
import { groups, myGroups } from './groups'
import { users } from './users'

interface MockKey {
  allowed_ips?: string[]
  quota_limit?: string
  quota_used?: string
  rpm_limit?: number
  tpm_limit?: number
  max_concurrency?: number
  id: number
  user_id: number
  group_id: number | null
  name: string
  key_prefix: string
  status: string
  expires_at: string | null
  last_used_at: string | null
  created_at: string
}

export const ME_ID = 1
export const keys: MockKey[] = [
  { id: 1, user_id: 1, group_id: 1, name: 'laptop', key_prefix: 'sk-s2a-a1b2c', status: 'active', expires_at: null, last_used_at: now(-42), created_at: now(-86400 * 30) },
  { id: 2, user_id: 1, group_id: 2, rpm_limit: 60, tpm_limit: 200000, max_concurrency: 2, quota_limit: '20', quota_used: '17.3', name: 'ci-runner', key_prefix: 'sk-s2a-c3d4e', status: 'active', expires_at: now(86400 * 60), last_used_at: now(-3600 * 3), created_at: now(-86400 * 12) },
  { id: 3, user_id: 1, group_id: 1, name: 'old-script', key_prefix: 'sk-s2a-e5f6g', status: 'active', expires_at: now(-86400), last_used_at: now(-86400 * 3), created_at: now(-86400 * 50) },
  { id: 4, user_id: 2, group_id: 1, name: 'ops-tools', key_prefix: 'sk-s2a-g7h8i', status: 'active', expires_at: null, last_used_at: now(-300), created_at: now(-86400 * 8) },
  { id: 5, user_id: 4, group_id: 2, name: 'acme-prod', key_prefix: 'sk-s2a-i9j0k', status: 'disabled', expires_at: null, last_used_at: now(-86400 * 4), created_at: now(-86400 * 9) },
  // Its group was deleted: the key is kept, unbound.
  { id: 6, user_id: 1, group_id: null, name: 'orphan', key_prefix: 'sk-s2a-k1l2m', status: 'active', expires_at: null, last_used_at: now(-86400 * 2), created_at: now(-86400 * 20) },
  // Its group has no accounts yet: it serves no platform.
  { id: 7, user_id: 1, group_id: 4, name: 'split-test-relay', key_prefix: 'sk-s2a-A9p0q', status: 'active', expires_at: null, last_used_at: null, created_at: now(-86400 * 2) },
  { id: 8, user_id: 1, group_id: 2, quota_limit: '50', quota_used: '50', allowed_ips: ['10.0.0.0/8', '203.0.113.7'], name: 'office-gateway', key_prefix: 'sk-s2a-m3n4o', status: 'active', expires_at: now(86400 * 7), last_used_at: now(-600), created_at: now(-86400 * 15) },
  { id: 9, user_id: 1, group_id: 1, name: 'paused-bot', key_prefix: 'sk-s2a-p5q6r', status: 'disabled', expires_at: null, last_used_at: now(-86400 * 6), created_at: now(-86400 * 40) }
]

const LIMIT_FIELDS = ['rpm_limit', 'tpm_limit', 'max_concurrency'] as const

/** Mock live use: a steady share of each cap (ci-runner close to its RPM). */
function liveOf(k: MockKey) {
  return { rpm: k.rpm_limit ? Math.round(k.rpm_limit * 0.85) : k.id * 3, tpm: k.tpm_limit ? Math.round(k.tpm_limit * 0.4) : k.id * 1200, concurrency: k.max_concurrency ? 1 : 0 }
}

/** How the key's group serves it, as the server reports group_status. */
function keyGroupStatus(k: MockKey) {
  const g = groups.find((x) => x.id === k.group_id)
  if (!g) return 'deleted'
  if (g.status !== 'active') return 'disabled'
  return accessOf(segmentOfUser(k.user_id), g).accessible ? 'active' : 'unavailable'
}

/** How much of a key the server keeps readable (key_prefix). */
const KEY_PREFIX_LENGTH = 12
/** Keys kept only as a hash (old-script stands for one): they cannot be revealed until regenerated. */
const legacyKeyIds = new Set([3])
/** The plaintext of the keys issued in this session; a seeded key's is made up from its prefix. */
const issuedKeys = new Map<number, string>()
const plaintextOf = (k: MockKey) => issuedKeys.get(k.id) ?? k.key_prefix + 'X'.repeat(47 - k.key_prefix.length)

/** Gives the key a new secret (on create and on rotate) and returns its plaintext. */
function issueKey(k: MockKey) {
  const token = 'sk-s2a-' + randomToken(40)
  issuedKeys.set(k.id, token)
  legacyKeyIds.delete(k.id)
  k.key_prefix = token.slice(0, KEY_PREFIX_LENGTH)
  return token
}

function keyOut(k: MockKey) {
  const g = groups.find((x) => x.id === k.group_id)
  return { copyable: !legacyKeyIds.has(k.id), rpm_limit: 0, tpm_limit: 0, max_concurrency: 0, quota_limit: '0', quota_used: k.id === 1 ? '3.2' : '0', ...k, group_id: g ? g.id : null, live: liveOf(k), user_email: users.find((u) => u.id === k.user_id)?.email, group_name: g?.name ?? '', group_status: keyGroupStatus(k), platforms: g ? groupPlatforms(g.id) : [] }
}

function randomToken(n: number) {
  const abc = 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789'
  let s = ''
  for (let i = 0; i < n; i++) s += abc[Math.floor(Math.random() * abc.length)]
  return s
}

/** A key under the status filter: one that is switched off is "disabled" even past its expiry. */
function keyListStatus(k: MockKey) {
  if (k.status !== 'active') return 'disabled'
  return k.expires_at && new Date(k.expires_at).getTime() <= Date.now() ? 'expired' : 'active'
}

/**
 * The list filters of GET /me/api-keys (CONTRACTS §92.1), ignoring case: q is
 * in the name, or starts the key prefix when it starts as a key and says more
 * than "sk-s2a-", which every key starts with (a whole key is compared by
 * its prefix); status and group_id match exactly.
 */
function keyMatches(k: MockKey, q: Record<string, string>) {
  const text = String(q.q || '').trim().toLowerCase()
  const prefix = text.startsWith('sk-s2a-') && text.length > 'sk-s2a-'.length ? text.slice(0, KEY_PREFIX_LENGTH) : ''
  if (text && !k.name.toLowerCase().includes(text) && !(prefix && k.key_prefix.toLowerCase().startsWith(prefix))) return false
  if (q.status && q.status !== keyListStatus(k)) return false
  return !q.group_id || k.group_id === Number(q.group_id)
}
on('GET', '/me/api-keys', (req) => paginate(keys.filter((k) => k.user_id === ME_ID && keyMatches(k, req.query)).map(keyOut), req.query))
on('POST', '/me/api-keys', (req) => {
  const b = req.body || {}
  if (!String(b.name || '').trim()) return invalid('name', 'name is required')
  if (!myGroups(caller(req).id).some((g) => g.id === Number(b.group_id))) return invalid('group_id', 'group not available')
  for (const f of LIMIT_FIELDS) if (b[f] !== undefined && !(Number(b[f]) >= 0)) return invalid(f, 'must be between 0 (unlimited) and the maximum')
  const k: MockKey = { id: nextId(), user_id: ME_ID, group_id: Number(b.group_id), rpm_limit: Number(b.rpm_limit) || 0, tpm_limit: Number(b.tpm_limit) || 0, max_concurrency: Number(b.max_concurrency) || 0, quota_limit: String(Number(b.quota_limit) || 0), quota_used: '0', allowed_ips: b.allowed_ips || [], name: b.name, key_prefix: '', status: 'active', expires_at: b.expires_at || null, last_used_at: null, created_at: now() }
  const key = issueKey(k)
  keys.unshift(k)
  return { ...keyOut(k), key }
})
on('PATCH', '/me/api-keys/:id', (req) => {
  const k = keys.find((x) => x.id === Number(req.params.id) && x.user_id === ME_ID)
  if (!k) return fail(404, 'not_found', 'api key not found')
  const b = req.body || {}
  if (b.status === undefined && b.group_id === undefined && LIMIT_FIELDS.every((f) => b[f] === undefined) && b.quota_limit === undefined && !b.reset_quota && b.name === undefined && b.expires_at === undefined && b.allowed_ips === undefined) return fail(400, 'invalid_argument', 'nothing to update')
  for (const f of LIMIT_FIELDS) if (b[f] !== undefined && !(Number(b[f]) >= 0)) return invalid(f, 'must be between 0 (unlimited) and the maximum')
  if (b.status !== undefined && b.status !== 'active' && b.status !== 'disabled') return invalid('status', 'status must be active or disabled')
  if (b.group_id !== undefined && !myGroups(caller(req).id).some((g) => g.id === Number(b.group_id))) return invalid('group_id', 'group not available')
  if (b.status !== undefined) k.status = b.status
  if (b.group_id !== undefined) k.group_id = Number(b.group_id)
  for (const f of LIMIT_FIELDS) if (b[f] !== undefined) k[f] = Number(b[f])
  if (b.quota_limit !== undefined) {
    if (!(Number(b.quota_limit) >= 0)) return invalid('quota_limit', 'must be between 0 (unlimited) and 1000000000 USD')
    k.quota_limit = String(Number(b.quota_limit))
  }
  if (b.reset_quota) k.quota_used = '0'
  if (b.name !== undefined) {
    if (!String(b.name).trim()) return invalid('name', 'name must be 1-100 characters')
    k.name = String(b.name).trim()
  }
  if (b.expires_at !== undefined) k.expires_at = b.expires_at
  if (b.allowed_ips !== undefined) k.allowed_ips = b.allowed_ips
  return keyOut(k)
})
on('POST', '/me/api-keys/:id/reveal', (req) => {
  const k = keys.find((x) => x.id === Number(req.params.id) && x.user_id === ME_ID)
  if (!k) return fail(404, 'not_found', 'api key not found')
  if (legacyKeyIds.has(k.id)) return fail(409, 'conflict', 'This legacy key cannot be recovered. Create a new key to enable copying.')
  return { key: plaintextOf(k) }
})
on('POST', '/me/api-keys/:id/rotate', (req) => {
  const k = keys.find((x) => x.id === Number(req.params.id) && x.user_id === ME_ID)
  if (!k) return fail(404, 'not_found', 'api key not found')
  const key = issueKey(k)
  return { ...keyOut(k), key }
})
on('DELETE', '/me/api-keys/:id', (req) => {
  const k = keys.find((x) => x.id === Number(req.params.id) && x.user_id === ME_ID)
  if (!k) return fail(404, 'not_found', 'api key not found')
  keys.splice(keys.indexOf(k), 1)
  return noContent()
})
