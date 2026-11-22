// Mock users: the users with their roles, their owner (the team tree,
// CONTRACTS §103) and user group, and balance adjustments.
import { fail, nextId, noContent, now, on, paginate, type MockRequest } from './router'
import { agentUser, caller, hasPerm, moderatorUser, pricerUser, readonlyUser, resolveTeams, stepUp, vendorUser, type Identity } from './core'
import { findSegment, forgetUser, moveUser, segmentOfUser, segmentRef } from './pricingStore'
import { invalid, match } from './common'
import { managesRole, roles } from './roles'

export interface MockUser {
  id: number
  email: string
  display_name: string
  status: string
  roles: string[]
  balance: string
  /** The user above (CONTRACTS §103.3). */
  owner_id: number | null
  last_login_at: string | null
  created_at: string
  updated_at: string
}

export const users: MockUser[] = [
  { id: 1, email: 'admin@example.com', display_name: 'Admin', status: 'active', roles: ['super_admin'], balance: '12.34000000', owner_id: null, last_login_at: now(-60), created_at: now(-86400 * 90), updated_at: now(-86400) },
  { id: 2, email: 'ops@example.com', display_name: '张三', status: 'active', roles: ['operator', 'user'], balance: '85.50000000', owner_id: null, last_login_at: now(-3600 * 5), created_at: now(-86400 * 40), updated_at: now(-86400 * 3) },
  { id: 3, email: 'li.si@example.com', display_name: '李四', status: 'active', roles: ['operator'], balance: '3.21000000', owner_id: null, last_login_at: now(-86400 * 2), created_at: now(-86400 * 20), updated_at: now(-86400 * 2) },
  { id: 4, email: 'dev@acme.io', display_name: 'Acme Dev', status: 'active', roles: ['user'], balance: '250.00000000', owner_id: null, last_login_at: now(-600), created_at: now(-86400 * 10), updated_at: now(-86400) },
  { id: 5, email: 'old@example.com', display_name: 'Former user', status: 'disabled', roles: ['user'], balance: '0.00000000', owner_id: null, last_login_at: null, created_at: now(-86400 * 80), updated_at: now(-86400 * 30) },
  { id: agentUser.id, email: agentUser.email, display_name: agentUser.display_name, status: 'active', roles: ['supplier_agent'], balance: '0.00000000', owner_id: null, last_login_at: now(-1800), created_at: now(-86400 * 6), updated_at: now(-86400) },
  // The vendor and the first two generated users are the agent's team (CONTRACTS §103).
  { id: vendorUser.id, email: vendorUser.email, display_name: vendorUser.display_name, status: 'active', roles: ['vendor'], balance: '0.00000000', owner_id: agentUser.id, last_login_at: now(-7200), created_at: now(-86400 * 5), updated_at: now(-86400) },
  { id: readonlyUser.id, email: readonlyUser.email, display_name: readonlyUser.display_name, status: 'active', roles: ['readonly'], balance: '0.00000000', owner_id: null, last_login_at: now(-86400), created_at: now(-86400 * 5), updated_at: now(-86400) },
  { id: pricerUser.id, email: pricerUser.email, display_name: pricerUser.display_name, status: 'active', roles: ['pricer'], balance: '0.00000000', owner_id: null, last_login_at: now(-86400), created_at: now(-86400 * 5), updated_at: now(-86400) },
  { id: moderatorUser.id, email: moderatorUser.email, display_name: moderatorUser.display_name, status: 'active', roles: ['moderator'], balance: '0.00000000', owner_id: null, last_login_at: now(-3600), created_at: now(-86400 * 2), updated_at: now(-86400) }
]
for (let i = 0; i < 18; i++) {
  users.push({ id: 100 + i, email: `user${i + 1}@example.org`, display_name: `User ${i + 1}`, status: i % 7 === 6 ? 'disabled' : 'active', roles: ['user'], balance: (Math.random() * 50).toFixed(8), owner_id: i < 2 ? agentUser.id : null, last_login_at: i % 3 ? now(-3600 * i) : null, created_at: now(-86400 * (i + 1)), updated_at: now(-3600 * i) })
}

function findUser(req: MockRequest) {
  return users.find((u) => u.id === Number(req.params.id))
}

/**
 * The users below uid, the whole subtree (users.owner_id, CONTRACTS §103.2:
 * reads do not truncate, the server bounds the depth on write).
 */
