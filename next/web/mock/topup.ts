import { fail, noContent, now, on, paginate } from './router'
import { grant, plugins } from './plugins'

// Mock of the core top-up (/me/topup, /topup/*, /promo-codes, CONTRACTS:
// core top-up and plugin payment gateways): settings with the built-in Epay
// and Stripe gateways, orders and promo codes in memory, and one plugin that
// adds a gateway (alipay_direct) for the gateways tab.

const L = (en: string, zh: string) => ({ en, zh })

const SECRETS = ['epay/key', 'stripe/secret_key', 'stripe/webhook_secret']

// A method with a custom logo: a small SVG stored inline as a data URL, as the settings dialog uploads it.
const USDT_SVG =
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><circle cx="16" cy="16" r="16" fill="#26a17b"/><path fill="#fff" d="M17.92 17.38c-.11 0-.69.04-1.98.04-1.03 0-1.75-.03-2-.04-3.97-.17-6.94-.87-6.94-1.7s2.97-1.52 6.94-1.7v2.7c.26.02 1 .06 2.02.06 1.22 0 1.83-.05 1.94-.06v-2.7c3.96.18 6.92.87 6.92 1.7s-2.96 1.53-6.92 1.7zm0-3.66V11.3h5.53V7.6H8.4v3.7h5.53v2.42c-4.5.2-7.88 1.1-7.88 2.17s3.38 1.96 7.88 2.17v7.75h3.99v-7.75c4.48-.21 7.86-1.1 7.86-2.17s-3.38-1.96-7.86-2.17z"/></svg>'
const USDT_LOGO = `data:image/svg+xml;base64,${Buffer.from(USDT_SVG).toString('base64')}`

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
    { type: 'usdt', name: 'USDT', gateway: 'epay', icon: '', color: '', logo: USDT_LOGO, min_topup: 0, enabled: true },
    { type: 'stripe', name: 'Stripe', gateway: 'stripe', icon: 'stripe', color: '#635bff', min_topup: 5, enabled: false }
  ],
  epay: { enabled: true, endpoint: 'https://pay.example.com', merchant_id: '1001', key: '******' },
  stripe: { enabled: false, secret_key: '', webhook_secret: '' }
}

/** Read only, beside the values: the core's site address joined with the notify route ("" on a site that is not https). */
const NOTIFY_BASE = 'https://console.example.com/api/v1/payment/notify/'

/** GET/PUT /topup/settings: the settings view, secrets masked ("******" or none keeps the stored one). */
const settingsView = () => ({ mode: 'native', values: settings, notify_base: NOTIFY_BASE, secret_fields: SECRETS, permission: 'topup:manage' })
const getPath = (o: any, path: string) => path.split('/').reduce((v, k) => v?.[k], o)
function keepSecrets(values: any) {
  for (const path of SECRETS) {
    const [g, k] = path.split('/')
    const v = values[g]?.[k]
    if (v === '******' || v === undefined) values[g] = { ...values[g], [k]: getPath(settings, path) }
    else if (v) values[g][k] = '******'
  }
  return values
}
on('GET', '/topup/settings', settingsView)
on('PUT', '/topup/settings', (req) => {
  Object.assign(settings, keepSecrets({ ...(req.body?.values || {}) }))
  return settingsView()
})

// A plugin that adds a payment gateway (manifest payments.gateways).
const GATEWAY_PLUGIN_VERSION = '0.1.0'
plugins.set('alipay_direct', {
  key: 'alipay_direct',
  name: L('Alipay direct', '支付宝官方直连'),
  description: L('Adds an Alipay gateway (official open platform API).', '提供支付宝开放平台官方接口的支付网关。'),
  status: 'enabled',
  status_reason: '',
  active_version: GATEWAY_PLUGIN_VERSION,
  desired_version: GATEWAY_PLUGIN_VERSION,
  publisher: 'example',
  trust: 'verified',
  egress_policy: 'allow_all',
  resources: { memory_mb: 64, cpu: 0.1, max_threads: 16, max_open_files: 64 },
  versions: [{ version: GATEWAY_PLUGIN_VERSION, consent_status: 'approved' }],
  grants: ['payment.gateway', 'net'].map((p) => grant(p, null, GATEWAY_PLUGIN_VERSION)),
  manifest: { key: 'alipay_direct', version: GATEWAY_PLUGIN_VERSION, name: L('Alipay direct', '支付宝官方直连'), capabilities: [{ id: 'payment.gateway.v1' }] },
  settings: { mode: 'schema', secret_fields: ['private_key'], values: { app_id: '2021000000000000', private_key: '******', sandbox: false } }
} as any)

const PLUGIN_GATEWAY = 'alipay_direct.alipay'
const inUse = (id: string) => settings.methods.filter((m) => m.gateway === id).length

