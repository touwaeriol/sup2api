import type { PluginHost } from '@sub2api/host'
import { forms } from './forms'
import messages from './messages'

// Native UI entry of the plugin (manifest ui.native.entry): its account
// forms, registered under the names manifest accountTypes[].form.component
// gives. Not wrapped in lazyPage: the account editor calls validate()
// through a template ref, which an async wrapper would not forward.
export function register(host: PluginHost) {
  host.addMessages(messages)
  for (const [name, form] of Object.entries(forms((key) => host.t(key)))) host.registerComponent(name, form)
}