function teamOf(uid: number): number[] {
  const out: number[] = []
  let frontier = [uid]
  while (frontier.length) {
    frontier = users.filter((u) => u.owner_id !== null && frontier.includes(u.owner_id) && !out.includes(u.id)).map((u) => u.id)
    out.push(...frontier)
  }
  return out
}
resolveTeams(teamOf)

/** The users a user action reaches (CONTRACTS §103.3): every user (null), the caller's team, or none (undefined). */
function userReach(who: Identity, action: string): number[] | null | undefined {
  if (hasPerm(who, action)) return null
  return hasPerm(who, `${action}:team`) ? teamOf(who.id) : undefined
}
const reaches = (reach: number[] | null, id: number) => reach === null || reach.includes(id)
const noUserAction = (action: string) => fail(403, 'permission_denied', `${action} is required`, { permission: action })

/**
 * The owner_id of POST /users or PATCH /users/:id for user `id` (0 when new):
 * a team holder names only itself; anyone else any user but the user itself
 * and those below it, or 0 for none. A response when refused.
 */
function ownerFor(who: Identity, reach: number[] | null, id: number, want: unknown): { owner: number | null } | ReturnType<typeof fail> {
  if (reach !== null) return want == null || Number(want) === who.id ? { owner: who.id } : fail(403, 'permission_denied', 'a team holder names only itself as the owner')
  const owner = Number(want) || null
  if (owner === null) return { owner }
  if (owner === id) return invalid('owner_id', 'a user cannot own itself', 'self')
  if (!users.some((u) => u.id === owner)) return invalid('owner_id', 'no such user', 'not_found')
  return teamOf(id).includes(owner) ? invalid('owner_id', 'the owner is below the user', 'cycle') : { owner }
}

/** A user as the API returns it: with its segment and its owner's email. */
export function userOut(u: MockUser) {
  return { ...u, owner_email: users.find((x) => x.id === u.owner_id)?.email ?? null, segment: segmentRef(segmentOfUser(u.id)) }
}

/** The ?segment= filter of the users list: a segment id, or "personal" for every personal segment. */
function inSegment(u: MockUser, want: string | undefined) {
  if (!want) return true
  const s = segmentOfUser(u.id)
  return want === 'personal' ? s.kind === 'personal' : s.id === Number(want)
}

