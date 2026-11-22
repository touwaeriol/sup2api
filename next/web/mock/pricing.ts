// Mock handlers: group pricing. User groups ("segments") with their grants
// and rates per account group ("group"), the pricing table of a group and the
// catalog of global prices, personal segments, the price quote and the
// caller's own prices. State and resolution live in pricingStore.ts, the
// checks of what a write carries in pricingWrites.ts; the handlers here read
// the request, call them and build the API rows.
import { fail, noContent, on, paginate, type MockHandler, type MockRequest, type MockResult } from './router'
import { caller, hasPerm } from './core'
import { groupPlatforms } from './accounts'
import { globalPrice, money, priceCost, pricedModels } from './billing'
import type { PricingQuote } from '../src/api/pricing'
import { invalid, match } from './common'
import { groupModels, groups, myGroups, type MockGroup } from './groups'
import { userOut, users, type MockUser } from './users'
import {
  accessOf,
  addSegment,
  defaultSegment,
  findSegment,
  grantedGroupIds,
  listPriceOf,
  makePersonal,
  modelRateOf,
  modelRatesWhere,
  moveUser,
  pairsWhere,
  rateLevelsOf,
  rateOf,
  rateOverrideOf,
  removeSegment,
  rulesWhere,
  segmentOfUser,
  segmentRef,
  segments,
  type MockSegment
} from './pricingStore'
import { applySegmentRates, fieldsFailure, segmentRatesErrors } from './pricingWrites'

// ------------------------------------------------------------------ routing helpers

/** The 403 of a caller without `perm`. */
const denied = (perm: string) => fail(403, 'permission_denied', `${perm} is required`, { permission: perm })

/** The first of `perms` the caller does not hold. */
const missingPerm = (req: MockRequest, perms: string[]) => perms.find((perm) => !hasPerm(caller(req), perm))

/** Registers a handler that needs every permission of `perms`; the first one missing is named. */
function route(method: string, path: string, perms: string | string[], handler: MockHandler) {
  on(method, path, (req) => {
    const missing = missingPerm(req, [perms].flat())
    return missing ? denied(missing) : handler(req)
  })
}

/** Whether the caller may read users: only then does a personal segment go by its user's email (segment.Masked). */
const showsUsers = (req: MockRequest) => hasPerm(caller(req), 'user:read')

/** A handler on the row `find` gives for the request; 404 when there is none. */
function onRow<T>(what: string, find: (req: MockRequest) => T | undefined, handler: (row: T, req: MockRequest) => MockResult): MockHandler {
  return (req) => {
    const row = find(req)
    return row ? handler(row, req) : fail(404, 'not_found', `${what} not found`)
  }
}

const idOf = (req: MockRequest) => Number(req.params.id)
const onSegment = (handler: (s: MockSegment, req: MockRequest) => MockResult) => onRow('user group', (req) => findSegment(idOf(req)), handler)
const onGroup = (handler: (g: MockGroup, req: MockRequest) => MockResult) => onRow('group', (req) => groups.find((g) => g.id === idOf(req)), handler)
const onUser = (handler: (u: MockUser, req: MockRequest) => MockResult) => onRow('user', (req) => users.find((u) => u.id === idOf(req)), handler)

/** An id in the query as the server parses one: a positive integer ("007" is 7). */
const positive = (v: string) => /^\d+$/.test(v) && Number(v) > 0

// ------------------------------------------------------------------ rows

const membersOf = (s: MockSegment) => users.filter((u) => segmentOfUser(u.id).id === s.id)
const namedFirst = (a: MockSegment, b: MockSegment) => Number(a.kind === 'personal') - Number(b.kind === 'personal')
/** The order of GET /segments: the default segment, the named ones, the personal ones, each by id. */
const listOrder = (a: MockSegment, b: MockSegment) => Number(b.is_default) - Number(a.is_default) || namedFirst(a, b) || a.id - b.id

/** A segment as the caller may see it: a personal one goes by its user only with `showUsers`. */
function segmentOut(s: MockSegment, showUsers: boolean) {
  const granted = grantedGroupIds(s.id)
  return {
    ...segmentRef(s, showUsers),
    description: s.description,
    is_default: s.is_default,
    member_count: membersOf(s).length,
    granted_groups: groups.filter((g) => g.visibility === 'restricted' && granted.includes(g.id)).map((g) => ({ id: g.id, name: g.name })),
    rate_overrides: pairsWhere({ segment_id: s.id })
      .filter((p) => p.rate_override !== null)
      .flatMap((p) => groups.filter((g) => g.id === p.group_id).map((g) => ({ id: g.id, name: g.name, rate: p.rate_override as string })))
      .sort((x, y) => x.id - y.id),
    ...(s.kind === 'personal' ? personalOut(s, showUsers) : {}),
    created_at: s.created_at
  }
}