on('GET', '/topup/gateways', () => [
  { id: PLUGIN_GATEWAY, plugin_key: 'alipay_direct', plugin_name: '支付宝官方直连', name: '支付宝（官方）', ready: true, methods: [{ type: 'alipay_pc', name: '电脑网站支付' }, { type: 'alipay_qr', name: '当面付扫码' }], in_use: inUse(PLUGIN_GATEWAY) }
])

// ---- orders ----

const STATUSES = ['completed', 'completed', 'completed', 'pending', 'completed', 'expired', 'failed', 'completed', 'cancelled', 'completed']
const EMAILS = ['admin@example.com', 'alice@example.com', 'bob@example.com', 'carol@example.com']
const AMOUNTS = [10, 20, 50, 100, 200, 500]

// Mostly Epay (Alipay / WeChat Pay); every sixth order went through Stripe before it was switched off.
const seedMethod = (i: number) => settings.methods[i % 6 === 5 ? 3 : i % 2]
// The gateway's transaction number: an Epay trade_no, or a Stripe PaymentIntent id.
const seedTradeNo = (gateway: string, i: number) => (gateway === 'stripe' ? `pi_3QxK${(0x9f2c1 + i * 4099).toString(36)}Lm2R` : `2026112522001${400000 + i}`)

function seedOrder(i: number): any {
  const status = STATUSES[i % STATUSES.length]
  const amount = AMOUNTS[i % AMOUNTS.length]
  const method = seedMethod(i)
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
    trade_no: status === 'completed' ? seedTradeNo(method.gateway, i) : undefined,
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
  const { bonus, error } = promoBonus(promo, amount)
  return { amount, bonus, discount_rate: rate, pay_amount: Math.round(original * rate * 100) / 100, original_pay_amount: original, promo_error: error }
}

// A usable code credits its bonus from its minimum top-up up; one message for unknown, disabled, used up or expired.
function promoBonus(code: string, amount: number): { bonus: number; error?: string } {
  if (!code) return { bonus: 0 }
  const usable = (x: any) => x.code === code && x.status === 'active' && (!x.max_uses || x.used_count < x.max_uses) && (!x.expires_at || x.expires_at > now())
  const p = promoCodes.find(usable)
  if (!p) return { bonus: 0, error: '优惠码无效或已用完' }
  if (amount < p.min_amount) return { bonus: 0, error: '充值满 $' + p.min_amount + ' 才能使用这个优惠码' }
  return { bonus: p.bonus_amount }
}

function checkAmount(body: any) {
  const amount = Number(body?.amount)
  const method = settings.methods.find((m) => m.type === body?.method && m.enabled)
  if (!method) return fail(400, 'invalid_argument', '支付方式不可用')
  const min = Math.max(settings.min_topup, method.min_topup || 0)
  if (!(amount >= min && amount <= settings.max_topup)) return fail(400, 'invalid_argument', `充值金额须在 ${min} 到 ${settings.max_topup} 之间`)
  return method
}

on('GET', '/me/topup', () => {
  const methods = settings.methods.filter((m) => m.enabled).map(({ type, name, icon, color, logo, min_topup }) => ({ type, name, icon, color, logo, min_topup }))
  const { enabled, currency, price, min_topup, max_topup, amount_options, discounts } = settings
  return { enabled, currency, price, min_topup, max_topup, amount_options, discounts, methods }
})

on('POST', '/me/topup/preview', (req) => preview(req.body))

