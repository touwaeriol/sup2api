import type { PluginHost } from '@sub2api/host'
import { lazyPage } from '@sub2api/ui'
import messages from './messages'
import { setHost } from './host'

// Native UI entry of the payment plugin (manifest ui.native.entry). Component
// names match manifest ui.pages.*.component and ui.slots[wallet.topup]; the
// settings are the "settings" page (ui.settings.page).
export function register(host: PluginHost) {
  setHost(host)
  host.addMessages(messages)
  host.registerComponent('PaymentSettings', lazyPage(() => import('./settings/PaymentSettings.vue')))
  host.registerComponent('PaymentOrders', lazyPage(() => import('./admin/OrdersPage.vue')))
  host.registerComponent('PaymentRedeemCodes', lazyPage(() => import('./admin/RedeemCodesPage.vue')))
  host.registerComponent('PaymentPromoCodes', lazyPage(() => import('./admin/PromoCodesPage.vue')))
  host.registerComponent('PaymentMyOrders', lazyPage(() => import('./wallet/MyOrdersPage.vue')))
  host.registerComponent('WalletTopup', lazyPage(() => import('./wallet/WalletTopup.vue')))
}

export function unregister() {
  setHost(null)
}