/** What only a personal segment has: its user (for a caller who may read users), and the segment that user came from. */
function personalOut(s: MockSegment, showUsers: boolean) {
  const u = showUsers ? users.find((x) => x.id === s.user_id) : undefined
  const origin = findSegment(s.origin_id)
  return { ...(u ? { user: { id: u.id, email: u.email, display_name: u.display_name } } : {}), origin: origin ? segmentRef(origin) : null }
}

/** The settings of one segment in one group. */
function settingOut(s: MockSegment, g: MockGroup, showUsers: boolean) {
  const access = accessOf(s, g)
  return {
    segment: segmentRef(s, showUsers),
    group: { id: g.id, name: g.name, description: g.description, status: g.status, visibility: g.visibility, rate_multiplier: g.rate_multiplier, platforms: groupPlatforms(g.id) },
    accessible: access.accessible,
    granted: access.granted,
    rate_override: rateOverrideOf(s.id, g.id),
    rate: rateOf(s, g),
    model_rates: modelRatesWhere({ segment_id: s.id, group_id: g.id }).map(({ model, rate }) => ({ model, rate })).sort((a, b) => a.model.localeCompare(b.model)),
    member_count: membersOf(s).length
  }
}

const segmentSettings = (s: MockSegment, req: MockRequest) => groups.map((g) => settingOut(s, g, showsUsers(req)))

/** A personal segment is listed in a group only when it has something there. */
const hasSettingsIn = (s: MockSegment, g: MockGroup) => pairsWhere({ segment_id: s.id, group_id: g.id }).length > 0

/** The rows of a group: `include` (?include=<id>,<id>) keeps personal segments that have nothing there yet. */
function groupSettings(g: MockGroup, req: MockRequest) {
  const include = includedIn(req)
  return segments
    .filter((s) => s.kind === 'named' || hasSettingsIn(s, g) || include.includes(s.id))
    .sort(namedFirst)
    .map((s) => settingOut(s, g, showsUsers(req)))
}

/** One model of a group's pricing table: whether the group provides it, its global price, and the group's model price and model rate. */
function modelRow(g: MockGroup, model: string, offered: string[]) {
  const base = globalPrice(model)
  return { model, offered: offered.includes(model), base, price: listPriceOf(g.id, model, base).group_rule, rate: modelRateOf(null, g.id, model) }
}

/** The pricing table of a group: the models it provides and those it sets a price or a rate on, by model, at its base rate. */
function modelPrices(g: MockGroup) {
  const offered = groupModels(g)
  const set = [...rulesWhere({ group_id: g.id }), ...modelRatesWhere({ segment_id: null, group_id: g.id })].map((r) => r.model)
  const models = [...new Set([...offered, ...set])].sort()
  return { group: { id: g.id, name: g.name }, rate: rateOf(null, g), models: models.map((m) => modelRow(g, m, offered)) }
}

// ------------------------------------------------------------------ segments

/** The kind ?kind= lists: named (the default, and of a value it does not know), personal, or all. */
const listKind = (kind: string | undefined) => (kind === 'personal' || kind === 'all' ? kind : 'named')

/** Whether a segment is listed for ?kind=&q=: q matches the name and the description; the email and the display name of a personal segment's user only for a caller who may read users. */
function segmentMatches(s: MockSegment, q: Record<string, string>, showUsers: boolean) {
  const kind = listKind(q.kind)
  if (kind !== 'all' && s.kind !== kind) return false
  // The server keeps no name of a personal segment: what the mock keeps there is its user's email.
  const own = s.kind === 'named' ? [s.name, s.description] : [s.description]
  const u = showUsers ? users.find((x) => x.id === s.user_id) : undefined
  return match(q.q, ...own, u?.email, u?.display_name)
}

/** The longest name of a segment, as segments.name varchar(100); and of its description. */
const NAME_MAX = 100
const DESCRIPTION_MAX = 2000

/** The first invalid field of a segment body; `self` is the segment being edited. */
function segmentError(b: any, self?: MockSegment) {
  const name = String(b.name ?? '').trim()
  if (self?.kind === 'personal' && (b.name !== undefined || b.description !== undefined)) return invalid('name', 'a personal user group has no name or description')
  if (b.name !== undefined && !name) return invalid('name', 'name is required')
  if ([...name].length > NAME_MAX) return invalid('name', `name must be at most ${NAME_MAX} characters`)
  // Unique without regard to case, as segments_name_uq on lower(name).
  if (name && segments.some((s) => s !== self && s.kind === 'named' && s.name.toLowerCase() === name.toLowerCase())) return invalid('name', 'name already in use')
  if (b.description !== undefined && [...String(b.description)].length > DESCRIPTION_MAX) return invalid('description', 'description is too long')
  return fieldsFailure(segmentRatesErrors(b, (id) => groups.some((g) => g.id === id))) ?? null
}

