/// <reference types="vite/client" />
import { describe, expect, it } from 'vitest'
import { checkMessages } from '@sub2api/ui/testing'
import messages from './messages'

describe('messages', () => {
  checkMessages(messages, {
    sources: import.meta.glob<string>(['./*.vue', './*.ts', '!./*.spec.ts', '!./*.cases.ts', '!./messages.ts'], { query: '?raw', import: 'default', eager: true }),
    // Looked up with the account type in the key: t(`keyFile.${type}.label`), t(`project.help.${type}`).
    dynamic: ['keyFile.', 'project.help.']
  })

  it('names every key file type', () => {
    for (const lang of [messages.zh, messages.en]) {
      expect(Object.keys(lang.keyFile)).toEqual(['vertex_sa', 'vertex_authorized_user'])
      expect(Object.keys(lang.project.help)).toEqual(['vertex_sa', 'vertex_authorized_user'])
    }
  })
})