on('POST', '/me/topup/orders', (req) => {
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
  const hit = (o: any) => [o.out_trade_no, o.trade_no, o.user_email].some((v) => String(v ?? '').toLowerCase().includes(q))
  const inRange = (o: any) => (!query.from || o.created_at >= query.from) && (!query.to || o.created_at < query.to)
  return base.filter((o) => (!query.status || o.status === query.status) && (!query.method || o.method === query.method) && (!q || hit(o)) && inRange(o))
}

// The caller's own: q finds an order number only, as the core.
on('GET', '/me/topup/orders', (req) => {
  const q = (req.query.q || '').toLowerCase()
  const mine = orders.filter((o) => o.user_id === 1 && (!q || o.out_trade_no.toLowerCase().includes(q)))
  return paginate(filterOrders({ status: req.query.status || '' }, mine), req.query)
})

// Another user's order is not found, as an unknown one.
const myOrder = (no: string) => orders.find((o) => o.out_trade_no === no && o.user_id === 1)

on('GET', '/me/topup/orders/:no', (req) => myOrder(req.params.no) ?? fail(404, 'not_found', '订单不存在'))

on('POST', '/me/topup/orders/:no/cancel', (req) => {
  const o = myOrder(req.params.no)
  if (!o) return fail(404, 'not_found', '订单不存在')
  if (o.status !== 'pending') return fail(409, 'conflict', '只能取消待支付的订单')
  o.status = 'cancelled'
  return o
})

on('GET', '/topup/orders', (req) => paginate(filterOrders(req.query, orders), req.query))

on('GET', '/topup/orders/stats', () => {
  const done = orders.filter((o) => o.status === 'completed')
  const sum = (xs: any[]) => xs.reduce((s, o) => s + o.amount, 0)
  return { today_amount: sum(done.slice(0, 4)), today_count: 4, month_amount: sum(done), pending_count: orders.filter((o) => o.status === 'pending').length, failed_count: orders.filter((o) => o.status === 'failed').length }
})

// The gateway events of an order (GET /topup/orders/:id/events), newest
// first: a paid order has its accepted notification (the first paid one also
// a mismatched and a duplicate one), others a rejected one or none.
function eventsOf(o: any): any[] {
  const minor = String(Math.round(o.pay_amount * 100))
  const detail = (extra: object = {}) => ({ out_trade_no: o.out_trade_no, trade_no: o.trade_no ?? '', status: 'paid', amount: minor, currency: o.currency, type: o.method, ...extra })
  const at = (s: number) => new Date(Date.parse(o.created_at) + s * 1000).toISOString()
  const ev = (id: number, kind: string, verified: boolean, s: number, extra?: object) => ({ id: o.id * 10 + id, gateway: o.gateway, kind, verified, detail: detail(extra), created_at: at(s) })
  if (o.status === 'failed') return [ev(1, 'SIGNATURE_INVALID', false, 30, { error: 'signature mismatch' })]
  if (o.status !== 'completed') return []
  if (o.id !== orders.find((x) => x.status === 'completed')?.id) return [ev(1, 'NOTIFY_OK', true, 40)]
  return [ev(3, 'DUPLICATE', true, 90), ev(2, 'NOTIFY_OK', true, 40), ev(1, 'PAYMENT_AMOUNT_MISMATCH', true, 35, { amount: String(Number(minor) - 1) })]
}

on('GET', '/topup/orders/:id/events', (req) => {
  const o = orders.find((x) => x.id === Number(req.params.id))
  return o ? paginate(eventsOf(o), req.query) : paginate([], req.query)
})

// ---- promo codes ----

const newCode = () => Math.random().toString(36).slice(2, 10).toUpperCase()

const promoCodes: any[] = [
  { id: 3, code: 'WELCOME10', bonus_amount: 10, min_amount: 20, max_uses: 500, used_count: 4, status: 'active', expires_at: now(86400 * 20), notes: '新用户首充', created_at: now(-86400 * 10) },
  { id: 2, code: 'DOUBLE11', bonus_amount: 20, min_amount: 100, max_uses: 100, used_count: 100, status: 'active', expires_at: now(-86400), notes: '双十一', created_at: now(-86400 * 20) },
  { id: 1, code: 'VIP2026', bonus_amount: 50, min_amount: 500, max_uses: 0, used_count: 8, status: 'disabled', notes: '', created_at: now(-86400 * 40) }
]

on('GET', '/promo-codes', (req) => paginate(promoCodes, req.query))

// Each order that carried the code is one user's use: held while unpaid, used once credited.
const usageOf = (o: any) => ({
  id: o.id,
  user_id: o.user_id,
  user_email: o.user_email,
  out_trade_no: o.out_trade_no,
  bonus: o.bonus,
  status: o.status === 'completed' ? 'used' : 'reserved',
  created_at: o.created_at,
  used_at: o.completed_at
})
on('GET', '/promo-codes/:id/usages', (req) => {
  const p = promoCodes.find((x) => x.id === Number(req.params.id))
  const held = (o: any) => p && o.promo_code === p.code && (o.status === 'completed' || o.status === 'pending')
  return paginate(orders.filter(held).map(usageOf), req.query)
})

// The code is upper-cased (a random one when none is given) and never changes; expires_at null clears it.
on('POST', '/promo-codes', (req) => {
  const code = String(req.body?.code || '').trim().toUpperCase() || newCode()
  if (!/^[A-Z0-9_-]{3,64}$/.test(code)) return fail(400, 'invalid_argument', 'invalid argument', { fields: [{ field: 'code', code: 'invalid', message: '3-64 位大写字母、数字、下划线或连字符' }] })
  if (promoCodes.some((x) => x.code === code)) return fail(409, 'conflict', '优惠码已存在')
  const p = { id: 10 + promoCodes.length, used_count: 0, status: 'active', created_at: now(), ...req.body, code }
  promoCodes.unshift(p)
  return { __status: 201, body: { data: p } }
})

on('PATCH', '/promo-codes/:id', (req) => {
  const p = promoCodes.find((x) => x.id === Number(req.params.id))
  return p ? Object.assign(p, req.body, { code: p.code }) : fail(404, 'not_found', '优惠码不存在')
})

on('DELETE', '/promo-codes/:id', (req) => {
  const i = promoCodes.findIndex((x) => x.id === Number(req.params.id))
  if (i < 0) return fail(404, 'not_found', '优惠码不存在')
  promoCodes.splice(i, 1)
  return noContent()
})
