// The state the mocks of the accounts list specs share (testing/accountsView.ts):
// the mocked requests and stores, and the list the view reads.
import { vi } from 'vitest'
import { reactive, ref } from 'vue'
import type { Account } from '@/api/types'
import type { PluginAccountActionEntry, SlotEntry } from '@/stores/plugins'

export const mocks = { get: vi.fn(), post: vi.fn(), patch: vi.fn(), findType: vi.fn(), confirm: vi.fn(), toast: vi.fn(), notifyError: vi.fn(), reload: vi.fn(), canUpdate: true, scope: 'all' as 'all' | 'team' | 'own', slotEntries: vi.fn((_slot: string) => [] as SlotEntry[]), accountActions: [] as PluginAccountActionEntry[], groups: [] as { id: number; name: string; platforms?: string[] }[] }
export const list = { items: ref<Account[]>([]), total: ref(1), page: ref(1), pageSize: ref(20), loading: ref(false), error: ref(''), filters: reactive<Record<string, string>>({}), reload: mocks.reload }
