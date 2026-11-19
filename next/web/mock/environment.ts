// Core environment variables and notifications (CONTRACTS §74.3, §74.4).
import { fail, nextId, now, on, paginate } from './router'
import { caller, hasAction, hasPerm } from './core'

const ALL = ['global', 'group', 'account']
const TASK = ['group', 'account']
const variables = [
  { key: 'relay_timeout_seconds', unit: 'seconds', min: 1, max: 86400, layers: ALL, process_env: 'SUB2API_RELAY_TIMEOUT_SECONDS',
    name: { en: 'Upstream timeout', zh: '上游整体超时' },
    help: { en: 'Longest time one upstream attempt may take, including reading a streamed response. Unset: no limit.', zh: '一次上游尝试的最长耗时，包含读取流式响应。未设置：不限。' } },
  { key: 'response_header_timeout_seconds', unit: 'seconds', min: 1, max: 86400, layers: ALL, process_env: 'SUB2API_RESPONSE_HEADER_TIMEOUT_SECONDS',
    name: { en: 'Response header timeout', zh: '响应头超时' },
    help: { en: 'Longest wait for the upstream response headers. Unset: 180 s for streams, 600 s otherwise.', zh: '等待上游响应头的最长时间。未设置：流式 180 秒，非流式 600 秒。' } },
  { key: 'streaming_idle_timeout_seconds', unit: 'seconds', min: 1, max: 86400, layers: ALL, process_env: 'SUB2API_STREAMING_IDLE_TIMEOUT_SECONDS',
    name: { en: 'Stream idle timeout', zh: '流式空闲超时' },
    help: { en: 'Longest gap between two reads of a streamed response. Unset: no limit.', zh: '流式响应两次读到数据之间的最长间隔。未设置：不限。' } },
  { key: 'task_timeout_minutes', unit: 'minutes', min: 1, max: 43200, layers: TASK, process_env: '',
    name: { en: 'Async task timeout', zh: '异步任务超时' },
    help: { en: 'How long the core keeps observing an async task (image, video). Unset: the reconcile settings.', zh: '核心观测一个异步任务（画图、视频）的最长时间。未设置：沿用对账设置。' } },
  { key: 'task_poll_max_failures', unit: 'count', min: 1, max: 1000, layers: TASK, process_env: '',
    name: { en: 'Task query failure limit', zh: '任务查询失败上限' },
    help: { en: 'Consecutive failed queries after which an async task is given up. Unset: the reconcile settings.', zh: '异步任务连续查询失败多少次后放弃。未设置：沿用对账设置。' } }
]
const environment = { values: { relay_timeout_seconds: 600 } as Record<string, number> }

// Readable by whoever edits a layer: settings, groups, accounts (own accounts included).
on('GET', '/environment/variables', (req) =>
  hasPerm(caller(req), ['settings:read', 'group:read']) || hasAction(caller(req), 'account:read')
    ? { variables, process: { streaming_idle_timeout_seconds: 300 } }
    : fail(403, 'permission_denied', 'permission denied')
)
on('GET', '/settings/environment', () => environment)
on('PUT', '/settings/environment', (req) => {
  const values: Record<string, number> = {}
  const fields: Array<{ field: string; code: string; message: string }> = []
  for (const [key, value] of Object.entries<number>(req.body?.values || {})) {
    const v = variables.find((x) => x.key === key)
    if (!v || !v.layers.includes('global')) fields.push({ field: `values.${key}`, code: 'invalid', message: 'cannot be set on this layer' })
    else if (value && (value < v.min || value > v.max)) fields.push({ field: `values.${key}`, code: 'invalid', message: `0 (follow) or ${v.min}-${v.max}` })
    else if (value) values[key] = value
  }
  if (fields.length) return fail(400, 'invalid_argument', 'invalid environment', { fields })
  environment.values = values
  return environment
})

const channels = [{ id: 'email', plugin_key: 'mailer', plugin_name: { en: 'Mailer', zh: '邮件' }, label: { en: 'Email', zh: '邮件' },
  description: { en: "Sent through the SMTP server set in the mailer's settings.", zh: '经邮件插件设置中的 SMTP 服务器发送。' }, recipient: 'email' }]
const records = [
  { id: 2, source: 'dynweight', channel: 'email', recipients: ['ops@example.com'], subject: 'Dynamic weight: Account main (#7): weight degraded 100 → 70',
    body: 'Account main (#7): weight degraded 100 → 70\nGroup: #2    Model: claude-sonnet-5', severity: 'warning', status: 'failed',
    attempts: 3, last_error: 'smtp: 550 mailbox unavailable', next_attempt_at: now(600), created_at: now(-1800), sent_at: null as string | null },
  { id: 1, source: 'core:test', channel: 'email', recipients: ['ops@example.com'], subject: 'Sub2API test notification',
    body: 'This is a test notification. The channel works.', severity: 'info', status: 'sent',
    attempts: 1, last_error: '', next_attempt_at: now(-3600), created_at: now(-3600), sent_at: now(-3590) as string | null }
]

on('GET', '/notifications/channels', () => channels)
on('GET', '/notifications', (req) =>
  paginate(records.filter((r) => (!req.query.status || r.status === req.query.status) && (!req.query.source || r.source === req.query.source)), req.query))
on('POST', '/notifications/test', (req) => {
  const b = req.body || {}
  if (!channels.some((c) => c.id === b.channel)) return fail(400, 'invalid_argument', 'invalid', { fields: [{ field: 'channel', code: 'invalid', message: 'unknown notification channel' }] })
  if (!Array.isArray(b.recipients) || !b.recipients.length) return fail(400, 'invalid_argument', 'invalid', { fields: [{ field: 'recipients', code: 'invalid', message: 'between 1 and 20 recipients' }] })
  const id = nextId()
  records.unshift({ id, source: 'core:test', channel: b.channel, recipients: b.recipients, subject: 'Sub2API test notification',
    body: 'This is a test notification. The channel works.', severity: 'info', status: 'sent', attempts: 1, last_error: '',
    next_attempt_at: now(), created_at: now(), sent_at: now() })
  return { id }
})
