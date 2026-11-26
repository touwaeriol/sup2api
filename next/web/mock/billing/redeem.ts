// Core redeem codes of the mock (Finance > Redeem codes): GET/POST
// /redeem-codes, DELETE /redeem-codes/:id voids an unused one, and
// POST /me/redeem credits the caller (any code of 8 or more characters).
import { fail, noContent, now, on, paginate } from '../router'

const mask = (code: string) => `••••-••••-••••-${code.slice(-4)}`
const newCode = () => Array.from({ length: 4 }, () => Math.random().toString(36).slice(2, 6).toUpperCase().padEnd(4, 'X')).join('-')
const statusAt = (i: number) => (i < 3 ? 'used' : i === 4 ? 'expired' : i === 5 ? 'voided' : 'unused')

const codes: any[] = Array.from({ length: 14 }, (_, i) => {
  const status = statusAt(i)
  const used = status === 'used'
  return {
    id: 300 - i, code: mask(newCode()), value: `${[10, 50, 100][i % 3]}.00000000`, status,
    used_by: used ? 2 : undefined, used_by_email: used ? 'alice@example.com' : undefined, used_at: used ? now(-86400 * i) : undefined,
    expires_at: i % 4 === 0 ? now(i === 4 ? -86400 : 86400 * 30) : undefined, notes: i < 6 ? '双十一活动' : '', created_at: now(-86400 * (i + 2))
  }
})

on('GET', '/redeem-codes', (req) => paginate(codes.filter((c) => !req.query.status || c.status === req.query.status), req.query))

on('POST', '/redeem-codes', (req) => {
  const count = Math.min(1000, Math.max(1, Number(req.body?.count) || 1))
  const plain = Array.from({ length: count }, newCode)
  const value = `${Number(req.body?.value).toFixed(8)}`
  plain.forEach((c, i) => codes.unshift({ id: 400 + codes.length + i, code: mask(c), value, status: 'unused', expires_at: req.body?.expires_at, notes: req.body?.notes || '', created_at: now() }))
  return { codes: plain }
})

on('DELETE', '/redeem-codes/:id', (req) => {
  const c = codes.find((x) => x.id === Number(req.params.id))
  if (!c || c.status !== 'unused') return fail(409, 'conflict', '只能作废未使用的兑换码')
  c.status = 'voided'
  return noContent()
})

on('POST', '/me/redeem', (req) => (String(req.body?.code || '').trim().length >= 8 ? { amount: '50.00000000' } : fail(400, 'invalid_argument', '兑换码无效或已使用')))
