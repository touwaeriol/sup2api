import { createApiKeyForm } from '@sub2api/ui'
import { TARGET } from './fields'

/**
 * The account form of vertex_apikey (manifest accountTypes[].form
 * {mode: "native", component: "VertexApiKeyForm"}, CONTRACTS §23.5): the API
 * key, then the optional project address (project ID and location, both or
 * neither; neither is the express address, ValidateCredentials checks the
 * pair). `t` looks up this plugin's messages.
 */
export function apiKeyForm(t: (key: string) => string) {
  const section = { title: () => t('express.title'), help: () => t('express.description') }
  const placeholder = () => t('express.placeholder')
  return createApiKeyForm({
    apiKey: { help: () => t('apiKey.help') },
    baseUrl: false,
    extra: [
      { key: 'project_id', kind: 'text', label: () => t('express.project'), placeholder, rules: TARGET.project_id, section, mono: true },
      { key: 'location', kind: 'text', label: () => t('express.location'), help: () => t('location.help'), placeholder, rules: TARGET.location, mono: true }
    ]
  })
}
