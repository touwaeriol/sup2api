import { fail, noContent, now, on, paginate } from './router'
import { myWallet } from './core'
import { pluginId } from './pluginIds'
import { grant, plugins } from './plugins'
import { roundTo } from '../src/views/topup/rounding'

// Mock of the core top-up (/me/topup, /topup/*, /promo-codes, CONTRACTS
// §140): settings with the built-in Epay and Stripe gateways, orders and
// promo codes in memory, and one plugin that adds a gateway (example's
// alipay_direct) for the gateways tab. Amounts are decimal strings, as the
// core sends them. A new order completes on the first read 8 s after it was
// placed (credited to the shared mock balance), so the waiting dialog can be
// seen to finish.

const L = (en: string, zh: string) => ({ en, zh })

const SECRETS = ['epay/key', 'stripe/secret_key', 'stripe/webhook_secret']
const SITE = 'https://console.example.com'
const notifyUrl = (gateway: string) => `${SITE}/api/v1/payment/notify/${gateway}`
const CURRENCIES = [
  { code: 'CNY', exponent: 2 },
  { code: 'USD', exponent: 2 },
  { code: 'HKD', exponent: 2 },
  { code: 'EUR', exponent: 2 },
  { code: 'GBP', exponent: 2 },
  { code: 'JPY', exponent: 0 }
]
const exponentOf = (code: string) => CURRENCIES.find(c => c.code === code)?.exponent ?? 2

// A method with a custom logo: a small SVG stored inline as a data URL, as the settings dialog uploads it.
const USDT_SVG =
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><circle cx="16" cy="16" r="16" fill="#26a17b"/><path fill="#fff" d="M17.92 17.38c-.11 0-.69.04-1.98.04-1.03 0-1.75-.03-2-.04-3.97-.17-6.94-.87-6.94-1.7s2.97-1.52 6.94-1.7v2.7c.26.02 1 .06 2.02.06 1.22 0 1.83-.05 1.94-.06v-2.7c3.96.18 6.92.87 6.92 1.7s-2.96 1.53-6.92 1.7zm0-3.66V11.3h5.53V7.6H8.4v3.7h5.53v2.42c-4.5.2-7.88 1.1-7.88 2.17s3.38 1.96 7.88 2.17v7.75h3.99v-7.75c4.48-.21 7.86-1.1 7.86-2.17s-3.38-1.96-7.86-2.17z"/></svg>'
const USDT_LOGO = `data:image/svg+xml;base64,${Buffer.from(USDT_SVG).toString('base64')}`

// What the core draws for a gateway's QR code (a PNG data URL): one fixed,
// scannable code (weixin://wxpay/bizpayurl?pr=s2a-mock-order) for every order.
const QR_IMAGE =
  'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAALkAAAC5AQAAAABc1qPxAAABV0lEQVR42u2XQYrDMAxFf/DCyxxBN2kuZkjAF0tu4iNo6UXoHyR3Sge6rQqdepXwvBDS/5IMPj8bvuCDgQKYtAgWsuUdQA4EK3loQR5fJCNBQT5Y2YG15UMh0UAL+sTKtwByRMVo8FuFg+e4EgluSrS8yDOJvhL40SK8ztUC+mvOFwPTAebNFJEsL7XlOGA6WDSZEVMDUCQSVHKfa+vTyMsJBoIN/ovLfJoReU9JANC19YmnwBqAXwkEPKUvZj9zgEXFQKBANwnAsmHSaJEgeSKEVxQZ2ogD3AQAxMQgXp5IUNkvM1s3kD2+OKAr847kC4ZwjwWj+/iWkRomnsiBoBKYSe5ItNYXCnzi3Pu9Pjbkl4PbrEWfFDZ27koMAb5ZrsTiAZklA8HYLFu2Qf9YjzBgUfmxqfsG4DbwBhAJfLPc4KsVrA8xELgSbeCqvybWSPB9O/8z8AMP2/2OwaTryAAAAABJRU5ErkJggg=='

