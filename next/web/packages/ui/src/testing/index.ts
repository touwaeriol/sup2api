// @sub2api/ui/testing: helpers for the specs of the console and of native
// plugin UIs. Specs only: it is not in the console's import map (a plugin's
// runtime code must not import it) and it needs vitest and @vue/test-utils.
// A plugin's vitest config takes the alias from the console's
// (web/vitest.config.ts); its tsconfig maps the path
// "@sub2api/ui/testing" to web/packages/ui/src/testing/index.ts.
export { mountPinned } from '../testViewport'
export { cleanup, fakeHost, messagesT } from './host'
export { control, fillControls, keyAndUrlCases, mountAccountForm } from './accountForm'
export type { AccountFormCase, MountedAccountForm } from './accountForm'
export { fakeCore, mountSettings } from './settings'
export type { FakeCore, MountedSettings } from './settings'
export { checkMessages } from './messages'
