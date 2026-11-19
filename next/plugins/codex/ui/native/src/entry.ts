import type { PluginHost } from '@sub2api/host'
import CodexAuthForm from './CodexAuthForm.vue'
import CodexResetBadge from './CodexResetBadge.vue'
import CodexResetTab from './CodexResetTab.vue'
import CodexSettings from './CodexSettings.vue'
import messages from './messages'
import { setHost } from './host'
import './codex.css'

// Native UI entry of the codex plugin (manifest ui.native.entry). The
// component names match manifest accountTypes[].form.component and
// ui.settings.component. They are not wrapped in lazyPage: the account
// editor calls validate() through a template ref, which an async wrapper
// would not forward.
export function register(host: PluginHost) {
  setHost(host)
  host.addMessages(messages)
  host.registerComponent('CodexAuthForm', CodexAuthForm)
  host.registerComponent('CodexResetTab', CodexResetTab)
  host.registerComponent('CodexResetBadge', CodexResetBadge)
  host.registerComponent('CodexSettings', CodexSettings)
}

export function unregister() {
  setHost(null)
}
