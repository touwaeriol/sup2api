// Mock handlers of the sticky rules: built-in and admin rules, their stats,
// restoring a built-in rule's default and flushing a rule's bindings.
import { fail, nextId, noContent, on, paginate } from '../router'

const stickyRules: any[] = [
  {
    id: 1,
    name: 'claude-code-session',
    source: 'builtin',
    enabled: true,
    priority: 100,
    match: { protocols: ['anthropic.messages'], models: [], user_agent_contains: [] },
    key_sources: [{ type: 'body', path: 'metadata.user_id' }],
    value_regex: '',
    ttl_seconds: 3600,
    key_includes: ['group', 'model', 'rule'],
    on_failure: 'failover'
  },
  {
    id: 2,
    name: 'openai-prompt-cache-key',
    source: 'builtin',
    enabled: true,
    priority: 101,
    match: { protocols: ['openai.chat', 'openai.responses', 'openai.responses_ws'], models: [], user_agent_contains: [] },
    key_sources: [{ type: 'body', path: 'prompt_cache_key' }],
    value_regex: '',
    ttl_seconds: 3600,
    key_includes: ['group', 'model', 'rule'],
    on_failure: 'failover'
  },
  {
    id: 3,
    name: 'cli-users-stick',
    source: 'admin',
    enabled: true,
    priority: 200,
    match: { protocols: [], models: ['claude-sonnet-*'], user_agent_contains: ['claude-cli'] },
    key_sources: [{ type: 'user' }],
    value_regex: '',
    ttl_seconds: 1800,
    key_includes: ['group', 'model'],
    on_failure: 'stick'
  }
]
// Default definitions of the built-in rules, for "restore default".
const stickyDefaults: Record<string, any> = Object.fromEntries(
  stickyRules.filter((r) => r.source === 'builtin').map((r) => [r.name, JSON.parse(JSON.stringify(r))])
)
const stickyStats: Record<string, { hits: number; misses: number; rebinds: number }> = {
  'claude-code-session': { hits: 18234, misses: 2311, rebinds: 142 },
  'openai-prompt-cache-key': { hits: 0, misses: 0, rebinds: 0 },
  'cli-users-stick': { hits: 912, misses: 88, rebinds: 3 }
}

on('GET', '/sticky-rules/stats', () => stickyRules.map((r) => ({ rule: r.name, ...(stickyStats[r.name] || { hits: 0, misses: 0, rebinds: 0 }) })))
on('GET', '/sticky-rules', (req) => paginate(stickyRules, req.query))
on('POST', '/sticky-rules', (req) => {
  const b = req.body || {}
  if (!b.name) return fail(400, 'invalid_argument', 'invalid', { fields: [{ field: 'name', code: 'required', message: 'Name is required' }] })
  if (stickyRules.some((r) => r.name === b.name)) return fail(400, 'invalid_argument', 'invalid', { fields: [{ field: 'name', code: 'duplicate', message: 'Name already used' }] })
  if ((b.key_sources || []).some((k: any) => !['body', 'header', 'api_key', 'user'].includes(k.type))) {
    return fail(400, 'invalid_argument', 'invalid', { fields: [{ field: 'key_sources[0].type', code: 'invalid', message: 'type must be body, header, api_key or user' }] })
  }
  const rule = { ...b, id: nextId(), source: 'admin' }
  stickyRules.push(rule)
  return rule
})
on('PATCH', '/sticky-rules/:id', (req) => {
  const r = stickyRules.find((x) => x.id === Number(req.params.id))
  if (!r) return fail(404, 'not_found', 'rule not found')
  const b = req.body || {}
  if (r.source !== 'admin' && b.name !== undefined && b.name !== r.name) {
    return fail(400, 'invalid_argument', 'invalid', { fields: [{ field: 'name', code: 'invalid', message: 'built-in rules cannot be renamed' }] })
  }
  const def = (x: any) => JSON.stringify([x.match, x.key_sources, x.value_regex, x.ttl_seconds, x.key_includes, x.on_failure])
  const before = def(r)
  Object.assign(r, b)
  if (r.source !== 'admin' && def(r) !== before) r.customized = true
  return r
})
on('DELETE', '/sticky-rules/:id', (req) => {
  const i = stickyRules.findIndex((x) => x.id === Number(req.params.id))
  if (i < 0) return fail(404, 'not_found', 'rule not found')
  if (stickyRules[i].source !== 'admin') return fail(400, 'invalid_argument', 'built-in rules cannot be deleted; disable them instead')
  stickyRules.splice(i, 1)
  return noContent()
})
on('POST', '/sticky-rules/:id/reset', (req) => {
  const r = stickyRules.find((x) => x.id === Number(req.params.id))
  if (!r) return fail(404, 'not_found', 'rule not found')
  if (r.source === 'admin') return fail(400, 'invalid_argument', 'only built-in rules have a default to restore')
  const d = stickyDefaults[r.name]
  Object.assign(r, { match: d.match, key_sources: d.key_sources, value_regex: d.value_regex, ttl_seconds: d.ttl_seconds, key_includes: d.key_includes, on_failure: d.on_failure, customized: false })
  return r
})
on('POST', '/sticky-rules/:id/flush', (req) => {
  const r = stickyRules.find((x) => x.id === Number(req.params.id))
  if (!r) return fail(404, 'not_found', 'rule not found')
  if (!(r.key_includes || []).includes('rule')) return fail(409, 'conflict', 'bindings are shared with other rules and cannot be flushed on their own')
  return { deleted: Math.floor(Math.random() * 40) }
})
