import { readFileSync, existsSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { join, normalize } from 'node:path'
import { fail, noContent, now, on, paginate } from './router'
import { grant, plugins } from './plugins'

// Mock of the payment plugin (/p/payment/*, CONTRACTS §46) and its native UI
// from plugins/payment/ui/native/dist (build it first). Settings, orders and
// codes live in memory. Admin pages sit in the finance menu, the top-up card
// in the wallet (slot wallet.topup), the settings on the plugin's settings tab.

const DIST = fileURLToPath(new URL('../../plugins/payment/ui/native/dist/', import.meta.url))
const VERSION = '0.3.0'
const L = (en: string, zh: string) => ({ en, zh })

export const paymentUIPlugin = {
  key: 'payment',
  version: VERSION,
  name: L('Payment & Recharge', '充值支付'),
  asset_base: `/plugin-ui/payment/${VERSION}-dev`,
  menus: [
    { id: 'orders', section: 'finance', label: L('Top-up orders', '充值订单'), page: 'orders', icon: 'balance' },
    { id: 'redeem', section: 'finance', label: L('Redeem codes', '兑换码'), page: 'redeem', icon: 'key' },
    { id: 'promo', section: 'finance', label: L('Promo codes', '优惠码'), page: 'promo', icon: 'price' }
  ],
  pages: {
    orders: { type: 'native', title: L('Top-up orders', '充值订单'), component: 'PaymentOrders' },
    redeem: { type: 'native', title: L('Redeem codes', '兑换码'), component: 'PaymentRedeemCodes' },
    promo: { type: 'native', title: L('Promo codes', '优惠码'), component: 'PaymentPromoCodes' }
  },
  slots: [{ slot: 'wallet.topup', component: 'WalletTopup', permission: 'recharge:use' }],
  native_entry: 'ui/native/entry.js',
  trust: 'official',
  host_ui_compat: '^4.0'
}

export function paymentAsset(path: string): { type: string; body: Buffer } | null {
  if (!path.startsWith('ui/native/')) return null
  const rel = normalize(path.slice('ui/native/'.length))
  if (rel.startsWith('..')) return null
  const file = join(DIST, rel)
  if (!existsSync(file)) return null
  return { type: rel.endsWith('.css') ? 'text/css' : 'text/javascript', body: readFileSync(file) }
}

const SECRETS = ['epay/key', 'stripe/secret_key', 'stripe/webhook_secret']

const settings = {
  enabled: true,
  currency: 'CNY',
  price: 7.3,
  min_topup: 1,
  max_topup: 10000,
  order_timeout_minutes: 30,
  max_pending_orders: 3,
  amount_options: [10, 20, 50, 100, 200, 500],
  discounts: [
    { amount: 100, rate: 0.95 },
    { amount: 500, rate: 0.9 }
  ],
  methods: [
    { type: 'alipay', name: '支付宝', gateway: 'epay', icon: 'alipay', color: '#1677ff', min_topup: 0, enabled: true },
    { type: 'wxpay', name: '微信支付', gateway: 'epay', icon: 'wxpay', color: '#07c160', min_topup: 0, enabled: true },
    { type: 'stripe', name: 'Stripe', gateway: 'stripe', icon: 'stripe', color: '#635bff', min_topup: 5, enabled: false }
  ],
  epay: { enabled: true, endpoint: 'https://pay.example.com', merchant_id: '1001', key: '******' },
  stripe: { enabled: false, secret_key: '', webhook_secret: '', allow_promotion_codes: false }
}

plugins.set('payment', {
  key: 'payment',
  name: paymentUIPlugin.name,
  description: L('Online top-up, redeem codes and promo codes.', '在线充值订单、兑换码与优惠码，余额通过核心账本入账。'),
  status: 'enabled',
  status_reason: '',
  active_version: VERSION,
  desired_version: VERSION,
  publisher: 'sub2api',
  trust: 'official',
  egress_policy: 'allow_all',
  resources: { memory_mb: 128, cpu: 0.25, max_threads: 64, max_open_files: 256 },
  versions: [{ version: VERSION, consent_status: 'approved' }],
  grants: ['db.schema', 'routes.user', 'routes.admin', 'routes.webhook', 'ui.native'].map((p) => grant(p, null, VERSION)),
  manifest: { key: 'payment', version: VERSION, name: paymentUIPlugin.name, ui: { native: { entry: 'ui/native/entry.js' }, settings: { mode: 'native', component: 'PaymentSettings', secrets: SECRETS.map((path) => ({ path })) } } },
  settings: { mode: 'native', component: 'PaymentSettings', secret_fields: SECRETS, values: settings }
} as any)

// ---- orders ----

const STATUSES = ['completed', 'completed', 'completed', 'pending', 'completed', 'expired', 'failed', 'completed', 'cancelled', 'completed']
const EMAILS = ['admin@example.com', 'alice@example.com', 'bob@example.com', 'carol@example.com']
const AMOUNTS = [10, 20, 50, 100, 200, 500]

function seedOrder(i: number): any {
  const status = STATUSES[i % STATUSES.length]
  const amount = AMOUNTS[i % AMOUNTS.length]
  const method = settings.methods[i % 2]
  const created = -3600 * (i * 5 + 1)
  return {
    id: 500 - i,
    out_trade_no: `s2a_${(0x5a3f00 + i * 7919).toString(16)}e1c4b2`,
    user_id: (i % 4) + 1,
    user_email: EMAILS[i % 4],
    amount,
    bonus: i % 7 === 0 ? 10 : 0,
    pay_amount: Math.round(amount * settings.price * rateOf(amount) * 100) / 100,
    currency: settings.currency,
    method: method.type,
    method_name: method.name,
    gateway: method.gateway,
    promo_code: i % 7 === 0 ? 'WELCOME10' : undefined,
    status,
    trade_no: status === 'completed' ? `2026112522001${400000 + i}` : undefined,
    failed_reason: status === 'failed' ? 'ledger credit rejected: daily limit reached' : undefined,
    created_at: now(created),
    paid_at: status === 'completed' ? now(created + 40) : undefined,
    completed_at: status === 'completed' ? now(created + 41) : undefined,
    expires_at: now(created + 1800)
  }
}

const orders: any[] = Array.from({ length: 36 }, (_, i) => seedOrder(i))

function rateOf(amount: number): number {
  return settings.discounts.filter((d) => amount >= d.amount).reduce((r, d) => Math.min(r, d.rate), 1)
}

function preview(body: any) {
  const amount = Number(body?.amount)
  const promo = String(body?.promo_code || '').trim().toUpperCase()
  const original = Math.round(amount * settings.price * 100) / 100
  const rate = rateOf(amount)
  const bonus = promo === 'WELCOME10' ? 10 : 0
  return { amount, bonus, discount_rate: rate, pay_amount: Math.round(original * rate * 100) / 100, original_pay_amount: original, promo_error: promo && !bonus ? '优惠码不存在或已用完' : undefined }
}

function checkAmount(body: any) {
  const amount = Number(body?.amount)
  const method = settings.methods.find((m) => m.type === body?.method && m.enabled)
  if (!method) return fail(400, 'invalid_argument', '支付方式不可用')
  const min = Math.max(settings.min_topup, method.min_topup || 0)
  if (!(amount >= min && amount <= settings.max_topup)) return fail(400, 'invalid_argument', `充值金额须在 ${min} 到 ${settings.max_topup} 之间`)
  return method
}

on('GET', '/p/payment/checkout', () => {
  const methods = settings.methods.filter((m) => m.enabled).map(({ type, name, icon, color, min_topup }) => ({ type, name, icon, color, min_topup }))
  const { enabled, currency, price, min_topup, max_topup, amount_options, discounts } = settings
  return { enabled, currency, price, min_topup, max_topup, amount_options, discounts, methods }
})

on('POST', '/p/payment/orders/preview', (req) => preview(req.body))

on('POST', '/p/payment/orders', (req) => {
  const method: any = checkAmount(req.body)
  if (method.__status) return method
  const p = preview(req.body)
  const order = { ...seedOrder(0), id: 501 + orders.length, out_trade_no: `s2a_${Date.now().toString(16)}`, user_id: 1, user_email: EMAILS[0], amount: p.amount, bonus: p.bonus, pay_amount: p.pay_amount, method: method.type, method_name: method.name, gateway: method.gateway, promo_code: req.body?.promo_code || undefined, status: 'pending', trade_no: undefined, paid_at: undefined, completed_at: undefined, created_at: now(), expires_at: now(settings.order_timeout_minutes * 60) }
  // Kept on the order while pending, so the history can continue paying it.
  Object.assign(order, { pay_url: `https://pay.example.com/submit.php?out_trade_no=${order.out_trade_no}`, qr_code: `weixin://wxpay/bizpayurl?pr=${order.out_trade_no}` })
  orders.unshift(order)
  return { out_trade_no: order.out_trade_no, pay_url: order.pay_url, qr_code: order.qr_code, expires_at: order.expires_at }
})

function filterOrders(query: Record<string, string>, base: any[]) {
  const q = (query.q || '').toLowerCase()
  return base.filter((o) => (!query.status || o.status === query.status) && (!query.method || o.method === query.method) && (!q || o.out_trade_no.includes(q) || String(o.user_email).includes(q)))
}

on('GET', '/p/payment/orders', (req) => paginate(filterOrders(req.query, orders.filter((o) => o.user_id === 1)), req.query))

on('GET', '/p/payment/orders/:no', (req) => orders.find((o) => o.out_trade_no === req.params.no) ?? fail(404, 'not_found', '订单不存在'))

on('POST', '/p/payment/orders/:no/cancel', (req) => {
  const o = orders.find((x) => x.out_trade_no === req.params.no)
  if (!o || o.status !== 'pending') return fail(409, 'conflict', '只能取消待支付的订单')
  o.status = 'cancelled'
  return o
})

on('GET', '/p/payment/admin/orders', (req) => paginate(filterOrders(req.query, orders), req.query))

on('GET', '/p/payment/admin/orders/stats', () => {
  const done = orders.filter((o) => o.status === 'completed')
  const sum = (xs: any[]) => xs.reduce((s, o) => s + o.amount, 0)
  return { today_amount: sum(done.slice(0, 4)), today_count: 4, month_amount: sum(done), pending_count: orders.filter((o) => o.status === 'pending').length, failed_count: orders.filter((o) => o.status === 'failed').length }
})

on('POST', '/p/payment/admin/orders/:id/complete', (req) => {
  const o = orders.find((x) => x.id === Number(req.params.id))
  if (!o || !['pending', 'expired', 'failed'].includes(o.status)) return fail(409, 'conflict', '这个订单不能补单')
  Object.assign(o, { status: 'completed', paid_at: now(), completed_at: now(), trade_no: o.trade_no || 'manual' })
  return o
})

// ---- redeem codes ----

const mask = (code: string) => `${code.slice(0, 4)}••••••••${code.slice(-4)}`
const newCode = () => Array.from({ length: 4 }, () => Math.random().toString(36).slice(2, 6).toUpperCase().padEnd(4, 'X')).join('-')

const redeemCodes: any[] = Array.from({ length: 14 }, (_, i) => {
  const status = i < 3 ? 'used' : i === 4 ? 'expired' : 'unused'
  return { id: 300 - i, code: mask(newCode()), value: [10, 50, 100][i % 3], status, used_by: status === 'used' ? 2 : undefined, used_by_email: status === 'used' ? 'alice@example.com' : undefined, used_at: status === 'used' ? now(-86400 * i) : undefined, expires_at: i % 4 === 0 ? now(86400 * 30) : undefined, notes: i < 6 ? '双十一活动' : '', created_at: now(-86400 * (i + 2)) }
})

on('GET', '/p/payment/redeem-codes', (req) => paginate(redeemCodes.filter((c) => !req.query.status || c.status === req.query.status), req.query))

on('POST', '/p/payment/redeem-codes', (req) => {
  const count = Math.min(1000, Math.max(1, Number(req.body?.count) || 1))
  const codes = Array.from({ length: count }, newCode)
  codes.forEach((c, i) => redeemCodes.unshift({ id: 400 + redeemCodes.length + i, code: mask(c), value: Number(req.body?.value), status: 'unused', expires_at: req.body?.expires_at, notes: req.body?.notes || '', created_at: now() }))
  return { codes }
})

on('DELETE', '/p/payment/redeem-codes/:id', (req) => {
  const c = redeemCodes.find((x) => x.id === Number(req.params.id))
  if (!c || c.status !== 'unused') return fail(409, 'conflict', '只能作废未使用的兑换码')
  c.status = 'expired'
  return noContent()
})

on('POST', '/p/payment/redeem', (req) => (String(req.body?.code || '').trim().length >= 8 ? { amount: 50 } : fail(400, 'invalid_argument', '兑换码无效或已使用')))

// ---- promo codes ----

const promoCodes: any[] = [
  { id: 3, code: 'WELCOME10', bonus_amount: 10, max_uses: 500, used_count: 132, status: 'active', expires_at: now(86400 * 20), notes: '新用户首充', created_at: now(-86400 * 10) },
  { id: 2, code: 'DOUBLE11', bonus_amount: 20, max_uses: 100, used_count: 100, status: 'active', expires_at: now(-86400), notes: '双十一', created_at: now(-86400 * 20) },
  { id: 1, code: 'VIP2026', bonus_amount: 50, max_uses: 0, used_count: 8, status: 'disabled', notes: '', created_at: now(-86400 * 40) }
]

on('GET', '/p/payment/promo-codes', (req) => paginate(promoCodes, req.query))

on('POST', '/p/payment/promo-codes', (req) => {
  const p = { id: 10 + promoCodes.length, used_count: 0, status: 'active', created_at: now(), ...req.body, code: String(req.body?.code || newCode()).toUpperCase() }
  promoCodes.unshift(p)
  return p
})

on('PATCH', '/p/payment/promo-codes/:id', (req) => {
  const p = promoCodes.find((x) => x.id === Number(req.params.id))
  return p ? Object.assign(p, req.body) : fail(404, 'not_found', '优惠码不存在')
})

on('DELETE', '/p/payment/promo-codes/:id', (req) => {
  const i = promoCodes.findIndex((x) => x.id === Number(req.params.id))
  if (i >= 0) promoCodes.splice(i, 1)
  return noContent()
})
