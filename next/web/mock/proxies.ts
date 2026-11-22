// Mock proxies: CRUD within the caller's owner scope (CONTRACTS §21), and
// the proxy_url find-or-create of the accounts mock (§21.4).
import { fail, nextId, noContent, now, on, paginate, type MockRequest } from './router'
import { caller, filterOwned, hasPerm, inScope, ownerScope, stepUp, userEmail, vendorUser } from './core'
import { proxyAccountCount } from './accounts'
import { invalid } from './common'

interface MockProxy {
  id: number
  name: string
  protocol: 'http' | 'https' | 'socks5'
  host: string
  port: number
  username: string
  password: string
  status: string
  /** Owner (CONTRACTS §21.2); null for rows created before ownership existed. */
  created_by: number | null
  created_at: string
}

export const proxies: MockProxy[] = [
  { id: 1, name: 'us-west-1', protocol: 'http', host: '10.0.1.20', port: 3128, username: '', password: '', status: 'active', created_by: 1, created_at: now(-86400 * 40) },
  { id: 2, name: 'jp-residential', protocol: 'socks5', host: 'jp.proxy.example.net', port: 1080, username: 'acme', password: 's3cret', status: 'active', created_by: vendorUser.id, created_at: now(-86400 * 15) },
  { id: 3, name: 'eu-backup', protocol: 'https', host: 'eu.proxy.example.net', port: 443, username: 'backup', password: 'pw', status: 'disabled', created_by: null, created_at: now(-86400 * 3) }
]

// CONTRACTS §15.4: the password is never returned, only has_password.
function proxyOut(p: MockProxy) {
  const { password, ...rest } = p
  return { ...rest, has_password: !!password, created_by_email: userEmail(p.created_by), account_count: proxyAccountCount(p.id) }
}

function validateProxy(b: any, partial: boolean) {
  if ((!partial || b.name !== undefined) && !String(b.name || '').trim()) return invalid('name', 'name is required')
  if ((!partial || b.protocol !== undefined) && !['http', 'https', 'socks5'].includes(b.protocol)) return invalid('protocol', 'invalid protocol')
  if ((!partial || b.host !== undefined) && !String(b.host || '').trim()) return invalid('host', 'host is required')
  if ((!partial || b.port !== undefined) && !(Number(b.port) >= 1 && Number(b.port) <= 65535)) return invalid('port', 'port out of range')
  return null
}

/** The caller's reach for a proxy action: proxy:read or proxy:manage at any scope. */
function proxyScope(req: MockRequest, action: 'read' | 'manage') {
  const who = caller(req)
  return { who, scope: ownerScope(who, `proxy:${action}`) }
}

/** The proxy of :id within the caller's manage scope: 403 without proxy:manage at any scope, 404 when missing or out of it. */
function managedProxy(req: MockRequest): { p: MockProxy } | { __status: number; body: unknown } {
  const { who, scope } = proxyScope(req, 'manage')
  if (!scope) return fail(403, 'permission_denied', 'proxy:manage is required', { permission: 'proxy:manage' })
  const p = proxies.find((x) => x.id === Number(req.params.id))
  return p && inScope(scope, who, p) ? { p } : fail(404, 'not_found', 'proxy not found')
}

// CONTRACTS §21.4: proxy.ParseURL. scheme://user:pass@host:port, scheme
// http / https / socks5 / socks5h (-> socks5); no path, query or fragment.
interface ProxySpec {
  protocol: MockProxy['protocol']
  host: string
  port: number
  username: string
  password: string
}
const PROXY_SCHEMES: Record<string, MockProxy['protocol']> = { http: 'http', https: 'https', socks5: 'socks5', socks5h: 'socks5' }

function parseProxyURL(raw: string): ProxySpec | null {
  const s = String(raw).trim()
  const m = /^([a-z0-9+.-]+):\/\//i.exec(s)
  const protocol = m ? PROXY_SCHEMES[m[1].toLowerCase()] : undefined
  if (!m || !protocol) return null
  let u: URL
  try {
    u = new URL('proxy-url://' + s.slice(m[0].length))
  } catch {
    return null
  }
  if ((u.pathname && u.pathname !== '/') || u.search || u.hash) return null
  const host = u.hostname.replace(/^\[(.*)\]$/, '$1').toLowerCase()
  const port = Number(u.port)
  if (!host || !u.port || !Number.isInteger(port) || port < 1 || port > 65535) return null
  const dec = (x: string) => {
    try {
      return decodeURIComponent(x)
    } catch {
      return x
    }
  }
  return { protocol, host, port, username: dec(u.username), password: dec(u.password) }
}

/** 400 invalid on a bad proxy_url (the message never echoes the string: it may hold a password); null when fine. */
export function validateProxyURL(raw: string) {
  return parseProxyURL(raw) ? null : fail(400, 'invalid_argument', 'invalid proxy_url', { fields: [{ field: 'proxy_url', code: 'invalid', message: 'Expected scheme://user:pass@host:port with http, https, socks5 or socks5h' }] })
}

