import type { Component } from 'vue'
import { createApiKeyForm } from '@sub2api/ui'

/**
 * The account forms by component name (manifest accountTypes[].form.component).
 * `t` looks up this plugin's messages; texts are functions so they follow
 * the console's language. The Go side (ValidateCredentials) checks the same
 * rules and is final.
 */
export function forms(t: (key: string) => string): Record<string, Component> {
  // The resource endpoint: a host, optionally with /, /openai, /openai/v1 or
  // a Foundry project path; nothing after it (Go: azureopenai.EndpointPattern).
  const endpoint = (help: string) => ({
    required: true,
    first: true,
    pattern: /^https?:\/\/[^\s/?#@]+(\/|\/openai\/?|\/openai\/v1\/?|\/api\/projects\/[^\s/?#]+\/?)?$/,
    message: () => t('baseUrl.pattern'),
    label: () => t('baseUrl.label'),
    placeholder: () => t('baseUrl.placeholder'),
    help: () => t(help)
  })
  const apiKey = { help: () => t('apiKey.help') }
  return {
    AzureV1Form: createApiKeyForm({ baseUrl: endpoint('baseUrl.helpV1'), apiKey }),
    AzureDeploymentForm: createApiKeyForm({
      baseUrl: endpoint('baseUrl.helpDeployment'),
      apiKey,
      defaults: { api_version: '2024-10-21' },
      extra: [
        {
          key: 'api_version',
          kind: 'text',
          label: () => t('apiVersion.label'),
          placeholder: () => '2024-10-21',
          help: () => t('apiVersion.help'),
          rules: { required: true, pattern: /^\d{4}-\d{2}-\d{2}(-preview)?$/ }
        }
      ]
    })
  }
}
