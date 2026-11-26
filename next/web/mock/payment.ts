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
  // The admin pages fold into one submenu of their own (ui.sections); the
  // caller's order history joins the core "me" section.
  sections: [{ id: 'pay', label: L('Top-up & payment', '充值/支付'), order: 410 }],
  menus: [
    { id: 'orders', section: 'pay', label: L('Top-up orders', '充值订单'), page: 'orders', icon: 'balance' },
    { id: 'settings', section: 'pay', label: L('Top-up settings', '充值配置'), page: 'settings', icon: 'settings' },
    { id: 'redeem', section: 'pay', label: L('Redeem codes', '兑换码'), page: 'redeem', icon: 'key' },
    { id: 'promo', section: 'pay', label: L('Promo codes', '优惠码'), page: 'promo', icon: 'price' },
    { id: 'my-orders', section: 'me', label: L('Top-up history', '充值记录'), page: 'my-orders', icon: 'list', permission: 'recharge:use' }
  ],
  pages: {
    orders: { type: 'native', title: L('Top-up orders', '充值订单'), component: 'PaymentOrders' },
    settings: { type: 'native', title: L('Top-up settings', '充值配置'), component: 'PaymentSettings' },
    redeem: { type: 'native', title: L('Redeem codes', '兑换码'), component: 'PaymentRedeemCodes' },
    promo: { type: 'native', title: L('Promo codes', '优惠码'), component: 'PaymentPromoCodes' },
    'my-orders': { type: 'native', title: L('Top-up history', '充值记录'), component: 'PaymentMyOrders' }
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
  manifest: { key: 'payment', version: VERSION, name: paymentUIPlugin.name, ui: { native: { entry: 'ui/native/entry.js' }, settings: { mode: 'native', page: 'settings', permission: 'config:manage', secrets: SECRETS.map((path) => ({ path })) } } },
  settings: { mode: 'native', page: 'settings', secret_fields: SECRETS, values: settings }
} as any)

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

on('GET', '/p/payment/checkout', () => {
  const methods = settings.methods.filter((m) => m.enabled).map(({ type, name, icon, color, logo, min_topup }) => ({ type, name, icon, color, logo, min_topup }))
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
  const hit = (o: any) => [o.out_trade_no, o.trade_no, o.user_email].some((v) => String(v ?? '').toLowerCase().includes(q))
  return base.filter((o) => (!query.status || o.status === query.status) && (!query.method || o.method === query.method) && (!q || hit(o)))
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
  { id: 3, code: 'WELCOME10', bonus_amount: 10, min_amount: 20, max_uses: 500, used_count: 132, status: 'active', expires_at: now(86400 * 20), notes: '新用户首充', created_at: now(-86400 * 10) },
  { id: 2, code: 'DOUBLE11', bonus_amount: 20, min_amount: 100, max_uses: 100, used_count: 100, status: 'active', expires_at: now(-86400), notes: '双十一', created_at: now(-86400 * 20) },
  { id: 1, code: 'VIP2026', bonus_amount: 50, min_amount: 500, max_uses: 0, used_count: 8, status: 'disabled', notes: '', created_at: now(-86400 * 40) }
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
