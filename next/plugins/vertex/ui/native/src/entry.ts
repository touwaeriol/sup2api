import type { PluginHost } from '@sub2api/host'
import VertexKeyFileForm from './VertexKeyFileForm.vue'
import messages from './messages'
import { setHost } from './host'
import { apiKeyForm } from './apiKeyForm'

// Native UI entry of the vertex plugin (manifest ui.native.entry). The
// component names match manifest accountTypes[].form.component; they are
// not wrapped in lazyPage, since the account editor calls validate() through
// a template ref. The key file types share one form (their defaults are the
// same); the API key type has its own (createApiKeyForm), so switching between them while
// creating mounts a fresh form with its own defaults.
export function register(host: PluginHost) {
  setHost(host)
  host.addMessages(messages)
  host.registerComponent('VertexKeyFileForm', VertexKeyFileForm)
  host.registerComponent('VertexApiKeyForm', apiKeyForm((key) => host.t(key)))
}

export function unregister() {
  setHost(null)
}
