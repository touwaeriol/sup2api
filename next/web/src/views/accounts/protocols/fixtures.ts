// Fixtures of the protocol specs: the account types GET /account-types lists
// for an OpenAI API key, a Codex subscription and a Claude account, and a
// mount around the editor state.
import { defineComponent, type VNode } from 'vue'
import { mountPinned } from '@sub2api/ui/testing'
import type { Account, AccountType, AccountTypeEndpoint, ConversionGrade } from '@/api/types'
import { i18n } from '@/i18n'
import { useProtocolEdit, type ProtocolEditor } from './useProtocolEdit'

export const ep = (protocol: string, native: boolean, grade: ConversionGrade = 'native', via?: string[]): AccountTypeEndpoint => ({
  method: 'POST',
  path: `/${protocol}`,
  protocol,
  platform: protocol.split('.')[0],
  native,
  grade,
  via
})

export const type = (platforms: string[], endpoints: AccountTypeEndpoint[], over: Partial<AccountType> = {}) =>
  ({ plugin_key: platforms[0], type: 'apikey', form: { mode: 'native', component: 'ApiKeyForm' }, default_models: [], platforms: platforms.map(id => ({ id, label: { en: id }, builtin: true, available: true })), endpoints, ...over }) as unknown as AccountType

/** An OpenAI API key type: four own protocols, Anthropic and Gemini through conversion, count_tokens estimated. */
export const openai = type(['openai'], [
  ep('openai.chat', true),
  ep('openai.responses', true),
  ep('openai.responses_ws', true),
  ep('openai.embeddings', true),
  ep('anthropic.count_tokens', false, 'local'),
  ep('anthropic.messages', false, 'fair', ['anthropic.messages', 'openai.chat']),
  ep('gemini.count_tokens', false, 'local'),
  ep('gemini.generate', false, 'fair', ['gemini.generate', 'openai.chat']),
  ep('gemini.stream_generate', false, 'fair', ['gemini.stream_generate', 'openai.chat'])
])

/** A Codex type: Responses only, chat adapted to it. */
export const codex = type(['openai'], [
  ep('openai.responses', true),
  ep('openai.chat', false, 'good', ['openai.chat', 'openai.responses']),
  ep('anthropic.messages', false, 'fair', ['anthropic.messages', 'openai.responses'])
])

/** A Claude type: Gemini is two conversions away. */
export const claude = type(['anthropic'], [
  ep('anthropic.messages', true),
  ep('anthropic.count_tokens', true),
  ep('openai.chat', false, 'fair', ['openai.chat', 'anthropic.messages']),
  ep('gemini.generate', false, 'discouraged', ['gemini.generate', 'openai.chat', 'anthropic.messages']),
  ep('gemini.stream_generate', false, 'discouraged', ['gemini.stream_generate', 'openai.chat', 'anthropic.messages'])
])

/**
 * Mounts what `render` draws around an editor state (useProtocolEdit) loaded
 * with `saved`, on a wide screen unless `compact`; returns the wrapper and
 * the state.
 */
export function mountEditor(
  render: (editor: ProtocolEditor) => VNode,
  { accountType = openai, models = [], saved = null, compact = false }: { accountType?: AccountType | null; models?: string[]; saved?: Pick<Account, 'protocols' | 'model_protocols'> | null; compact?: boolean } = {}
) {
  let editor!: ProtocolEditor
  const Host = defineComponent({
    setup() {
      editor = useProtocolEdit(() => accountType, () => models)
      editor.setSaved(saved)
      return () => render(editor)
    }
  })
  const w = mountPinned(Host, { global: { plugins: [i18n] } }, { compact })
  return { w, editor }
}
