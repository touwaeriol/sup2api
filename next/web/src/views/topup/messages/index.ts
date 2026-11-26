import common from './common'
import settings from './settings'
import admin from './admin'
import wallet from './wallet'

// Top-up messages, the core i18n namespace topup.* (locales/<locale>/topup.ts).
// Each area keeps its own file; keys are namespaced by area (settings.*, admin.*, wallet.*).
export default {
  en: { ...common.en, settings: settings.en, admin: admin.en, wallet: wallet.en },
  zh: { ...common.zh, settings: settings.zh, admin: admin.zh, wallet: wallet.zh }
}
