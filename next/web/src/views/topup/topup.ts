import { api as coreApi, useHost } from '@sub2api/host'

// Core top-up (CONTRACTS: core top-up and plugin payment gateways): what the
// top-up views share (messages under topup.* by their full key, formatting, toast, confirm,
// the API client) and the types and calls of the core routes (/api/v1/me/topup,
// /topup/*, /promo-codes).
export function useTopup() {
  const host = useHost()
  return {
    t: host.i18n.t,
    /** Formatting (money, numbers, dates) in the viewer's locale. */
    i18n: host.i18n,
    toast: host.toast,
    confirm: host.confirm,
    api: coreApi
  }
}

/** Human-readable message of a failed call (ApiError is an Error), else `fallback`. */
export function errorMessage(e: unknown, fallback: string): string {
  return e instanceof Error && e.message ? e.message : fallback
}

// ---- Settings (core, GET/PUT /topup/settings) ----

/** A built-in payment gateway (its credentials are core settings); a plugin gateway is "<plugin>.<id>". */
export type GatewayKey = 'epay' | 'stripe'

/** A payment method users pick when topping up, served by one gateway (new-api PayMethods). */
export interface PaymentMethod {
  /** Type key sent to the gateway (epay: alipay / wxpay / qqpay / custom; stripe: stripe). */
  type: string
  name: string
  gateway: GatewayKey
  /** Built-in icon key (alipay / wxpay / qqpay / stripe / card); another key shows the name's initial. */
  icon: string
  /** Background of the built-in icon (#rrggbb); its brand color when empty. */
  color?: string
  /** Custom logo in place of the built-in icon: a base64 image data URL (PNG / JPEG / WebP / SVG, at most 64 KB) or an https address. */
  logo?: string
  /** Optional minimum top-up for this method (balance units); 0 = the global one. */
  min_topup?: number
  enabled: boolean
}

/** Discount by top-up amount: from `amount` up, pay `rate` of the price (0.95 = 5% off). */
export interface DiscountTier {
  amount: number
  rate: number
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
  price: number
  min_topup: number
  max_topup: number
  order_timeout_minutes: number
  max_pending_orders: number
  amount_options: number[]
  discounts: DiscountTier[]
  methods: PaymentMethod[]
  epay: EpayConfig
  stripe: StripeConfig
}

// ---- User side ----

/** What the wallet's top-up card needs (GET /checkout). */
export interface CheckoutInfo {
  enabled: boolean
  currency: string
  price: number
  min_topup: number
  max_topup: number
  amount_options: number[]
  discounts: DiscountTier[]
  methods: Array<Pick<PaymentMethod, 'type' | 'name' | 'icon' | 'color' | 'logo' | 'min_topup'>>
}

/** Price of a top-up before paying (POST /orders/preview). */
export interface OrderPreview {
  amount: number
  /** Balance credited on top (promo code). */
  bonus: number
  discount_rate: number
  /** What the user pays, in the local currency. */
  pay_amount: number
  original_pay_amount: number
  promo_error?: string
}

export type OrderStatus = 'pending' | 'paid' | 'recharging' | 'completed' | 'failed' | 'expired' | 'cancelled'

export interface Order {
  id: number
  out_trade_no: string
  user_id: number
  user_email?: string
  amount: number
  bonus: number
  pay_amount: number
  currency: string
  method: string
  method_name: string
  gateway: GatewayKey
  promo_code?: string
  status: OrderStatus
  /** The gateway's transaction number (Epay trade_no, Stripe PaymentIntent id), for reconciliation. */
  trade_no?: string
  failed_reason?: string
  created_at: string
  paid_at?: string
  completed_at?: string
  expires_at: string
  /** Where to pay, while pending (the user's own orders only): "continue paying". */
  pay_url?: string
  qr_code?: string
}

/** POST /orders: where to pay. */
export interface CreatedOrder {
  out_trade_no: string
  pay_url?: string
  qr_code?: string
  expires_at: string
}

// ---- Admin side ----

export interface OrderStats {
  today_amount: number
  today_count: number
  month_amount: number
  pending_count: number
  failed_count: number
}

export interface PromoCode {
  id: number
  code: string
  bonus_amount: number
  /** Smallest top-up (balance units) the code applies to; 0 = any. Keeps a fixed bonus from beating a tiny top-up. */
  min_amount: number
  max_uses: number
  used_count: number
  status: 'active' | 'disabled'
  expires_at?: string
  notes?: string
  created_at: string
}

/** One user's use of a promo code: held by an unpaid order (reserved), or credited with it (used). */
export interface PromoUsage {
  id: number
  user_id: number
  user_email?: string
  out_trade_no: string
  bonus: number
  status: 'reserved' | 'used'
  created_at: string
  used_at?: string
}

/**
 * A payment gateway a plugin provides (manifest payments.gateways,
 * capability payment.gateway.v1): listed on the gateways tab; its
 * credentials are the plugin's own settings, its notifications come in on
 * /payment/notify/<id> and the plugin only verifies them.
 */
export interface PluginGateway {
  /** "<plugin key>.<gateway id>", unique across the site. */
  id: string
  plugin_key: string
  plugin_name: string
  name: string
  /** The plugin is enabled and answers. */
  ready: boolean
  /** Payment methods it offers as templates (type key and name). */
  methods: { type: string; name: string }[]
  /** Methods configured on it. */
  in_use: number
}

const api = () => useTopup().api

export const paymentApi = {
  checkout: () => api().get<CheckoutInfo>('/me/topup'),
  preview: (body: { amount: number; method: string; promo_code?: string }) => api().post<OrderPreview>('/me/topup/preview', body),
  createOrder: (body: { amount: number; method: string; promo_code?: string }) => api().post<CreatedOrder>('/me/topup/orders', body),
  myOrders: (query: Record<string, string>) => api().list<Order>('/me/topup/orders', query),
  order: (outTradeNo: string) => api().get<Order>(`/me/topup/orders/${encodeURIComponent(outTradeNo)}`),
  cancelOrder: (outTradeNo: string) => api().post<Order>(`/me/topup/orders/${encodeURIComponent(outTradeNo)}/cancel`, {}),

  adminOrders: (query: Record<string, string>) => api().list<Order>('/topup/orders', query),
  orderStats: () => api().get<OrderStats>('/topup/orders/stats'),
  pluginGateways: () => api().get<PluginGateway[]>('/topup/gateways'),


  promoCodes: (query: Record<string, string>) => api().list<PromoCode>('/promo-codes', query),
  createPromoCode: (body: Partial<PromoCode>) => api().post<PromoCode>('/promo-codes', body),
  updatePromoCode: (id: number, body: Partial<PromoCode>) => api().patch<PromoCode>(`/promo-codes/${id}`, body),
  deletePromoCode: (id: number) => api().del<void>(`/promo-codes/${id}`),
  promoUsagesPath: (id: number) => `/promo-codes/${id}/usages`
}
