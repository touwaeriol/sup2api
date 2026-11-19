/// <reference types="vite/client" />
import { describe } from 'vitest'
import { checkMessages } from '@sub2api/ui/testing'
import messages from './messages'

// zh and en have the same keys (a missing one shows the raw key), no text
// holds a vue-i18n special character, and the forms use every key.
describe('messages', () =>
  checkMessages(messages, { sources: import.meta.glob<string>(['./*.ts', '!./*.spec.ts', '!./messages.ts'], { query: '?raw', import: 'default', eager: true }) }))
