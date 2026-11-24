// Mock of the plugin store's channels (CONTRACTS §136): each is a branch of
// the official store repository, whose index CI publishes to index-<branch>;
// seeded with 正式 (main, default) and 测试 (develop). The default channel
// cannot be disabled or deleted.
import { fail, noContent, on } from './router'
import { invalid } from './common'
import type { Any } from './plugins'

const BASE = 'https://raw.githubusercontent.com/touwaeriol/sup2api-plugin'
const BRANCH = /^[A-Za-z0-9._-]{1,64}$/

interface Channel {
  id: number
  name: string
  branch: string
  enabled: boolean
  is_default: boolean
}

let seq = 2
const channels: Channel[] = [
  { id: 1, name: '正式', branch: 'main', enabled: true, is_default: true },
  { id: 2, name: '测试', branch: 'develop', enabled: true, is_default: false }
]

const view = (c: Channel) => ({ ...c, url: `${BASE}/index-${c.branch}/index.json` })

/** The channel a listing, review or install names; the default one when it names none. */
export function channelOf(sourceId: unknown): Channel | undefined {
  const id = Number(sourceId)
  return id ? channels.find(c => c.id === id) : channels.find(c => c.is_default)
}

function create(body: Any): Any {
  const name = String(body?.name ?? '').trim()
  const branch = String(body?.branch ?? '').trim()
  if (!name) return invalid('name', 'name is required', 'required')
  if (!BRANCH.test(branch) || branch.includes('..')) return invalid('branch', 'not a branch name')
  if (channels.some(c => c.branch === branch)) return fail(409, 'conflict', `a channel already reads branch ${branch}`)
  const c = { id: ++seq, name, branch, enabled: true, is_default: false }
  channels.push(c)
  return view(c)
}

function update(c: Channel, body: Any): Any {
  if (body?.enabled === false && c.is_default) return fail(409, 'conflict', 'the default channel cannot be disabled')
  if (body?.is_default === true && !(body?.enabled ?? c.enabled)) return fail(409, 'conflict', 'a disabled channel cannot be the default')
  if (typeof body?.name === 'string' && body.name.trim()) c.name = body.name.trim()
  if (typeof body?.enabled === 'boolean') c.enabled = body.enabled
  if (body?.is_default === true) channels.forEach(x => (x.is_default = x === c))
  return view(c)
}

const found = (id: string, run: (c: Channel) => Any) => {
  const c = channels.find(x => x.id === Number(id))
  return c ? run(c) : fail(404, 'not_found', `channel ${id} not found`)
}

on('GET', '/market/sources', () => channels.map(view))
on('POST', '/market/sources', req => create(req.body))
on('PATCH', '/market/sources/:id', req => found(req.params.id, c => update(c, req.body)))
on('DELETE', '/market/sources/:id', req =>
  found(req.params.id, c => {
    if (c.is_default) return fail(409, 'conflict', 'the default channel cannot be deleted')
    channels.splice(channels.indexOf(c), 1)
    return noContent()
  })
)
