import type { Component } from 'vue'
import { createApiKeyForm } from '@sub2api/ui'

/**
 * The account forms by component name (manifest accountTypes[].form.component).
 * `t` looks up this plugin's messages; texts are functions so they follow
 * the console's language. The Go side (ValidateCredentials) checks the same
 * rules and is final.
 */
export function forms(t: (key: string) => string): Record<string, Component> {
  return {
    AnthropicKeyForm: createApiKeyForm({
      apiKey: { placeholder: () => 'sk-ant-...', help: () => t('apiKey.help') },
      baseUrl: { presets: ['https://api.anthropic.com'], placeholder: () => t('baseUrl.placeholder'), help: () => t('baseUrl.help') }
    })
  }
}
