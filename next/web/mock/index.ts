// Dev-only mock of the console API (vite --mode mock). Never bundled.
// Handlers are registered by the area modules below; each handler receives
// the parsed request and returns {status?, body} or a plain value (wrapped
// as {"data": value}).

import type { Plugin } from 'vite'
import type { IncomingMessage, ServerResponse } from 'node:http'
import { randomBytes } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { routes, type MockRequest } from './router'
import { pluginAsset } from './pluginui'
import { accessTokenValid } from './core'
import './core'
import './topology'
import './volcengine'
import './accounts'
import './resources'
import './billing'
import './pricing'
import './lookup'
import './environment'
import './pluginEgress'
import './updates'
import './observability'
import './plugins'

async function readBody(req: IncomingMessage): Promise<any> {
  const chunks: Buffer[] = []
  for await (const c of req) chunks.push(c as Buffer)
  const raw = Buffer.concat(chunks)
  if (!raw.length) return undefined
  const ct = String(req.headers['content-type'] || '')
  if (ct.includes('application/json')) {
    try {
      return JSON.parse(raw.toString('utf8'))
    } catch {
      return undefined
    }
  }
  return { __raw: raw.length }
}

function send(res: ServerResponse, status: number, body: unknown) {
  res.statusCode = status
  if (body === undefined || status === 204) return res.end()
  res.setHeader('Content-Type', 'application/json')
  res.end(JSON.stringify(body))
}

export function mockApi(): Plugin {
  return {
    name: 'sub2api:mock-api',
    configureServer(server) {
      server.middlewares.use(handle)
    },
    configurePreviewServer(server) {
      server.middlewares.use(handle)
      // Emulates what the Go server must do for the SPA: inject a per-request
      // nonce into index.html and send a strict CSP header.
      const outDir = server.config.build.outDir
      server.middlewares.use((req, res, next) => {
        const url = new URL(req.url || '/', 'http://localhost')
        const accept = String(req.headers.accept || '')
        if (req.method !== 'GET' || !accept.includes('text/html') || /\.[a-z0-9]+$/i.test(url.pathname)) return next()
        const nonce = randomBytes(16).toString('base64')
        const html = readFileSync(join(outDir, 'index.html'), 'utf8').replaceAll('__CSP_NONCE__', nonce)
        res.setHeader('Content-Type', 'text/html; charset=utf-8')
        res.setHeader('Content-Security-Policy', cspHeader(nonce))
        res.end(html)
      })
    }
  }
}

/** CSP the console is designed for (recommended for the Go server). */
export function cspHeader(nonce: string): string {
  return [
    "default-src 'self'",
    `script-src 'self' 'nonce-${nonce}'`,
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' data: blob:",
    "font-src 'self' data:",
    "connect-src 'self'",
    "frame-src 'self'",
    "object-src 'none'",
    "base-uri 'self'",
    "form-action 'self'",
    "frame-ancestors 'self'"
  ].join('; ')
}

async function handle(req: IncomingMessage, res: ServerResponse, next: () => void) {
  const url = new URL(req.url || '/', 'http://localhost')
  if (url.pathname.startsWith('/plugin-ui/')) {
    const asset = pluginAsset(url.pathname)
    if (!asset) return send(res, 404, { error: { code: 'not_found', message: 'asset not found' } })
    res.statusCode = 200
    res.setHeader('Content-Type', asset.type)
    return res.end(asset.body)
  }
  if (!url.pathname.startsWith('/api/v1/')) return next()
  const path = url.pathname.slice('/api/v1'.length)
  const method = (req.method || 'GET').toUpperCase()
  if (!path.startsWith('/auth/login') && !accessTokenValid(req.headers.authorization)) {
    return send(res, 401, { error: { code: 'unauthenticated', message: 'session has ended' } })
  }
  for (const r of routes) {
    if (r.method !== method && r.method !== 'ANY') continue
    const m = r.re.exec(path)
    if (!m) continue
    const params: Record<string, string> = {}
    r.keys.forEach((k, i) => (params[k] = decodeURIComponent(m[i + 1])))
    const mreq: MockRequest = {
      method,
      path,
      params,
      query: Object.fromEntries(url.searchParams.entries()),
      body: await readBody(req),
      headers: req.headers as Record<string, string>
    }
    try {
      const out = await r.handler(mreq)
      if (out && typeof out === 'object' && '__status' in out) {
        for (const [name, value] of Object.entries((out as any).__headers || {})) res.setHeader(name, String(value))
        // __bytes: a raw (non-JSON) body, e.g. a request log file.
        if ((out as any).__bytes instanceof Uint8Array) {
          res.statusCode = (out as any).__status
          return res.end((out as any).__bytes)
        }
        return send(res, (out as any).__status, (out as any).body)
      }
      return send(res, 200, out && typeof out === 'object' && ('data' in out || 'error' in out) ? out : { data: out })
    } catch (e) {
      return send(res, 500, { error: { code: 'internal', message: String(e) } })
    }
  }
  send(res, 404, { error: { code: 'not_found', message: `mock: no handler for ${method} ${path}` } })
}
