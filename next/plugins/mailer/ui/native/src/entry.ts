import type { PluginHost } from '@sub2api/host'
import MailerSettings from './MailerSettings.vue'
import messages from './messages'
import { setHost } from './host'

// Native UI entry of the mailer plugin (manifest ui.native.entry). The
// component name matches manifest ui.settings.component: the console shows it
// in the plugin's settings tab.
export function register(host: PluginHost) {
  setHost(host)
  host.addMessages(messages)
  host.registerComponent('MailerSettings', MailerSettings)
}

export function unregister() {
  setHost(null)
}
