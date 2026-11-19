/// <reference types="vite/client" />
import { describe, expect, it } from 'vitest'
import { checkMessages } from '@sub2api/ui/testing'
import messages from './messages'
import { KNOWN_PLANS } from './flow'

describe('messages', () => {
  checkMessages(messages, {
    sources: import.meta.glob<string>(['./*.vue', './*.ts', '!./*.spec.ts', '!./messages.ts'], { query: '?raw', import: 'default', eager: true }),
    // Keys looked up with a computed suffix: t(`tabs.${f}`), t(`plan.${p}`), t(`oauth.shape.${s}`), t(`reset.window${key}`).
    dynamic: ['tabs.', 'plan.', 'oauth.shape.', 'reset.window']
  })

  it('names every known plan', () => {
    for (const lang of [messages.zh, messages.en]) expect(KNOWN_PLANS.filter((p) => !(p in lang.plan))).toEqual([])
  })
})
