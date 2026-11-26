import { api } from '@sub2api/host'
import type {
  CheckoutInfo,
  CreatedOrder,
  Order,
  OrderPreview,
  OrderRequest,
  OrderStats,
  PaymentEvent,
  PluginGateway,
  PromoCode,
  PromoInput,
  SettingsView
} from './topup'

// The core top-up routes (CONTRACTS §140). Lists are read with
// useList(TOPUP_PATHS.…); the other calls are here.

const order = (no: string) => `/me/topup/orders/${encodeURIComponent(no)}`

export const TOPUP_PATHS = {
  settings: '/topup/settings',
  myOrders: '/me/topup/orders',
  orders: '/topup/orders',
  promoCodes: '/promo-codes',
  promoCode: (id: number) => `/promo-codes/${id}`,
  promoUsages: (id: number) => `/promo-codes/${id}/usages`
} as const

export const topupApi = {
  settings: () => api.get<SettingsView>(TOPUP_PATHS.settings),
  saveSettings: (values: SettingsView['values']) => api.put<SettingsView>(TOPUP_PATHS.settings, { values }),
  pluginGateways: () => api.get<PluginGateway[]>('/topup/gateways'),

  checkout: () => api.get<CheckoutInfo>('/me/topup'),
  preview: (body: OrderRequest) => api.post<OrderPreview>('/me/topup/preview', body),
  createOrder: (body: OrderRequest) => api.post<CreatedOrder>(TOPUP_PATHS.myOrders, body),
  /** One of the caller's orders; `qr` ("continue paying") adds its QR code, which a poll does without. */
  order: (no: string, qr = false) => api.get<Order>(order(no), qr ? { qr: 1 } : undefined),
  cancelOrder: (no: string) => api.post<Order>(`${order(no)}/cancel`, {}),

  orderStats: () => api.get<OrderStats>(`${TOPUP_PATHS.orders}/stats`),
  orderEvents: (id: number, pageSize: number) =>
    api.list<PaymentEvent>(`${TOPUP_PATHS.orders}/${id}/events`, { page: 1, page_size: pageSize }),

  createPromoCode: (body: PromoInput) => api.post<PromoCode>(TOPUP_PATHS.promoCodes, body),
  updatePromoCode: (id: number, body: PromoInput) => api.patch<PromoCode>(TOPUP_PATHS.promoCode(id), body)
}