route('GET', '/segments', 'segment:read', (req) => {
  const show = showsUsers(req)
  const rows = segments.filter((s) => segmentMatches(s, req.query, show)).sort(listOrder)
  return paginate(rows.map((s) => segmentOut(s, show)), req.query)
})
route('GET', '/segments/:id', 'segment:read', onSegment((s, req) => segmentOut(s, showsUsers(req))))
route('POST', '/segments', 'segment:manage', (req) => {
  const b = req.body || {}
  const err = segmentError({ ...b, name: b.name ?? '' })
  if (err) return err
  const s = addSegment(String(b.name).trim(), String(b.description ?? ''))
  applySegmentRates(s.id, b)
  return segmentOut(s, showsUsers(req))
})
route('PATCH', '/segments/:id', 'segment:manage', onSegment((s, req) => {
  const b = req.body || {}
  const err = segmentError(b, s)
  if (err) return err
  if (b.name !== undefined) s.name = String(b.name).trim()
  if (b.description !== undefined) s.description = String(b.description)
  applySegmentRates(s.id, b)
  return segmentOut(s, showsUsers(req))
}))
route('DELETE', '/segments/:id', 'segment:manage', onSegment((s) => {
  if (s.is_default) return fail(409, 'conflict', 'the default user group cannot be deleted')
  if (s.kind === 'personal') return fail(409, 'conflict', 'a personal user group is deleted by moving its user to a user group')
  removeSegment(s)
  return noContent()
}))

// The members are user rows: they take user:read as well.
route('GET', '/segments/:id/members', ['segment:read', 'user:read'], onSegment((s, req) => {
  const rows = membersOf(s).filter((u) => match(req.query.q, u.email, u.display_name))
  return paginate(rows.map(userOut), req.query)
}))
route('POST', '/segments/:id/members', 'segment:manage', onSegment((s, req) => {
  if (s.kind === 'personal') return fail(409, 'conflict', 'a personal user group has one member')
  const ids: number[] = Array.isArray(req.body?.user_ids) ? req.body.user_ids.map(Number) : []
  const unknown = ids.find((id) => !users.some((u) => u.id === id))
  if (unknown !== undefined) return invalid('user_ids', `unknown user ${unknown}`)
  return { moved: ids.filter((id) => moveUser(id, s)).length }
}))

// ------------------------------------------------------------------ grants and rates (read; written with the group or the segment)

function includedIn(req: MockRequest) {
  return String(req.query.include || '').split(',').filter(Boolean).map(Number)
}

route('GET', '/segments/:id/groups', 'segment:read', onSegment(segmentSettings))
route('GET', '/groups/:id/segments', 'group:read', onGroup(groupSettings))

// ------------------------------------------------------------------ pricing table (read; written with the group)

route('GET', '/groups/:id/model-prices', 'group:read', onGroup(modelPrices))
// GET /pricing/catalog: every enabled global price, by model.
route('GET', '/pricing/catalog', 'group:read', () => pricedModels().map((model) => ({ model, price: globalPrice(model)! })))

// ------------------------------------------------------------------ personal segments

route('POST', '/users/:id/personal-segment', 'segment:manage', onUser((u, req) => {
  if (segmentOfUser(u.id).kind === 'personal') return fail(409, 'conflict', 'the user has a personal user group already')
  return segmentOut(makePersonal(u, !!req.body?.copy), showsUsers(req))
}))
route('DELETE', '/users/:id/personal-segment', 'segment:manage', onUser((u, req) => {
  if (segmentOfUser(u.id).kind !== 'personal') return fail(404, 'not_found', 'the user has no personal user group')
  const to = req.query.to ? findSegment(Number(req.query.to)) : defaultSegment()
  if (to?.kind !== 'named') return invalid('to', 'pick a user group')
  moveUser(u.id, to)
  return noContent()
}))

// ------------------------------------------------------------------ quote

/** Who a quote is for: the segment of `user_id`, or `segment_id` (the handler checks that exactly one is given). */
function quoteSegment(b: any): MockSegment | undefined {
  if (b.user_id == null) return findSegment(Number(b.segment_id))
  return users.some((u) => u.id === Number(b.user_id)) ? segmentOfUser(Number(b.user_id)) : undefined
}

/** What a quote takes besides price:read, as the console's entry to it: the group's rules, and those of the user or the segment it names. */
const quotePerms = (b: any) => ['group:read', ...(b.user_id != null ? ['user:read'] : []), ...(b.segment_id != null ? ['segment:read'] : [])]

