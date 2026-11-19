import type { Component } from 'vue'
import { createApiKeyForm } from '@sub2api/ui'

/**
 * The account forms by component name (manifest accountTypes[].form.component).
 * `t` looks up this plugin's messages; texts are functions so they follow
 * the console's language. The Go side (ValidateCredentials) checks the same
 * rules and is final.
 */
export function forms(t: (key: string) => string): Record<string, Component> {
  // Organization and project: up to 128 visible ASCII characters, empty allowed.
  const header = [{ max: 128 }, { pattern: /^[!-~]*$/ }]
  // Closed at first, open while either holds a value or an error.
  const advanced = { title: () => t('advanced'), collapsed: true }
  return {
    OpenAIKeyForm: createApiKeyForm({
      apiKey: { placeholder: () => 'sk-...', help: () => t('apiKey.help') },
      baseUrl: { presets: ['https://api.openai.com'], placeholder: () => t('baseUrl.placeholder'), help: () => t('baseUrl.help') },
      extra: [
        { key: 'organization', kind: 'text', section: advanced, label: () => t('organization.label'), placeholder: () => 'org-...', help: () => t('organization.help'), rules: header },
        { key: 'project', kind: 'text', section: advanced, label: () => t('project.label'), placeholder: () => 'proj_...', help: () => t('project.help'), rules: header }
      ]
    })
  }
}
