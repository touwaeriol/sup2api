// Mock groups: the groups with their model policy and the models they
// provide, and the groups a user may use (GET /me/groups).
import { offeredModels } from '../src/components/pricing/offeredModels'
import { fail, nextId, noContent, now, on, paginate } from './router'
import { caller } from './core'
import { accounts, groupAccountCount, groupPlatforms, groupServed } from './accounts'
import { pricedModels } from './billing'
import { accessOf, forgetGroup, groupAccess, modelRatesWhere, rateOf, modelPricesWhere, segmentOfUser } from './pricingStore'
import { applyGroupPricing, fieldsFailure, groupPricingErrors, validRate } from './pricingWrites'
import { invalid, match } from './common'
import { keys } from './apiKeys'

export interface MockGroup {
  id: number
  name: string
  description: string
  status: string
  rate_multiplier: string
  visibility: 'public' | 'restricted'
  model_allowlist: string[]
  model_filter_mode?: 'whitelist' | 'blacklist'
  account_count: number
  created_at: string
}

export const groups: MockGroup[] = [
  { id: 1, name: 'default', description: 'Default group for everyone', status: 'active', rate_multiplier: '1', visibility: 'public', model_allowlist: [], account_count: 8, created_at: now(-86400 * 60) },
  { id: 2, name: 'vip', description: 'Discounted Claude access', status: 'active', rate_multiplier: '0.8', visibility: 'restricted', model_allowlist: ['claude-*'], account_count: 3, created_at: now(-86400 * 30) },
  { id: 3, name: 'haiku-only', description: 'Cheap models for batch jobs', status: 'disabled', rate_multiplier: '0.5', visibility: 'restricted', model_allowlist: ['claude-3-5-haiku*', 'claude-haiku-4*'], account_count: 1, created_at: now(-86400 * 5) },
  { id: 4, name: 'gemini-trial', description: 'Planned Gemini access (no accounts yet)', status: 'active', rate_multiplier: '1', visibility: 'public', model_allowlist: ['gemini-*'], account_count: 0, created_at: now(-86400 * 1) }
]

/** The models a group sets a price or a rate of its own on. */
const setModelCount = (gid: number) => new Set([...modelPricesWhere({ group_id: gid }), ...modelRatesWhere({ group_id: gid, segment_id: null })].map((r) => r.model)).size

// account_count and platforms come from the accounts mock (CONTRACTS §13);
// the key count uses the server spelling api_key_count.
function groupOut(g: MockGroup) {
  return { ...g, account_count: groupAccountCount(g.id), platforms: groupPlatforms(g.id), api_key_count: keys.filter((k) => k.group_id === g.id).length, access: groupAccess(g), price_rule_count: setModelCount(g.id), model_count: groupModels(g).length }
}

/**
 * The models a group provides: those its accounts declare, through its model
 * policy. An account that declares none serves every model, which here is
 * every model with a global price.
 */
export function groupModels(g: Pick<MockGroup, 'id' | 'model_allowlist' | 'model_filter_mode'>): string[] {
  const members = accounts.filter((a) => a.group_ids.includes(g.id))
  const models = members.flatMap((a) => a.models || [])
  const unrestricted = members.filter((a) => !a.models?.length).length
  return offeredModels({ models, unrestricted_accounts: unrestricted, model_allowlist: g.model_allowlist, model_filter_mode: g.model_filter_mode ?? 'whitelist' }, pricedModels())
}

/** The first wrong field of the group's own, else every wrong item of its prices and rates (pricingWrites): any of them changes nothing. */
function validateGroup(b: any) {
  if (b.name !== undefined && !String(b.name).trim()) return invalid('name', 'name is required')
  if (b.rate_multiplier !== undefined && !validRate(b.rate_multiplier)) return invalid('rate_multiplier', 'must be 0 or more, below 1000000, with at most 4 decimals')
  if (b.visibility !== undefined && !['public', 'restricted'].includes(b.visibility)) return invalid('visibility', 'invalid visibility')
  return fieldsFailure(groupPricingErrors(b)) ?? null
}

