/// <reference types="vite/client" />
import { describe } from 'vitest'
import { checkMessages } from '@sub2api/ui/testing'
import messages from './messages'

describe('messages', () =>
  checkMessages(messages, {
    sources: import.meta.glob<string>(['./*.vue', './*.ts', '!./*.spec.ts', '!./messages.ts'], { query: '?raw', import: 'default', eager: true })
  }))