const settings = {
  enabled: true,
  currency: 'CNY',
  price: '7.3',
  min_topup: '1',
  max_topup: '10000',
  order_timeout_minutes: 30,
  max_pending_orders: 3,
  amount_options: ['10', '20', '50', '100', '200', '500'],
  discounts: [
    { amount: '100', rate: '0.95' },
    { amount: '500', rate: '0.9' }
  ],
  methods: [
    { type: 'alipay', name: '支付宝', gateway: 'epay', icon: 'alipay', color: '#1677ff', min_topup: '0', enabled: true },
    { type: 'wxpay', name: '微信支付', gateway: 'epay', icon: 'wxpay', color: '#07c160', min_topup: '0', enabled: true },
    { type: 'usdt', name: 'USDT', gateway: 'epay', icon: '', color: '', logo: USDT_LOGO, min_topup: '0', enabled: true },
    { type: 'stripe', name: 'Stripe', gateway: 'stripe', icon: 'stripe', color: '#635bff', min_topup: '5', enabled: false }
  ],
  epay: { enabled: true, endpoint: 'https://pay.example.com', merchant_id: '1001', key: '******' },
  stripe: { enabled: false, secret_key: '', webhook_secret: '' }
}

/** GET/PUT /topup/settings: the values (secrets masked) and the read-only parts beside them. */
const settingsView = () => ({
  values: settings,
  secret_fields: SECRETS,
  notify_urls: { epay: notifyUrl('epay'), stripe: notifyUrl('stripe') },
  currencies: CURRENCIES
})
const getPath = (o: any, path: string) => path.split('/').reduce((v, k) => v?.[k], o)
// "******" or none keeps the stored secret; a new one is stored (and shown masked).
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
on('PUT', '/topup/settings', req => {
  Object.assign(settings, keepSecrets({ ...(req.body?.values || {}) }))
  return settingsView()
})

// A third-party plugin that adds a payment gateway (manifest payments.gateways; id per §137).
const GATEWAY_PLUGIN = pluginId('example', 'alipay_direct')
const GATEWAY_PLUGIN_VERSION = '0.1.0'
plugins.set(GATEWAY_PLUGIN, {
  key: GATEWAY_PLUGIN,
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
  grants: ['payment.gateway', 'net'].map(p => grant(p, null, GATEWAY_PLUGIN_VERSION)),
  manifest: {
    key: 'alipay_direct',
    publisher: 'example',
    version: GATEWAY_PLUGIN_VERSION,
    name: L('Alipay direct', '支付宝官方直连'),
    capabilities: [{ id: 'payment.gateway.v1' }]
  },
  settings: {
    mode: 'schema',
    secret_fields: ['private_key'],
    values: { app_id: '2021000000000000', private_key: '******', sandbox: false }
  }
} as any)

const PLUGIN_GATEWAY = `${GATEWAY_PLUGIN}.alipay`
const inUse = (id: string) => settings.methods.filter(m => m.gateway === id).length

on('GET', '/topup/gateways', () => [
  {
    id: PLUGIN_GATEWAY,
    plugin_key: GATEWAY_PLUGIN,
    plugin_name: '支付宝官方直连',
    name: '支付宝（官方）',
    ready: true,
    methods: [
      { type: 'alipay_pc', name: '电脑网站支付' },
      { type: 'alipay_qr', name: '当面付扫码' }
    ],
    in_use: inUse(PLUGIN_GATEWAY),
    notify_url: notifyUrl(PLUGIN_GATEWAY)
  }
])

// ---- pricing ----

// Half away from zero after cutting float noise, as the core and the wallet's estimate.
const round = (v: number, digits: number) => roundTo(v, digits).toFixed(digits)
const rateOf = (amount: number) =>
  settings.discounts.filter(d => amount >= Number(d.amount)).reduce((r, d) => Math.min(r, Number(d.rate)), 1)

/** POST /me/topup/preview: the price of `amount` (each figure rounded once), with a promo code's bonus or why it does not apply. */
function preview(body: any) {
  const amount = Number(body?.amount)
  const raw = amount * Number(settings.price)
  const rate = rateOf(amount)
  const digits = exponentOf(settings.currency)
  const { bonus, error } = promoBonus(
    String(body?.promo_code || '')
      .trim()
      .toUpperCase(),
    amount
  )
  return {
    amount: String(amount),
    bonus: String(bonus),
    discount_rate: String(rate),
    pay_amount: round(raw * rate, digits),
    original_pay_amount: round(raw, digits),
    promo_error: error
  }
}

// ---- orders ----

const STATUSES = ['completed', 'completed', 'completed', 'pending', 'completed', 'expired', 'failed', 'completed', 'cancelled', 'completed']
const EMAILS = ['admin@example.com', 'alice@example.com', 'bob@example.com', 'carol@example.com']
const AMOUNTS = [10, 20, 50, 100, 200, 500]
const LATE_WINDOW_SEC = 24 * 3600

