// Mock of the entity lookup, GET /lookup/:kind (CONTRACTS, "lookup"): the
// candidates of the console's entity pickers. The protocol (the parameters and
// their 400s, q / ids / limit, the order, `more`) is written once in parse()
// and answer(); a kind only says who may read it and which rows of the other
// mock modules it lists (`mine` and `user_id` narrow those rows, so they hold
// under `ids` too). A request the core refuses is refused here as well: a
// page must not look right against the mock and fail against the core.
import { fail, on, type MockRequest } from './router'
import { caller, filterOwned, hasPerm, ownerScope, type Identity } from './core'
import { parsePermission } from '../src/authz/implies'
import { accounts } from './accounts'
import { pricedModels } from './billing'
import { ME_ID, keys } from './apiKeys'
import { groups, myGroups } from './groups'
import { proxies } from './proxies'
import { users } from './users'

interface Row {
  id: number | string
  name: string
  hint?: string
  status?: string
  /** Further text q matches (a user's display name). */
  also?: string
  /** Listed before the other rows (accounts of prefer_group_id). */
  preferred?: boolean
}
type Failure = ReturnType<typeof fail>
interface Kind {
  /** False for models: their id is the name, so a numeric q is not an id. */
  numericId?: boolean
  /** Parameters holding the id of another entity; the other kinds ignore them. */
  refs?: string[]
  rows(req: MockRequest, who: Identity, mine: boolean): Row[] | Failure
}
/** The parameters of one request, as the core reads them. */
interface Params {
  q: string
  /** The given ids (numbers; names for models), or null. */
  ids: Array<number | string> | null
  limit: number
  mine: boolean
}

const MAX_IDS = 100
const MAX_LIMIT = 50
const MAX_Q_BYTES = 256
/** The values the core takes for a boolean parameter (strconv.ParseBool). */
const BOOLEANS: Record<string, boolean> = { 1: true, t: true, T: true, TRUE: true, true: true, True: true, 0: false, f: false, F: false, FALSE: false, false: false, False: false }
const invalid = (message: string) => fail(400, 'invalid_argument', message)
const denied = (permission: string) => fail(403, 'permission_denied', `${permission} is required`, { permission })
const ownerEmail = (id: number) => users.find((u) => u.id === id)?.email
const INT64_MAX = BigInt('9223372036854775807')
/** A positive integer as the core parses one ("007" is 7; int64 at most), else undefined. */
const positive = (v: string) => (/^\d+$/.test(v) && Number(v) > 0 && BigInt(v) <= INT64_MAX ? Number(v) : undefined)

const KINDS: Record<string, Kind> = {
  users: {
    rows(_req, who, mine) {
      if (mine) return invalid('mine is not supported for users')
      if (!hasPerm(who, 'user:read')) return denied('user:read')
      return users.map((u) => ({ id: u.id, name: u.email, hint: u.display_name, also: u.display_name, status: u.status }))
    }
  },
  groups: {
    rows(_req, who, mine) {
      const rows = groupRows(who, mine)
      if (!rows) return denied('group:read')
      return rows.map((g) => ({ id: g.id, name: g.name, status: g.status }))
    }
  },
  'api-keys': {
    refs: ['user_id'],
    rows(req, who, mine) {
      const permission = mine ? 'apikey:self:manage' : 'usage:all:read'
      if (!hasPerm(who, permission)) return denied(permission)
      const owner = Number(req.query.user_id) || 0
      return keys
        .filter((k) => (!mine || k.user_id === ME_ID) && (!owner || k.user_id === owner))
        .map((k) => ({ id: k.id, name: k.name, hint: mine ? k.key_prefix : ownerEmail(k.user_id), status: k.status }))
    }
  },
  accounts: {
    refs: ['prefer_group_id'],
    rows(req, who, mine) {
      const scope = ownerScope(who, 'account:read')
      if (!scope) return denied('account:read')
      const preferred = Number(req.query.prefer_group_id) || 0
      return filterOwned(accounts, scope, who, mine ? { mine: 'true' } : {}).map((a) => ({
        id: a.id,
        name: a.name,
        hint: a.plugin_key,
        status: a.status,
        preferred: !!preferred && (a.group_ids || []).includes(preferred)
      }))
    }
  },
  models: {
    numericId: false,
    rows(_req, who, mine) {
      const permission = mine ? 'apikey:self:manage' : 'price:read'
      if (!hasPerm(who, permission)) return denied(permission)
      return pricedModels().map((model) => ({ id: model, name: model }))
    }
  },
  proxies: {
    rows(_req, who, mine) {
      const scope = ownerScope(who, 'proxy:read')
      if (!scope) return denied('proxy:read')
      return filterOwned(proxies, scope, who, mine ? { mine: 'true' } : {}).map((p) => ({
        id: p.id,
        name: p.name,
        hint: `${p.protocol}://${p.host}:${p.port}`,
        status: p.status
      }))
    }
  }
}

