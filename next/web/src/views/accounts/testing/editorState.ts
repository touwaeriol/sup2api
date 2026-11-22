// The state the mocks of the account editor specs share (testing/accountEditor.ts):
// the mocked requests, the caller's scope and permissions, and the plugin store.
import { vi } from 'vitest'

export const mocks = { get: vi.fn(), post: vi.fn(), patch: vi.fn(), put: vi.fn(), list: vi.fn().mockResolvedValue({ items: [] }), toast: vi.fn() }
export const ownership = { scope: 'all' as 'all' | 'own' }
export const pluginStore = { listed: true, pending: {} as Record<string, boolean>, errors: {} as Record<string, string>, plugin: () => null, uiMismatch: () => null, component: (() => null) as (key: string, name?: string) => unknown, slotEntries: (() => []) as (slot: string) => unknown[], refresh: vi.fn() }
export const perms = { denied: [] as string[] }
