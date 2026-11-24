import { afterEach, describe, expect, it, vi } from 'vitest'
import type { MarketSource } from '@/api/marketTypes'
import { pickChannel, rememberChannel, rememberedChannel } from './marketChannels'

const channel = (id: number, over: Partial<MarketSource> = {}): MarketSource => ({
  id,
  name: `c${id}`,
  branch: `b${id}`,
  url: `https://x/index-b${id}/index.json`,
  enabled: true,
  is_default: false,
  ...over
})

describe('market channels', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    localStorage.clear()
  })

  it('opens the preferred enabled channel, else the default, else the first enabled one', () => {
    const list = [channel(1), channel(2, { is_default: true }), channel(3, { enabled: false })]
    expect(pickChannel(list, 1)?.id).toBe(1)
    expect(pickChannel(list, 3)?.id).toBe(2)
    expect(pickChannel(list, null)?.id).toBe(2)
    expect(pickChannel([channel(4, { enabled: false }), channel(5)], null)?.id).toBe(5)
    expect(pickChannel([channel(6, { enabled: false })], null)).toBeNull()
  })

  it('remembers the viewer channel in this browser', () => {
    expect(rememberedChannel()).toBeNull()
    rememberChannel(7)
    expect(rememberedChannel()).toBe(7)
  })

  it('works without browser storage', () => {
    const blocked = () => {
      throw new Error('blocked')
    }
    vi.stubGlobal('localStorage', { getItem: blocked, setItem: blocked, clear: () => undefined })
    expect(() => rememberChannel(2)).not.toThrow()
    expect(rememberedChannel()).toBeNull()
  })
})