/**
 * The groups a lookup lists: every one to group:read; the caller's usable ones
 * to `mine` and to account:group:bind alone; otherwise those its own scopes
 * name (group/G, group/G/account/X; core PermissionSet.ScopeGroups, Entity.Named),
 * null (refused) when they name none.
 */
function groupRows(who: Identity, mine: boolean) {
  if (!mine && hasPerm(who, 'group:read')) return groups
  if (mine || hasPerm(who, 'account:group:bind')) return myGroups(who.id)
  const named = new Set(who.permissions.map((k) => parsePermission(k)?.scope.group ?? 0).filter((g) => g > 0))
  return named.size ? groups.filter((g) => named.has(g.id)) : null
}

/** Reads the parameters as the core does, with its 400s (CONTRACTS §96.1); they come before the permission. */
function parse(query: Record<string, string>, kind: Kind): Params | Failure {
  const numericId = kind.numericId !== false
  const q = (query.q || '').trim()
  if (new TextEncoder().encode(q).length > MAX_Q_BYTES) return invalid('invalid q')
  let ids: Params['ids'] = null
  if (query.ids) {
    const given = query.ids.split(',').map((v) => v.trim())
    if (given.length > MAX_IDS) return invalid(`ids accepts at most ${MAX_IDS} ids`)
    ids = given.map((v) => (numericId ? positive(v) : v) || '')
    if (ids.includes('')) return invalid('invalid ids')
  }
  const limit = query.limit ? positive(query.limit) : 20
  if (!limit || limit > MAX_LIMIT) return invalid('invalid limit')
  const mine = query.mine ? BOOLEANS[query.mine] : false
  if (mine === undefined) return invalid('invalid mine')
  const bad = (kind.refs ?? []).find((param) => query[param] && !positive(query[param]))
  return bad ? invalid(`invalid ${bad}`) : { q, ids, limit, mine }
}

/** The lookup protocol over a kind's rows: `ids` alone, else `q` (name contains, or a numeric id) in order, cut at `limit`. */
function answer(rows: Row[], { q, ids, limit }: Params, numericId: boolean) {
  const item = ({ id, name, hint, status }: Row) => ({ id, name, ...(hint ? { hint } : {}), ...(status ? { status } : {}) })
  if (ids) return { items: rows.filter((r) => ids.includes(r.id)).map(item), more: false }
  const text = q.toLowerCase()
  const id = numericId && /^\d+$/.test(q) ? Number(q) : undefined
  const rank = (r: Row) => (r.id === id ? 0 : r.preferred ? 1 : 2)
  const hits = rows
    .filter((r) => !text || r.id === id || r.name.toLowerCase().includes(text) || !!r.also?.toLowerCase().includes(text))
    .sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name) || Number(a.id) - Number(b.id))
  return { items: hits.slice(0, limit).map(item), more: hits.length > limit }
}

on('GET', '/lookup/:kind', (req) => {
  const kind = KINDS[req.params.kind]
  if (!kind) return fail(404, 'not_found', `no lookup for ${req.params.kind}`)
  const params = parse(req.query, kind)
  if ('__status' in params) return params
  const rows = kind.rows(req, caller(req), params.mine)
  return Array.isArray(rows) ? answer(rows, params, kind.numericId !== false) : rows
})
