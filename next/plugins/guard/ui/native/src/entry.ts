import type { PluginHost } from '@sub2api/host'
import { lazyPage } from '@sub2api/ui'
import BlockedTodayCard from './BlockedTodayCard.vue'
import GuardSettings from './GuardSettings.vue'
import messages from './messages'
import { setHost } from './host'

// Native UI entry of the guard plugin (manifest ui.native.entry).
// Component names match manifest ui.pages.dashboard.component,
// ui.slots[dashboard.widgets].component and ui.settings.component.
export function register(host: PluginHost) {
  setHost(host)
  host.addMessages(messages)
  host.registerComponent('GuardDashboard', lazyPage(() => import('./GuardDashboard.vue')))
  host.registerComponent('BlockedTodayCard', BlockedTodayCard)
  host.registerComponent('GuardSettings', GuardSettings)
}

export function unregister() {
  setHost(null)
}
