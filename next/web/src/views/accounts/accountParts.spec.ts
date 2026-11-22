import { describe, expect, it } from 'vitest'
import { ref, toValue } from 'vue'
import { accountParts } from './accountParts'
import type { AccountSaveDeps } from './useAccountSave'

/** The deps of the connection part: a named account, and a plugin form that answers `credentials`. */
function deps(credentials: Record<string, unknown> | null): AccountSaveDeps {
  return {
    account: () => null,
    accountType: () => null,
    basic: { name: 'a', group_ids: [], proxy_id: null },
    errors: ref({}),
    credErrors: ref({}),
    proxyUrlToSend: () => '',
    collectCredentials: async () => credentials
  } as unknown as AccountSaveDeps
}
const connection = (d: AccountSaveDeps) => accountParts(d, (key) => key)[0]

describe('the connection part of the account editor', () => {
  it('marks its section while the plugin form refuses the credentials, which shows why itself', async () => {
    const refused = connection(deps(null))
    expect(toValue(refused.invalid)).toBe(false)
    expect(await refused.check!()).toBe(false)
    expect(toValue(refused.invalid)).toBe(true)
  })

  it('sends the credentials the plugin form gave', async () => {
    const taken = connection(deps({ api_key: 'k' }))
    expect(await taken.check!()).toBe(true)
    expect(toValue(taken.invalid)).toBe(false)
  })
})