/** Whether a segment may use a group, as a quote says: nobody may use a disabled group. */
function quoteAccess(s: MockSegment, g: MockGroup): { accessible: boolean; source: PricingQuote['access_source'] } {
  return g.status === 'active' ? accessOf(s, g) : { accessible: false, source: 'disabled' }
}

/** The token variables of a quote's usage and their bound (core.MaxUsageTokens). */
const QUOTE_USAGE = ['p', 'c', 'cr', 'cc', 'cc1h']
const MAX_USAGE_TOKENS = 1e12

/** The 400 of a usage the server would not take: a count that is no number does not bind, one out of bounds is a field usage.<variable>. */
function usageFailure(usage: any): MockResult | undefined {
  const counts = QUOTE_USAGE.map((k) => [k, usage?.[k] ?? 0] as const)
  if (counts.some(([, v]) => typeof v !== 'number')) return fail(400, 'invalid_argument', 'invalid request body')
  const fields = counts.filter(([, v]) => v < 0 || v > MAX_USAGE_TOKENS).map(([k]) => ({ field: `usage.${k}`, code: 'invalid', message: `must be 0 to ${MAX_USAGE_TOKENS} tokens` }))
  return fields.length ? fail(400, 'invalid_argument', 'invalid', { fields }) : undefined
}

/** The quote of a model for segment s in group g; a price the usage cannot be evaluated with is a 400, as for a request. */
function quoteOf(req: MockRequest, s: MockSegment, g: MockGroup) {
  const b = req.body
  const access = quoteAccess(s, g)
  const base = globalPrice(b.model)
  const price = listPriceOf(g.id, b.model, base)
  const rate = rateOf(s, g, b.model)
  // Without a global price nothing is billed.
  const listCost = price.list_price ? priceCost(price.list_price, b.usage || {}) : 0
  if (listCost === null) return fail(400, 'invalid_argument', 'the price cannot be evaluated with this usage')
  return {
    segment: segmentRef(s, showsUsers(req)),
    accessible: access.accessible,
    access_source: access.source,
    base,
    list_price: price.list_price,
    price_source: price.source,
    price_levels: { group: price.group_rule },
    rate,
    rate_levels: rateLevelsOf(s, g, b.model),
    list_cost: money(listCost),
    cost: money(listCost * Number(rate.value))
  }
}

// POST /pricing/quote, in the order the server checks: the permissions, the subject, the usage, the group, the segment, the model, then the costs.
route('POST', '/pricing/quote', 'price:read', (req) => {
  const b = req.body || {}
  const missing = missingPerm(req, quotePerms(b))
  if (missing) return denied(missing)
  if ((b.user_id == null) === (b.segment_id == null)) return invalid('user_id', 'give either user_id or segment_id')
  const badUsage = usageFailure(b.usage)
  if (badUsage) return badUsage
  const g = groups.find((x) => x.id === Number(b.group_id))
  if (!g) return fail(404, 'not_found', 'group not found')
  const s = quoteSegment(b)
  if (!s) return fail(404, 'not_found', b.user_id == null ? 'user group not found' : 'user not found')
  if (!groupModels(g).includes(b.model)) return invalid('model', 'the group does not provide this model')
  return quoteOf(req, s, g)
})

// ------------------------------------------------------------------ the caller's prices

type MyGroup = ReturnType<typeof myGroups>[number]

/** Every model with a global price, at that price, with the caller's groups that provide it. */
function allPrices(mine: MyGroup[]) {
  const provided = mine.map((g) => ({ id: g.id, name: g.name, models: new Set(groupModels(g)) }))
  const groupsOf = (model: string) => provided.filter((g) => g.models.has(model)).map(({ id, name }) => ({ id, name }))
  return { group: null, rate_multiplier: '1', prices: pricedModels().map((model) => ({ model, ...globalPrice(model)!, groups: groupsOf(model) })) }
}

/** The models one of the caller's groups provides and can bill, at the list price and the rate the caller gets there. */
function groupPrices(g: MyGroup) {
  const prices = groupModels(g).flatMap((model) => {
    const { list_price, source } = listPriceOf(g.id, model, globalPrice(model))
    return list_price ? [{ model, ...list_price, source }] : []
  })
  return { group: { id: g.id, name: g.name }, rate_multiplier: g.effective_rate, rate_source: g.rate_source, prices }
}

// GET /me/prices (CONTRACTS §79.5): a group_id that is no positive integer is a 400, a group the caller cannot use a 404.
on('GET', '/me/prices', (req) => {
  const who = caller(req)
  const mine = myGroups(who.id)
  if (!req.query.group_id) return allPrices(mine)
  if (!positive(req.query.group_id)) return fail(400, 'invalid_argument', 'invalid group_id')
  const g = mine.find((x) => x.id === Number(req.query.group_id))
  return g ? groupPrices(g) : fail(404, 'not_found', 'group not found')
})
