import common from './common'
import settings from './settings'
import admin from './admin'
import wallet from './wallet'

// Payment UI messages, registered under plugin.payment.* via host.addMessages.
// Each area keeps its own file; keys are namespaced by area (settings.*, admin.*, wallet.*).
export default {
  en: { ...common.en, settings: settings.en, admin: admin.en, wallet: wallet.en },
  zh: { ...common.zh, settings: settings.zh, admin: admin.zh, wallet: wallet.zh }
}
