import { ref } from 'vue'
import { api } from '@sub2api/host'
import type { LText, Platform, PlatformEndpoint } from '@/api/types'
import { lt } from '@/i18n'

// Gateway platforms: GET /me/platforms (CONTRACTS §14.1) lists every
// available platform and its endpoints to any signed-in user. When it fails,
// the built-in catalog below is used; plugin platforms then show their id
// only.

const ep = (id: string, method: string, path: string, protocol: string, billing = 'usage'): PlatformEndpoint => ({ id, method, path, protocol, billing })

/** Built-in platforms of the core (server/internal/platforms), used as fallback. */
export const BUILTIN_PLATFORMS: Platform[] = [
  {
    id: 'anthropic',
    label: { en: 'Anthropic', zh: 'Anthropic' },
    builtin: true,
    endpoints: [ep('messages', 'POST', '/v1/messages', 'anthropic.messages'), ep('count_tokens', 'POST', '/v1/messages/count_tokens', 'anthropic.count_tokens', 'free')]
  },
  {
    id: 'openai',
    label: { en: 'OpenAI', zh: 'OpenAI' },
    builtin: true,
    endpoints: [
      ep('chat_completions', 'POST', '/v1/chat/completions', 'openai.chat'),
      ep('responses', 'POST', '/v1/responses', 'openai.responses'),
      ep('embeddings', 'POST', '/v1/embeddings', 'openai.embeddings')
    ]
  },
  {
    id: 'gemini',
    label: { en: 'Gemini', zh: 'Gemini' },
    builtin: true,
    endpoints: [
      ep('generate_content', 'POST', '/v1beta/models/:model:generateContent', 'gemini.generate'),
      ep('stream_generate_content', 'POST', '/v1beta/models/:model:streamGenerateContent', 'gemini.stream_generate'),
      ep('count_tokens', 'POST', '/v1beta/models/:model:countTokens', 'gemini.count_tokens', 'free')
    ]
  }
]

const platforms = ref<Platform[]>([])
const loaded = ref(false)
/** true when the list came from the server (plugin platforms included). */
const complete = ref(false)
let pending: Promise<void> | null = null

export function usePlatforms() {
  /** Loads the list once for every caller; a failed load is tried again by the next one. */
  function load(): Promise<void> {
    pending ??= api
      .get<Platform[]>('/me/platforms')
      .then((list) => {
        platforms.value = (Array.isArray(list) ? list : []).map((p) => ({ ...p, endpoints: p.endpoints || [] }))
        complete.value = true
      })
      .catch(() => {
        platforms.value = BUILTIN_PLATFORMS
        complete.value = false
        pending = null
      })
      .finally(() => {
        loaded.value = true
      })
    return pending
  }

  function find(id: string): Platform | undefined {
    return platforms.value.find((p) => p.id === id) || BUILTIN_PLATFORMS.find((p) => p.id === id)
  }

  /** Display label of a platform id; `fallback` (e.g. from the payload) wins over the raw id. */
  function label(id: string, fallback?: LText | null): string {
    const p = find(id)
    return (p && lt(p.label)) || (fallback ? lt(fallback) : '') || id
  }

  /** Endpoints of the given platforms, grouped by platform, unknown ids kept with no endpoints. */
  function endpointsOf(ids: string[]): Array<{ id: string; label: string; builtin: boolean; endpoints: PlatformEndpoint[]; known: boolean }> {
    return ids.map((id) => {
      const p = find(id)
      return { id, label: label(id), builtin: p?.builtin ?? false, endpoints: p?.endpoints || [], known: !!p }
    })
  }

  return { platforms, loaded, complete, load, find, label, endpointsOf }
}
