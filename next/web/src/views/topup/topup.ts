import type { Money } from '@/api/types'

// Core top-up (CONTRACTS §140): the types of the core routes (/me/topup,
// /topup/*, /promo-codes) and the gateway names the pages share. Amounts,
// prices and rates are decimal strings (Money); the calls are in api.ts.

/** A built-in payment gateway (its credentials are core settings); a plugin gateway is "<plugin id>.<gateway key>". */
export type GatewayKey = 'epay' | 'stripe'

export const BUILTIN_GATEWAYS: readonly GatewayKey[] = ['epay', 'stripe']

export const isBuiltinGateway = (id: string): id is GatewayKey => (BUILTIN_GATEWAYS as readonly string[]).includes(id)

/** A gateway's display name where only its id is known (orders): built-in ones translated, a plugin one as its id. */
export const gatewayName = (t: (key: string) => string, id: string) => (isBuiltinGateway(id) ? t(`topup.gateway.${id}`) : id)

// ---- Settings (GET/PUT /topup/settings) ----

/** A payment method users pick when topping up, served by one gateway. */
export interface PaymentMethod {
  /** Type key sent to the gateway (epay: alipay / wxpay / qqpay / custom; stripe: stripe). */
  type: string
  name: string
  /** A built-in gateway or a plugin's "<plugin id>.<gateway key>" (GET /topup/gateways). */
  gateway: string
  /** Built-in icon key (alipay / wxpay / qqpay / stripe / card); another key shows the name's initial. */
  icon: string
  /** Background of the built-in icon (#rrggbb); its brand color when empty. */
  color?: string
  /** Custom logo in place of the built-in icon: a base64 image data URL (PNG / JPEG / WebP / SVG, at most 64 KB) or an https address. */
  logo?: string
  /** Optional minimum top-up for this method (balance units); "0" = the global one. */
  min_topup?: Money
  enabled: boolean
}

/** Discount by top-up amount: from `amount` up, pay `rate` of the price ("0.95" = 5% off). */
export interface DiscountTier {
  amount: Money
  rate: Money
}

export interface EpayConfig {
  enabled: boolean
  endpoint: string
  merchant_id: string
  key: string
}

export interface StripeConfig {
  enabled: boolean
  secret_key: string
  webhook_secret: string
}

export interface PaymentSettingsValue {
  enabled: boolean
  /** Local currency the user pays in. */
  currency: string
  /** Local currency per 1 balance unit (USD). */
  price: Money
  min_topup: Money
  max_topup: Money
  order_timeout_minutes: number
  max_pending_orders: number
  amount_options: Money[]
  discounts: DiscountTier[]
  methods: PaymentMethod[]
  epay: EpayConfig
  stripe: StripeConfig
}

/** A currency users may pay in, with its decimals (JPY 0, the others 2). */
export interface CurrencyInfo {
  code: string
  exponent: number
}

/** GET /topup/settings (and the PUT answer): the values and what is read only beside them. */
export interface SettingsView {
  values: PaymentSettingsValue
  /** Secret paths ("epay/key"): they come back as "******", which sent back keeps them. */
  secret_fields: string[]
  /** Callback addresses of the built-in gateways; "" while the site address is not https. */
  notify_urls: Record<GatewayKey, string>
  currencies: CurrencyInfo[]
}

/**
 * A payment gateway a plugin provides (manifest payments.gateways,
 * capability payment.gateway.v1); its credentials are the plugin's own
 * settings and the plugin only verifies its notifications.
 */
export interface PluginGateway {
  /** "<plugin id>.<gateway key>", unique across the site. */
  id: string
  /** The plugin id (its detail page is /plugins/<id>). */
  plugin_key: string
  plugin_name: string
  name: string
  /** The plugin is enabled and answers. */
  ready: boolean
  /** Payment methods it offers as templates (type key and name). */
  methods: { type: string; name: string }[]
  /** Methods configured on it. */
  in_use: number
  /** Where the provider posts notifications; "" while the site address is not https. */
  notify_url: string
}

// ---- User side ----