// Mostly Epay (Alipay / WeChat Pay); every sixth order went through Stripe before it was switched off.
const seedMethod = (i: number) => settings.methods[i % 6 === 5 ? 3 : i % 2]
// The gateway's transaction number: an Epay trade_no, or a Stripe PaymentIntent id.
const seedTradeNo = (gateway: string, i: number) =>
  gateway === 'stripe' ? `pi_3QxK${(0x9f2c1 + i * 4099).toString(36)}Lm2R` : `2026112522001${400000 + i}`

function seedOrder(i: number): any {
  const status = STATUSES[i % STATUSES.length]
  const amount = AMOUNTS[i % AMOUNTS.length]
  const method = seedMethod(i)
  const created = -3600 * (i * 5 + 1)
  const done = status === 'completed'
  return {
    id: 500 - i,
    out_trade_no: `s2a_${(0x5a3f00 + i * 7919).toString(16)}e1c4b2`,
    user_id: (i % 4) + 1,
    user_email: EMAILS[i % 4],
    amount: String(amount),
    bonus: i % 7 === 0 ? '10' : '0',
    pay_amount: preview({ amount }).pay_amount,
    currency: settings.currency,
    method: method.type,
    method_name: method.name,
    gateway: method.gateway,
    promo_code: i % 7 === 0 ? 'WELCOME10' : undefined,
    status,
    trade_no: done ? seedTradeNo(method.gateway, i) : undefined,
    failed_reason: status === 'failed' ? 'ledger credit rejected: daily limit reached' : undefined,
    created_at: now(created),
    paid_at: done ? now(created + 40) : undefined,
    completed_at: done ? now(created + 41) : undefined,
    expires_at: now(created + 1800)
  }
}

const orders: any[] = Array.from({ length: 36 }, (_, i) => seedOrder(i))

/** A payment may still credit it: pending, or expired / cancelled within the late window. */
const payable = (o: any) =>
  o.status === 'pending' || (['expired', 'cancelled'].includes(o.status) && Date.parse(o.expires_at) + LATE_WINDOW_SEC * 1000 > Date.now())
/** Where to pay, on the caller's own pending orders not yet expired only. */
const payTo = (o: any) =>
  o.status === 'pending' && Date.parse(o.expires_at) > Date.now() ? { pay_url: o.pay_url, qr_image: o.qr_image } : {}
// Others (admins included) never see where to pay; undefined keys drop out of the JSON.
const view = (o: any, own: boolean) => ({ ...o, pay_url: undefined, qr_image: undefined, payable: payable(o), ...(own ? payTo(o) : {}) })
/** The caller's own order; its QR code only when asked (?qr=1, "continue paying"): a list or a poll carries none, as the core. */
const ownView = (o: any, qr = false) => ({ ...view(o, true), ...(qr ? {} : { qr_image: undefined }) })

// A usable code credits its bonus from its minimum top-up up; one message for unknown, disabled, used up or expired.
function promoBonus(code: string, amount: number): { bonus: number; error?: string } {
  if (!code) return { bonus: 0 }
  const p = promoCodes.find(x => x.code === code && stateOf(x) === 'active')
  if (!p) return { bonus: 0, error: '优惠码无效或已用完' }
  if (amount < Number(p.min_amount)) return { bonus: 0, error: '充值满 $' + p.min_amount + ' 才能使用这个优惠码' }
  return { bonus: Number(p.bonus_amount) }
}

/** The method an order may use at this amount, or why not. */
function orderMethod(body: any): any {
  const amount = Number(body?.amount)
  const method = settings.methods.find(m => m.type === body?.method && m.enabled)
  if (!method) return fail(400, 'invalid_argument', '支付方式不可用')
  const min = Math.max(Number(settings.min_topup), Number(method.min_topup))
  if (amount >= min && amount <= Number(settings.max_topup)) return method
  return fail(400, 'invalid_argument', `充值金额须在 ${min} 到 ${settings.max_topup} 之间`)
}

on('GET', '/me/topup', () => {
  const methods = settings.methods
    .filter(m => m.enabled)
    .map(({ type, name, icon, color, logo, min_topup }) => ({ type, name, icon, color, logo, min_topup }))
  const { enabled, currency, price, min_topup, max_topup, amount_options, discounts } = settings
  return {
    enabled,
    currency,
    currency_exponent: exponentOf(currency),
    price,
    min_topup,
    max_topup,
    amount_options,
    discounts,
    methods: enabled ? methods : []
  }
})

