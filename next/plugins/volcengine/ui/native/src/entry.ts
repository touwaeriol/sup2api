import type { PluginHost } from '@sub2api/host'
import { ApiKeyForm } from './apikeyForm'
import RelayForm from './RelayForm.vue'
import { AssetGroupsPage, AssetsPage } from './pages'
import messages from './messages'
import { setHost } from './host'
import './volcengine.css'

// Native UI entry of the volcengine plugin (manifest ui.native.entry). The
// names match manifest accountTypes[].form.component and ui.pages.*.component.
// The forms are not wrapped in lazyPage: the account editor calls validate()
// through a template ref, which an async wrapper would not forward.
export function register(host: PluginHost) {
  setHost(host)
  host.addMessages(messages)
  host.registerComponent('VolcengineKeyForm', ApiKeyForm)
  host.registerComponent('VolcengineRelayForm', RelayForm)
  host.registerComponent('VolcengineAssetsPage', AssetsPage)
  host.registerComponent('VolcengineAssetGroupsPage', AssetGroupsPage)
}

export function unregister() {
  setHost(null)
}