/** What the wallet's top-up card needs (GET /me/topup). */
export interface CheckoutInfo {
  enabled: boolean
  currency: string
  /** Decimals of `currency`: what users pay is rounded to them. */
  currency_exponent: number
  price: Money
  min_topup: Money
  max_topup: Money
  amount_options: Money[]
  discounts: DiscountTier[]
  methods: CheckoutMethod[]
}

export type CheckoutMethod = Pick<PaymentMethod, 'type' | 'name' | 'icon' | 'color' | 'logo' | 'min_topup'>

/** The body of POST /me/topup/preview and /me/topup/orders. */
export interface OrderRequest {
  amount: Money
  method: string
  promo_code?: string
}

/** Price of a top-up before paying (POST /me/topup/preview). */
export interface OrderPreview {
  amount: Money
  /** Balance credited on top (promo code). */
  bonus: Money
  discount_rate: Money
  /** What the user pays, in the local currency. */
  pay_amount: Money
  original_pay_amount: Money
  promo_error?: string
}

export type OrderStatus = 'pending' | 'paid' | 'completed' | 'failed' | 'expired' | 'cancelled'

export interface Order {
  id: number
  out_trade_no: string
  user_id: number
  user_email?: string
  amount: Money
  bonus: Money
  pay_amount: Money
  currency: string
  method: string
  method_name: string
  gateway: string
  promo_code?: string
  status: OrderStatus
  /** A payment may still credit it: pending, or expired / cancelled within the late window. */
  payable: boolean
  /** The gateway's transaction number (Epay trade_no, Stripe PaymentIntent id), for reconciliation. */
  trade_no?: string
  failed_reason?: string
  created_at: string
  paid_at?: string
  completed_at?: string
  expires_at: string
  /** Where to pay, on the user's own pending orders not yet expired ("continue paying"). */
  pay_url?: string
  /** The QR code to scan, as a PNG data URL (same orders as pay_url), on the order read with ?qr=1 only. */
  qr_image?: string
}

/** POST /me/topup/orders: where to pay. */
export interface CreatedOrder {
  out_trade_no: string
  pay_url?: string
  qr_image?: string
  expires_at: string
}

/**
 * What the waiting dialog starts from: a new order (the create answer, which
 * has no amounts) or a pending one from the history. The amounts shown are
 * always the order's own, read back from the server.
 */
export type PayingOrder = CreatedOrder &
  Pick<Order, 'method_name'> &
  Partial<Pick<Order, 'amount' | 'bonus' | 'pay_amount' | 'currency' | 'payable'>>

// ---- Admin side ----

/**
 * What the core made of one notification or query of an order
 * (GET /topup/orders/:id/events): its kind (NOTIFY_OK, SIGNATURE_INVALID,
 * …), whether it was verified, and the whitelisted fields it kept (amount
 * in the currency's major unit).
 */
export interface PaymentEvent {
  id: number
  gateway: string
  kind: string
  verified: boolean
  event_id?: string
  detail: Record<string, string>
  created_at: string
}

export interface OrderStats {
  today_amount: Money
  today_count: number
  month_amount: Money
  pending_count: number
  failed_count: number
}

/** What a promo code shows (server-side): its switch, or why an enabled one no longer applies. */
export type PromoState = 'active' | 'disabled' | 'expired' | 'exhausted'

export interface PromoCode {
  id: number
  code: string
  bonus_amount: Money
  /** Smallest top-up (balance units) the code applies to; "0" = any. Keeps a fixed bonus from beating a tiny top-up. */
  min_amount: Money
  max_uses: number
  used_count: number
  status: 'active' | 'disabled'
  state: PromoState
  expires_at?: string
  notes?: string
  created_at: string
}

/** The body of POST and PATCH /promo-codes: a PATCH changes what it sends, expires_at null clears it; the code is set once. */
export type PromoInput = Partial<Pick<PromoCode, 'code' | 'bonus_amount' | 'min_amount' | 'max_uses' | 'status' | 'notes'>> & {
  expires_at?: string | null
}

/** One user's use of a promo code: held by an unpaid order (reserved), or credited with it (used). */
export interface PromoUsage {
  id: number
  user_id: number
  user_email?: string
  out_trade_no: string
  bonus: Money
  status: 'reserved' | 'used'
  created_at: string
  used_at?: string
}