/**
 * CONTRACTS §21.4 find-or-create for POST/PATCH /accounts proxy_url: needs
 * proxy:manage at any scope, and its own scope or more to create one; matches protocol/host/port/username/
 * password among the proxies the caller can see (skipping disabled ones) or
 * creates one owned by the caller.
 */
export function resolveProxyURL(req: MockRequest, raw: string): { id: number; created: boolean } | { __status: number; body: unknown } {
  const who = caller(req)
  const reach = ownerScope(who, 'proxy:manage')
  if (!reach) return fail(403, 'permission_denied', 'proxy:manage is required to attach a proxy by URL', { permission: 'proxy:manage:own' })
  const spec = parseProxyURL(raw)
  if (!spec) return validateProxyURL(raw)!
  const hit = proxies
    .filter((p) => inScope(reach, who, p) && p.status !== 'disabled')
    .filter((p) => p.protocol === spec.protocol && p.host.toLowerCase() === spec.host && p.port === spec.port && p.username === spec.username && p.password === spec.password)
    .sort((a, b) => a.id - b.id)[0]
  if (hit) return { id: hit.id, created: false }
  // Creating one takes the own scope or more: proxy/<id> reaches existing proxies only.
  if (!hasPerm(who, 'proxy:manage:own')) {
    return fail(403, 'permission_denied', 'creating a proxy requires proxy:manage for every proxy or your own', { permission: 'proxy:manage:own' })
  }
  const p: MockProxy = {
    id: nextId(),
    name: `${spec.protocol}://${spec.host}:${spec.port}`.slice(0, 100),
    protocol: spec.protocol,
    host: spec.host,
    port: spec.port,
    username: spec.username,
    password: spec.password,
    status: 'active',
    created_by: who.id,
    created_at: now()
  }
  proxies.push(p)
  return { id: p.id, created: true }
}

on('GET', '/proxies', (req) => {
  const { who, scope } = proxyScope(req, 'read')
  return paginate(filterOwned(proxies, scope, who, req.query).map(proxyOut), req.query)
})
on('GET', '/proxies/:id', (req) => {
  const { who, scope } = proxyScope(req, 'read')
  const p = proxies.find((x) => x.id === Number(req.params.id))
  return p && inScope(scope, who, p) ? proxyOut(p) : fail(404, 'not_found', 'proxy not found')
})
on('POST', '/proxies', (req) => {
  const b = req.body || {}
  // proxy/<id> scopes reach existing proxies only (CONTRACTS §21).
  if (!hasPerm(caller(req), 'proxy:manage:own')) {
    return fail(403, 'permission_denied', 'creating a proxy requires proxy:manage for every proxy or your own', { permission: 'proxy:manage:own' })
  }
  const err = validateProxy(b, false)
  if (err) return err
  const p: MockProxy = { id: nextId(), name: b.name, protocol: b.protocol, host: b.host, port: Number(b.port), username: b.username || '', password: b.password || '', status: 'active', created_by: caller(req).id, created_at: now() }
  proxies.push(p)
  return proxyOut(p)
})
on('PATCH', '/proxies/:id', (req) => {
  const r = managedProxy(req)
  if (!('p' in r)) return r
  const p = r.p
  const b = req.body || {}
  const err = validateProxy(b, true)
  if (err) return err
  for (const k of ['name', 'protocol', 'host', 'username', 'status'] as const) if (b[k] !== undefined) (p as any)[k] = b[k]
  if (b.port !== undefined) p.port = Number(b.port)
  // No mask convention: omitted / null keeps, "" clears, any other string is the new password.
  if (b.password !== undefined && b.password !== null) p.password = String(b.password)
  return proxyOut(p)
})
on('DELETE', '/proxies/:id', (req) => {
  const r = managedProxy(req)
  if (!('p' in r)) return r
  const p = r.p
  // Still referenced: 409 with the count over every account (§21.2), whoever owns them.
  const n = proxyAccountCount(p.id)
  if (n) return fail(409, 'conflict', 'proxy is used by accounts', { account_count: n })
  proxies.splice(proxies.indexOf(p), 1)
  return noContent()
})
on('POST', '/proxies/:id/reveal-url', stepUp('proxy.reveal', (req) => {
  const r = managedProxy(req)
  if (!('p' in r)) return r
  const p = r.p
  const host = p.host.includes(':') ? `[${p.host}]` : p.host
  const cred = p.username || p.password ? `${encodeURIComponent(p.username)}:${encodeURIComponent(p.password)}@` : ''
  return { url: `${p.protocol}://${cred}${host}:${p.port}` }
}))
on('POST', '/proxies/:id/test', async (req) => {
  const r = managedProxy(req)
  if (!('p' in r)) return r
  const p = r.p
  await new Promise((r) => setTimeout(r, 400 + Math.random() * 600))
  if (p.status !== 'active' || p.host.startsWith('eu.')) return { ok: false, latency_ms: 5000, message: 'dial tcp: i/o timeout' }
  return { ok: true, latency_ms: Math.round(80 + Math.random() * 300), ip: p.host.startsWith('jp') ? '203.0.113.' + Math.floor(Math.random() * 250) : '198.51.100.7', message: 'HTTP 200 from https://api.ipify.org' }
})