// ?model=<name> keeps the groups that provide that model.
on('GET', '/groups', (req) => {
  const { q, model } = req.query
  const rows = groups.filter((g) => match(q, g.name, g.description) && (!model || groupModels(g).includes(model)))
  return paginate(rows.map(groupOut), req.query)
})
on('GET', '/groups/:id/models', (req) => {
  const gid = Number(req.params.id)
  if (!groups.some((g) => g.id === gid)) return fail(404, 'not_found', 'group not found')
  const members = accounts.filter((a) => a.group_ids.includes(gid))
  return { models: [...new Set(members.flatMap((a) => a.models || []))].sort(), unrestricted_accounts: members.filter((a) => !a.models?.length).length }
})
on('GET', '/groups/:id', (req) => {
  const g = groups.find((x) => x.id === Number(req.params.id))
  // served_protocols comes with a single group only.
  return g ? { ...groupOut(g), served_protocols: groupServed(g.id) } : fail(404, 'not_found', 'group not found')
})
on('POST', '/groups', (req) => {
  const b = req.body || {}
  const err = validateGroup({ name: b.name ?? '', ...b })
  if (err) return err
  if (groups.some((g) => g.name === b.name)) return invalid('name', 'name already in use')
  const g: MockGroup = {
    id: nextId(),
    name: b.name,
    description: b.description || '',
    status: b.status || 'active',
    rate_multiplier: String(b.rate_multiplier ?? '1'),
    visibility: b.visibility || 'public',
    model_allowlist: b.model_allowlist || [],
    model_filter_mode: b.model_filter_mode || 'blacklist',
    account_count: 0,
    created_at: now()
  }
  groups.push(g)
  applyGroupPricing(g.id, b)
  return groupOut(g)
})
on('PATCH', '/groups/:id', (req) => {
  const g = groups.find((x) => x.id === Number(req.params.id))
  if (!g) return fail(404, 'not_found', 'group not found')
  const err = validateGroup(req.body || {})
  if (err) return err
  const b = req.body || {}
  for (const k of ['name', 'description', 'status', 'visibility', 'model_allowlist', 'model_filter_mode'] as const) if (b[k] !== undefined) (g as any)[k] = b[k]
  if (b.rate_multiplier !== undefined) g.rate_multiplier = String(b.rate_multiplier)
  applyGroupPricing(g.id, b)
  return groupOut(g)
})
on('DELETE', '/groups/:id', (req) => {
  const g = groups.find((x) => x.id === Number(req.params.id))
  if (!g) return fail(404, 'not_found', 'group not found')
  // Keys are only loosely bound: they move to move_keys_to, or stay unbound.
  let to: number | null = null
  if (req.query.move_keys_to !== undefined && req.query.move_keys_to !== '') {
    to = Number(req.query.move_keys_to)
    if (to === g.id || !groups.some((x) => x.id === to)) return invalid('move_keys_to', 'pick another existing group')
  }
  for (const k of keys) if (k.group_id === g.id) k.group_id = to
  groups.splice(groups.indexOf(g), 1)
  forgetGroup(g.id)
  return noContent()
})

/** The active groups the segment of a user may use, each with the rate that user gets in it. */
export function myGroups(userId: number) {
  const s = segmentOfUser(userId)
  return groups
    .filter((g) => g.status === 'active' && accessOf(s, g).accessible)
    .map((g) => {
      const rate = rateOf(s, g)
      return { ...groupOut(g), effective_rate: rate.value, rate_source: rate.source, exclusive: g.visibility === 'restricted' }
    })
}

/** A row of GET /me/groups: the fields the server's group.MyGroup has, no more; served_protocols without the account counts. */
function myGroupOut(g: ReturnType<typeof myGroups>[number]) {
  const { id, name, description, rate_multiplier, model_allowlist, model_filter_mode, platforms, effective_rate, rate_source, exclusive } = g
  const served_protocols = groupServed(id).map(({ protocol }) => ({ protocol }))
  return { id, name, description, rate_multiplier, model_allowlist, model_filter_mode, platforms, served_protocols, effective_rate, rate_source, exclusive }
}
on('GET', '/me/groups', (req) => myGroups(caller(req).id).map(myGroupOut))