on('POST', '/me/topup/preview', req => preview(req.body))

// Epay methods pay on a page; the others show the gateway's QR code.
on('POST', '/me/topup/orders', req => {
  const method = orderMethod(req.body)
  if (method.__status) return method
  const p = preview(req.body)
  const no = `s2a_${Date.now().toString(16)}`
  const where =
    method.gateway === 'epay' && method.type !== 'wxpay'
      ? { pay_url: `https://pay.example.com/submit.php?out_trade_no=${no}` }
      : { qr_image: QR_IMAGE }
  const order = {
    id: 501 + orders.length,
    out_trade_no: no,
    user_id: 1,
    user_email: EMAILS[0],
    amount: p.amount,
    bonus: p.bonus,
    pay_amount: p.pay_amount,
    currency: settings.currency,
    method: method.type,
    method_name: method.name,
    gateway: method.gateway,
    promo_code: req.body?.promo_code || undefined,
    status: 'pending',
    created_at: now(),
    expires_at: now(settings.order_timeout_minutes * 60),
    ...where
  }
  orders.unshift(order)
  return { __status: 201, body: { data: { out_trade_no: no, expires_at: order.expires_at, ...where } } }
})

function filterOrders(query: Record<string, string>, base: any[]) {
  const q = (query.q || '').toLowerCase()
  const hit = (o: any) =>
    [o.out_trade_no, o.trade_no, o.user_email].some(v =>
      String(v ?? '')
        .toLowerCase()
        .includes(q)
    )
  const inRange = (o: any) => (!query.from || o.created_at >= query.from) && (!query.to || o.created_at < query.to)
  return base.filter(
    o => (!query.status || o.status === query.status) && (!query.method || o.method === query.method) && (!q || hit(o)) && inRange(o)
  )
}

// The caller's own: q finds an order number only, as the core.
on('GET', '/me/topup/orders', req => {
  const mine = orders.filter(o => o.user_id === 1 && o.out_trade_no.toLowerCase().includes((req.query.q || '').toLowerCase()))
  return paginate(
    filterOrders({ status: req.query.status || '' }, mine).map(o => ownView(o)),
    req.query
  )
})

// Another user's order is not found, as an unknown one.
const myOrder = (no: string) => orders.find(o => o.out_trade_no === no && o.user_id === 1)

/** An order placed here (it has somewhere to pay) is "paid" 8 s later: credited on the next read. */
function settle(o: any) {
  if (o.status !== 'pending' || !(o.pay_url || o.qr_image) || Date.now() - Date.parse(o.created_at) < 8000) return
  Object.assign(o, { status: 'completed', trade_no: seedTradeNo(o.gateway, o.id), paid_at: now(), completed_at: now() })
  myWallet.balance += Number(o.amount) + Number(o.bonus)
}

on('GET', '/me/topup/orders/:no', req => {
  const o = myOrder(req.params.no)
  if (!o) return fail(404, 'not_found', '订单不存在')
  settle(o)
  return ownView(o, req.query.qr === '1')
})

on('POST', '/me/topup/orders/:no/cancel', req => {
  const o = myOrder(req.params.no)
  if (!o) return fail(404, 'not_found', '订单不存在')
  if (o.status !== 'pending') return fail(409, 'conflict', '只能取消待支付的订单')
  o.status = 'cancelled'
  return view(o, true)
})

on('GET', '/topup/orders', req =>
  paginate(
    filterOrders(req.query, orders).map(o => view(o, false)),
    req.query
  )
)

on('GET', '/topup/orders/stats', () => {
  const done = orders.filter(o => o.status === 'completed')
  const sum = (xs: any[]) => String(xs.reduce((s, o) => s + Number(o.amount), 0))
  const count = (status: string) => orders.filter(o => o.status === status).length
  return {
    today_amount: sum(done.slice(0, 4)),
    today_count: 4,
    month_amount: sum(done),
    pending_count: count('pending'),
    failed_count: count('failed')
  }
})

