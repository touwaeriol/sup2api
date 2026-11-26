// Core redeem codes of the mock, shaped as the core's (CONTRACTS §140):
// GET/POST /redeem-codes, DELETE /redeem-codes/:id voids an unused one, and
// POST /me/redeem credits the caller. Listed codes carry their last 4
// characters only; the plain codes are in the generate answer once. Any
// well-formed code (32 hex digits, case, spaces and hyphens ignored) that is
// not on the list redeems for 50, as a code generated elsewhere.
import { fail, noContent, now, on, paginate } from '../router'
import { myWallet } from '../core'

const HEX = '0123456789ABCDEF'
const newCode = () => Array.from({ length: 32 }, () => HEX[Math.floor(Math.random() * 16)]).join('')
const display = (code: string) => code.match(/.{4}/g)!.join('-')
const normalize = (raw: string) => raw.replace(/[\s-]/g, '').toUpperCase()
const statusAt = (i: number) => (i < 3 ? 'used' : i === 5 ? 'voided' : 'unused')

/** Each code with its plain form, which only the mock keeps (to redeem it). */
const codes: any[] = Array.from({ length: 14 }, (_, i) => {
  const plain = newCode()
  const used = statusAt(i) === 'used'
  return {
    plain, id: 300 - i, code_tail: plain.slice(-4), value: `${[10, 50, 100][i % 3]}.00000000`, status: statusAt(i),
    used_by: used ? 2 : undefined, used_by_email: used ? 'alice@example.com' : undefined, used_at: used ? now(-86400 * i) : undefined,
    expires_at: i % 4 === 0 ? now(i === 4 ? -86400 : 86400 * 30) : undefined, notes: i < 6 ? '双十一活动' : '', created_at: now(-86400 * (i + 2))
  }
})

/** The status the core reports: an unused code past its expiry is expired. */
const reported = (c: any) => (c.status === 'unused' && c.expires_at && Date.parse(c.expires_at) <= Date.now() ? 'expired' : c.status)
const row = ({ plain: _plain, ...c }: any) => ({ ...c, status: reported(c) })

on('GET', '/redeem-codes', (req) => {
  const status = req.query.status
  if (status && !['unused', 'used', 'voided', 'expired'].includes(status)) return fail(400, 'invalid_argument', 'invalid status')
  return paginate(codes.map(row).filter((c) => !status || c.status === status), req.query)
})

function generateErrors(b: any) {
  const fields: Array<{ field: string; code: string; message: string }> = []
  const bad = (field: string, message: string) => fields.push({ field, code: 'invalid', message })
  const count = Number(b?.count)
  const value = Number(b?.value)
  if (!Number.isInteger(count) || count < 1 || count > 1000) bad('count', '数量为 1–1000')
  if (!(value > 0) || value > 100000) bad('value', '须大于 0、不超过 100000，最多 8 位小数')
  if (b?.expires_at && Date.parse(b.expires_at) <= Date.now()) bad('expires_at', '须晚于当前时间')
  if (String(b?.notes || '').length > 200) bad('notes', '最多 200 个字符')
  return fields
}

on('POST', '/redeem-codes', (req) => {
  const fields = generateErrors(req.body)
  if (fields.length) return fail(400, 'invalid_argument', 'invalid argument', { fields })
  const plain = Array.from({ length: Number(req.body.count) }, newCode)
  const value = Number(req.body.value).toFixed(8)
  const next = Math.max(...codes.map((c) => c.id)) + 1
  plain.forEach((p, i) => codes.unshift({ plain: p, id: next + i, code_tail: p.slice(-4), value, status: 'unused', expires_at: req.body.expires_at, notes: req.body.notes || '', created_at: now() }))
  return { __status: 201, body: { data: { codes: plain.map(display) } } }
})

on('DELETE', '/redeem-codes/:id', (req) => {
  const c = codes.find((x) => x.id === Number(req.params.id))
  if (!c) return fail(404, 'not_found', 'not found')
  if (c.status !== 'unused') return fail(409, 'conflict', '只能作废未使用的兑换码')
  c.status = 'voided'
  return noContent()
})

on('POST', '/me/redeem', (req) => {
  const code = normalize(String(req.body?.code || ''))
  if (!code) return fail(400, 'invalid_argument', 'invalid argument', { fields: [{ field: 'code', code: 'required', message: '请输入兑换码' }] })
  const c = codes.find((x) => x.plain === code)
  const usable = c ? reported(c) === 'unused' : /^[0-9A-F]{32}$/.test(code)
  if (!usable) return fail(400, 'invalid_redeem_code', '兑换码无效或已使用')
  const amount = c ? Number(c.value) : 50
  if (c) Object.assign(c, { status: 'used', used_by: 1, used_by_email: 'admin@example.com', used_at: now() })
  myWallet.balance += amount
  return { amount: amount.toFixed(8), balance: myWallet.balance.toFixed(8) }
})
