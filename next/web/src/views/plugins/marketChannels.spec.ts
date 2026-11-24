import { afterEach, describe, expect, it, vi } from 'vitest'
import { channel, store } from '@/testing/marketFixtures'
import { REMEMBERED, pickChannel, pickStore, rememberChannel, rememberStore } from './marketChannels'

describe('market stores and channels', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    localStorage.clear()
  })

  it('opens the preferred store, else the official one, else the first', () => {
    const list = [store(2), store(1), store(3)]
    expect(pickStore(list, 3)?.id).toBe(3)
    expect(pickStore(list, 9)?.id).toBe(1)
    expect(pickStore(list, null)?.id).toBe(1)
    expect(pickStore([store(4), store(5)], null)?.id).toBe(4)
    expect(pickStore([], null)).toBeNull()
  })

  it('opens the preferred enabled channel, else the default, else the first enabled one', () => {
    const list = [channel(1, { is_default: false }), channel(2, { is_default: true }), channel(3, { enabled: false })]
    expect(pickChannel(list, 1)?.id).toBe(1)
    expect(pickChannel(list, 3)?.id).toBe(2)
    expect(pickChannel(list, null)?.id).toBe(2)
    expect(pickChannel([channel(4, { enabled: false }), channel(5)], null)?.id).toBe(5)
    expect(pickChannel([channel(6, { enabled: false })], null)).toBeNull()
  })

  it('remembers the viewer store and the channel of each store in this browser', () => {
    expect(REMEMBERED.store()).toBeNull()
    expect(REMEMBERED.channel(1)).toBeNull()
    rememberChannel(1, 2)
    rememberStore(5)
    rememberChannel(5, 9)
    expect(REMEMBERED.store()).toBe(5)
    expect(REMEMBERED.channel(1)).toBe(2)
    expect(REMEMBERED.channel(5)).toBe(9)
  })

  it('ignores what it cannot read and works without browser storage', () => {
    localStorage.setItem('s2a.market', 'not json')
    expect(REMEMBERED.store()).toBeNull()
    const blocked = () => {
      throw new Error('blocked')
    }
    vi.stubGlobal('localStorage', { getItem: blocked, setItem: blocked, clear: () => undefined })
    expect(() => rememberStore(2)).not.toThrow()
    expect(() => rememberChannel(2, 3)).not.toThrow()
    expect(REMEMBERED.channel(2)).toBeNull()
  })
})
