import type { PluginHost } from '@sub2api/host'
import BedrockAccountForm from './BedrockAccountForm.vue'
import messages from './messages'
import { setHost } from './host'

// Native UI entry of the bedrock plugin (manifest ui.native.entry). The
// component name matches manifest accountTypes[].form.component; it is not
// wrapped in lazyPage, since the account editor calls validate() through a
// template ref.
export function register(host: PluginHost) {
  setHost(host)
  host.addMessages(messages)
  host.registerComponent('BedrockAccountForm', BedrockAccountForm)
}

export function unregister() {
  setHost(null)
}
