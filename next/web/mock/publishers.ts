// Mock plugin publishers and their signing keys; every change asks for step-up.
import { fail, nextId, now, on, paginate } from './router'
import { stepUp } from './core'
import { invalid } from './common'

interface MockPublisherKey {
  key_id: string
  public_key: string
  status: string
  not_before: string | null
  not_after: string | null
  created_at: string
}
interface MockPublisher {
  id: number
  name: string
  trust_level: string
  status: string
  created_at: string
  revoked_at: string | null
  keys: MockPublisherKey[]
}

const publishers: MockPublisher[] = [
  { id: 1, name: 'sub2api', trust_level: 'official', status: 'active', created_at: now(-86400 * 120), revoked_at: null, keys: [
    { key_id: 'sub2api-2026', public_key: 'Hn0l9pJ3vWqg1H1Zb7xg6Yx0cF5m3r2Lk8QwTzA4uYs=', status: 'active', not_before: now(-86400 * 120), not_after: now(86400 * 365), created_at: now(-86400 * 120) },
    { key_id: 'sub2api-2025', public_key: 'q8J2k5L9m1N4b7V0c3X6z9A2s5D8f1G4h7J0k3L6m9Q=', status: 'revoked', not_before: now(-86400 * 500), not_after: now(-86400 * 100), created_at: now(-86400 * 500) }
  ] },
  { id: 2, name: 'acme', trust_level: 'verified', status: 'active', created_at: now(-86400 * 30), revoked_at: null, keys: [
    { key_id: 'acme-1', public_key: 'Zm9vYmFyYmF6cXV4cXV1eGNvcmdlZ3JhdWx0Z2FycGw=', status: 'active', not_before: null, not_after: null, created_at: now(-86400 * 30) }
  ] },
  { id: 4, name: 'ourteam', trust_level: 'verified', status: 'active', created_at: now(-86400 * 7), revoked_at: null, keys: [
    { key_id: 'ourteam-1', public_key: 'b3VydGVhbXNpZ25pbmdrZXlmb3JtYW51YWx1cGxvYWQ=', status: 'active', not_before: null, not_after: null, created_at: now(-86400 * 7) }
  ] },
  { id: 3, name: 'shady-plugins', trust_level: 'community', status: 'revoked', created_at: now(-86400 * 60), revoked_at: now(-86400 * 10), keys: [] }
]

function b64Len(s: string) {
  try {
    return Buffer.from(s, 'base64').length
  } catch {
    return 0
  }
}

on('GET', '/publishers', (req) => paginate(publishers, req.query))
on('POST', '/publishers', stepUp('publisher.manage', (req) => {
  const b = req.body || {}
  if (!String(b.name || '').trim()) return invalid('name', 'name is required')
  if (!['official', 'verified', 'community'].includes(b.trust_level)) return invalid('trust_level', 'invalid trust level')
  if (publishers.some((p) => p.name === b.name)) return fail(409, 'conflict', 'publisher already exists')
  const p: MockPublisher = { id: nextId(), name: b.name, trust_level: b.trust_level, status: 'active', created_at: now(), revoked_at: null, keys: [] }
  publishers.push(p)
  return p
}))
on('POST', '/publishers/:id/keys', stepUp('publisher.manage', (req) => {
  const p = publishers.find((x) => x.id === Number(req.params.id))
  if (!p) return fail(404, 'not_found', 'publisher not found')
  const b = req.body || {}
  if (!String(b.key_id || '').trim()) return invalid('key_id', 'key_id is required')
  if (publishers.some((x) => x.keys.some((k) => k.key_id === b.key_id))) return invalid('key_id', 'key_id already exists')
  if (b64Len(String(b.public_key || '')) !== 32) return invalid('public_key', 'public key must be 32 bytes of base64')
  const k: MockPublisherKey = { key_id: b.key_id, public_key: b.public_key, status: 'active', not_before: b.not_before || null, not_after: b.not_after || null, created_at: now() }
  p.keys.push(k)
  return k
}))
on('POST', '/publishers/:id/revoke', stepUp('publisher.manage', (req) => {
  const p = publishers.find((x) => x.id === Number(req.params.id))
  if (!p) return fail(404, 'not_found', 'publisher not found')
  p.status = 'revoked'
  p.revoked_at = now()
  p.keys.forEach((k) => (k.status = 'revoked'))
  return p
}))
on('POST', '/publisher-keys/:key_id/revoke', stepUp('publisher.manage', (req) => {
  for (const p of publishers) {
    const k = p.keys.find((x) => x.key_id === req.params.key_id)
    if (k) {
      k.status = 'revoked'
      return k
    }
  }
  return fail(404, 'not_found', 'key not found')
}))