/** ?owner_id=<id>|none: directly under that user, or under none; ?direct=true: directly under the caller. */
function ownerMatch(u: MockUser, q: Record<string, string>, who: Identity) {
  if (q.owner_id && u.owner_id !== (q.owner_id === 'none' ? null : Number(q.owner_id))) return false
  return q.direct !== 'true' || u.owner_id === who.id
}
on('GET', '/users', (req) => {
  const who = caller(req)
  const reach = userReach(who, 'user:read')
  if (reach === undefined) return noUserAction('user:read')
  const { q, status, role, segment } = req.query
  const items = users.filter((u) => reaches(reach, u.id) && match(q, u.email, u.display_name) && (!status || u.status === status) && (!role || u.roles.includes(role)) && inSegment(u, segment) && ownerMatch(u, req.query, who))
  return paginate(items.map(userOut), req.query)
})
on('GET', '/users/:id', (req) => {
  const reach = userReach(caller(req), 'user:read')
  if (reach === undefined) return noUserAction('user:read')
  const u = findUser(req)
  return u && reaches(reach, u.id) ? userOut(u) : fail(404, 'not_found', 'user not found')
})
on('POST', '/users', (req) => {
  const who = caller(req)
  const reach = userReach(who, 'user:create')
  if (reach === undefined) return noUserAction('user:create')
  const b = req.body || {}
  const roleKeys: string[] = Array.isArray(b.role_keys) && b.role_keys.length ? b.role_keys : ['user']
  // Roles other than the default are a role assignment, the user group a
  // segment assignment: each needs its key as well, checked before the body.
  if ((roleKeys.length > 1 || roleKeys[0] !== 'user') && !hasPerm(who, 'role:assign')) return noUserAction('role:assign')
  if (b.segment_id != null && !hasPerm(who, 'segment:manage')) return noUserAction('segment:manage')
  if (!/^[^@\s]+@[^@\s]+$/.test(b.email || '')) return invalid('email', 'invalid email address')
  if (String(b.password || '').length < 8) return invalid('password', 'at least 8 characters', 'too_short')
  if (Number(b.owner_id) < 0) return invalid('owner_id', 'must be a user id, or 0 for none')
  // segment_id: the named user group the user starts in; without it, the default one.
  const segment = b.segment_id == null ? null : findSegment(Number(b.segment_id))
  if (segment !== null && segment?.kind !== 'named') return invalid('segment_id', 'unknown user group')
  if (users.some((u) => u.email === b.email)) return fail(409, 'conflict', 'email already in use', { fields: [{ field: 'email', code: 'taken', message: 'email already in use' }] })
  const owner = ownerFor(who, reach, 0, b.owner_id)
  if (!('owner' in owner)) return owner
  // Every role must be one the caller assigns, but the default one (authz checkAssignment).
  const unknown = roleKeys.filter((k) => !roles.some((r) => r.key === k))
  if (unknown.length) return invalid('role_keys', `unknown roles: ${unknown.join(', ')}`, 'unknown')
  const refused = roleKeys.find((k) => k !== 'user' && !managesRole(who, k))
  if (refused) return fail(403, 'permission_denied', 'you cannot assign or revoke this role', { role: refused })
  const u: MockUser = { id: nextId(), email: b.email, display_name: b.display_name || '', status: 'active', roles: roleKeys, balance: '0.00000000', owner_id: owner.owner, last_login_at: null, created_at: now(), updated_at: now() }
  users.unshift(u)
  if (segment) moveUser(u.id, segment)
  return userOut(u)
})
on('PATCH', '/users/:id', (req) => {
  const who = caller(req)
  const reach = userReach(who, 'user:update')
  if (reach === undefined) return noUserAction('user:update')
  const u = findUser(req)
  if (!u || !reaches(reach, u.id)) return fail(404, 'not_found', 'user not found')
  const b = req.body || {}
  if (b.owner_id != null) {
    const owner = ownerFor(who, reach, u.id, b.owner_id)
    if (!('owner' in owner)) return owner
    u.owner_id = owner.owner
  }
  if (b.display_name !== undefined) u.display_name = b.display_name
  if (b.status !== undefined) u.status = b.status
  u.updated_at = now()
  return userOut(u)
})
on('DELETE', '/users/:id', stepUp('user.delete', (req) => {
  const u = findUser(req)
  if (!u) return fail(404, 'not_found', 'user not found')
  if (u.roles.includes('super_admin') && users.filter((x) => x.roles.includes('super_admin')).length <= 1) return fail(409, 'conflict', 'at least one super_admin must remain')
  users.splice(users.indexOf(u), 1)
  forgetUser(u.id)
  return noContent()
}))
// role:assign, on a user in reach of user:update; every role added or taken
// away must be one the caller manages (authz checkAssignment).
on('PUT', '/users/:id/roles', (req) => {
  const who = caller(req)
  if (!hasPerm(who, 'role:assign')) return noUserAction('role:assign')
  const reach = userReach(who, 'user:update')
  const u = findUser(req)
  if (!u || reach === undefined || !reaches(reach, u.id)) return fail(404, 'not_found', 'user not found')
  const keys: string[] = Array.isArray(req.body?.role_keys) ? req.body.role_keys : []
  const bad = keys.find((k) => !roles.some((r) => r.key === k))
  if (bad) return invalid('role_keys', `unknown role ${bad}`)
  const changed = [...keys.filter((k) => !u.roles.includes(k)), ...u.roles.filter((k) => !keys.includes(k))]
  const refused = changed.find((k) => !managesRole(who, k))
  if (refused) return fail(403, 'permission_denied', `${refused} is not a role you may assign`, { role: refused })
  u.roles = keys
  return userOut(u)
})
on('POST', '/users/:id/balance/adjust', (req) => {
  const u = findUser(req)
  if (!u) return fail(404, 'not_found', 'user not found')
  const amount = String(req.body?.amount ?? '')
  if (!/^\d+(\.\d{1,8})?$/.test(amount) || Number(amount) <= 0) return invalid('amount', 'amount must be a positive decimal')
  const delta = (req.body?.credit ? 1 : -1) * Number(amount)
  const next = Number(u.balance) + delta
  if (next < 0) return fail(402, 'insufficient_balance', 'balance would become negative')
  u.balance = next.toFixed(8)
  return { id: nextId(), user_id: u.id, delta: delta.toFixed(8), balance_after: u.balance, kind: 'admin_adjust', ref_type: 'admin', ref_id: '', note: req.body?.note || '', created_at: now() }
})
