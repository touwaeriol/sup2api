// Plugin network access (CONTRACTS §113): GET/PUT /settings/plugin-egress.
// Entries are checked with the console's own normalizeDomain, which mirrors
// the server's rules.
import { fail, on } from './router'
import { caller, hasPerm } from './core'
import { normalizeDomain } from '../src/views/settings/pluginEgress'

const policy = {
  mode: 'allow_all',
  blocked_domains: ['tracker.example.com', 'ads.example.net'],
  allowed_domains: ['api.anthropic.com', 'smtp.example.com']
}

on('GET', '/settings/plugin-egress', (req) =>
  hasPerm(caller(req), 'settings:read') ? policy : fail(403, 'permission_denied', 'permission denied'))

on('PUT', '/settings/plugin-egress', (req) => {
  if (!hasPerm(caller(req), 'settings:manage')) return fail(403, 'permission_denied', 'permission denied')
  const body = req.body || {}
  const fields: Array<{ field: string; code: string; message: string }> = []
  if (body.mode !== undefined && body.mode !== 'allow_all' && body.mode !== 'block_all') {
    fields.push({ field: 'mode', code: 'invalid', message: 'must be allow_all or block_all' })
  }
  const lists: Record<string, string[]> = {}
  for (const key of ['blocked_domains', 'allowed_domains']) {
    if (body[key] === undefined) continue
    const out: string[] = []
    ;(body[key] as string[]).forEach((entry, i) => {
      const d = normalizeDomain(String(entry))
      if (!d) fields.push({ field: `${key}.${i}`, code: 'invalid', message: `"${entry}" is not a domain name or IP address` })
      else if (!out.includes(d)) out.push(d)
    })
    lists[key] = out
  }
  if (fields.length) return fail(400, 'invalid_argument', 'invalid plugin network access', { fields })
  Object.assign(policy, body.mode ? { mode: body.mode } : {}, lists)
  return policy
})
