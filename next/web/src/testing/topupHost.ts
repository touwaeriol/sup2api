import { ref } from 'vue'
import { vi } from 'vitest'
import { provideHost, type HostContext } from '@sub2api/host'
import { formatMoney } from '@/utils/format'

/**
 * The console the top-up views read through useTopup() in a spec: `t`
 * answers the key (with its params as JSON, so a spec can find a value in
 * it), money is formatted as the console does, toast and confirm are spies
 * (confirm says yes). The API is mocked by the spec (`api` of @sub2api/host).
 */
export function provideTopupHost() {
  const toast = vi.fn()
  const confirm = vi.fn(async () => true)
  const t = (key: string, params?: Record<string, unknown>) => (params ? `${key} ${JSON.stringify(params)}` : key)
  const i18n = { locale: ref('en'), t, text: (v: Record<string, string>) => v.en, addMessages: () => {}, formatMoney, formatNumber: String, formatDateTime: String }
  provideHost({ i18n, toast, confirm, permissions: { has: () => true, any: () => true, superuser: () => false } } as unknown as HostContext)
  return { toast, confirm }
}
