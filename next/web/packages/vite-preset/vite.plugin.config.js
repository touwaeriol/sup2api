// Build config of the plugin UI projects in this repository: run from the
// project directory, the root (src/entry.ts -> dist/entry.js), with the
// console's vite, `node ../../../../web/node_modules/vite/bin/vite.js build
// --config ../../../../web/packages/vite-preset/vite.plugin.config.js`.
import { sub2apiPlugin } from './index.js'

export default sub2apiPlugin()