// The gateway events of an order (GET /topup/orders/:id/events), newest
// first: a paid order has its accepted notification (the first paid one also
// a mismatched and a duplicate one), others a rejected one or none. The
// amount is in the currency's major unit, as the core shows it.
function eventsOf(o: any): any[] {
  const detail = (extra: object = {}) => ({
    out_trade_no: o.out_trade_no,
    trade_no: o.trade_no ?? '',
    status: 'paid',
    amount: o.pay_amount,
    currency: o.currency,
    type: o.method,
    ...extra
  })
  const at = (s: number) => new Date(Date.parse(o.created_at) + s * 1000).toISOString()
  const ev = (id: number, kind: string, verified: boolean, s: number, extra?: object) => ({
    id: o.id * 10 + id,
    gateway: o.gateway,
    kind,
    verified,
    detail: detail(extra),
    created_at: at(s)
  })
  if (o.status === 'failed') return [ev(1, 'SIGNATURE_INVALID', false, 30, { error: 'signature mismatch' })]
  if (o.status !== 'completed') return []
  if (o.id !== orders.find(x => x.status === 'completed')?.id) return [ev(1, 'NOTIFY_OK', true, 40)]
  return [
    ev(3, 'DUPLICATE', true, 90),
    ev(2, 'NOTIFY_OK', true, 40),
    ev(1, 'PAYMENT_AMOUNT_MISMATCH', true, 35, { amount: (Number(o.pay_amount) - 0.01).toFixed(2) })
  ]
}

on('GET', '/topup/orders/:id/events', req => {
  const o = orders.find(x => x.id === Number(req.params.id))
  return paginate(o ? eventsOf(o) : [], req.query)
})

// ---- promo codes ----

const newCode = () => Math.random().toString(36).slice(2, 10).toUpperCase()

const promoCodes: any[] = [
  {
    id: 3,
    code: 'WELCOME10',
    bonus_amount: '10',
    min_amount: '20',
    max_uses: 500,
    used_count: 4,
    status: 'active',
    expires_at: now(86400 * 20),
    notes: '新用户首充',
    created_at: now(-86400 * 10)
  },
  {
    id: 2,
    code: 'DOUBLE11',
    bonus_amount: '20',
    min_amount: '100',
    max_uses: 100,
    used_count: 100,
    status: 'active',
    expires_at: now(-86400),
    notes: '双十一',
    created_at: now(-86400 * 20)
  },
  {
    id: 1,
    code: 'VIP2026',
    bonus_amount: '50',
    min_amount: '500',
    max_uses: 0,
    used_count: 8,
    status: 'disabled',
    notes: '',
    created_at: now(-86400 * 40)
  }
]

/** The state the core reports: disabled first, then expired, then used up. */
function stateOf(p: any): string {
  if (p.status === 'disabled') return 'disabled'
  if (p.expires_at && p.expires_at <= now()) return 'expired'
  return p.max_uses > 0 && p.used_count >= p.max_uses ? 'exhausted' : 'active'
}
const promoView = (p: any) => ({ ...p, state: stateOf(p) })

on('GET', '/promo-codes', req => paginate(promoCodes.map(promoView), req.query))

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
on('GET', '/promo-codes/:id/usages', req => {
  const p = promoCodes.find(x => x.id === Number(req.params.id))
  const held = (o: any) => p && o.promo_code === p.code && (o.status === 'completed' || o.status === 'pending')
  return paginate(orders.filter(held).map(usageOf), req.query)
})

// The code is upper-cased (a random one when none is given) and never changes; expires_at null clears it.
on('POST', '/promo-codes', req => {
  const code =
    String(req.body?.code || '')
      .trim()
      .toUpperCase() || newCode()
  if (!/^[A-Z0-9_-]{3,64}$/.test(code))
    return fail(400, 'invalid_argument', 'invalid argument', {
      fields: [{ field: 'code', code: 'invalid', message: '3-64 位大写字母、数字、下划线或连字符' }]
    })
  if (promoCodes.some(x => x.code === code)) return fail(409, 'conflict', '优惠码已存在')
  const p = { id: 10 + promoCodes.length, used_count: 0, status: 'active', created_at: now(), ...req.body, code }
  promoCodes.unshift(p)
  return { __status: 201, body: { data: promoView(p) } }
})

on('PATCH', '/promo-codes/:id', req => {
  const p = promoCodes.find(x => x.id === Number(req.params.id))
  return p ? promoView(Object.assign(p, req.body, { code: p.code })) : fail(404, 'not_found', '优惠码不存在')
})

on('DELETE', '/promo-codes/:id', req => {
  const i = promoCodes.findIndex(x => x.id === Number(req.params.id))
  if (i < 0) return fail(404, 'not_found', '优惠码不存在')
  promoCodes.splice(i, 1)
  return noContent()
})
