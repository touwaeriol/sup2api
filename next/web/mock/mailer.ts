// Mock of a plugin whose settings are native and have no page of their own
// (manifest ui.settings {mode: "native", component}, DESIGN-2026-11-19-
// PLUGIN-UI-CODE-ONLY §5.11): its native module only registers the settings
// component, which the console mounts on the plugin's settings tab. The
// component is written with the UI SDK alone (usePluginSettings +
// SSettingsPage), as a plugin's ui/native would be; it is hand-written here
// (no build step). The settings themselves live in plugins.ts.

const VERSION = '0.2.0'

export const mailerUIPlugin = {
  key: 'mailer',
  version: VERSION,
  name: { en: 'Mailer', zh: '邮件' },
  asset_base: `/plugin-ui/mailer/${VERSION}-dev`,
  menus: [],
  pages: {},
  slots: [],
  native_entry: 'ui/native/entry.js',
  trust: 'official',
  host_ui_compat: '^4.0'
}

/** GET /plugins/mailer/settings: native, no page; the password is the one secret. */
export const mailerSettings = {
  mode: 'native',
  component: 'MailerSettings',
  values: { host: 'smtp.example.com', port: 587, security: 'starttls', username: 'notify@example.com', password: '******', from_address: 'notify@example.com', from_name: 'Sub2API' },
  secret_fields: ['password']
}

const ENTRY = `
import { defineComponent, h } from 'vue'
import { SInput, SSecretInput, SSelect, SSettingRow, SSettingsGroup, SSettingsPage, usePluginSettings } from '@sub2api/ui'

const messages = {
  en: { server: 'SMTP server', sender: 'Sender', host: 'Host', port: 'Port', security: 'Security', username: 'Username', password: 'Password', fromAddress: 'From address', fromName: 'From name' },
  zh: { server: 'SMTP 服务器', sender: '发件人', host: '主机', port: '端口', security: '加密方式', username: '用户名', password: '密码', fromAddress: '发件地址', fromName: '发件人名称' }
}

export function register(host) {
  host.addMessages(messages)
  host.registerComponent('MailerSettings', defineComponent({
    setup() {
      const settings = usePluginSettings({
        pluginKey: host.plugin.key,
        api: host.api,
        defaults: { port: 587, security: 'starttls', from_name: 'Sub2API' },
        rules: {
          host: { max: 253 },
          port: { required: true, type: 'integer', min: 1, max: 65535 },
          security: { enum: ['starttls', 'tls', 'none'] },
          username: { max: 320 },
          password: { max: 1000 },
          from_address: { type: 'email', max: 320 },
          from_name: { max: 100 }
        }
      })
      const security = [{ value: 'starttls', label: 'STARTTLS' }, { value: 'tls', label: 'TLS' }, { value: 'none', label: 'None' }]
      const row = (key, label, control) => h(SSettingRow, { label: host.t(label), prop: key }, () => control)
      return () => h(SSettingsPage, { settings }, () => [
        h(SSettingsGroup, { title: host.t('server') }, () => [
          row('host', 'host', h(SInput, settings.bind('host'))),
          row('port', 'port', h(SInput, { type: 'number', ...settings.bind('port') })),
          row('security', 'security', h(SSelect, { options: security, ...settings.bind('security') })),
          row('username', 'username', h(SInput, settings.bind('username'))),
          row('password', 'password', h(SSecretInput, { clearable: true, ...settings.bind('password') }))
        ]),
        h(SSettingsGroup, { title: host.t('sender') }, () => [
          row('from_address', 'fromAddress', h(SInput, settings.bind('from_address'))),
          row('from_name', 'fromName', h(SInput, settings.bind('from_name')))
        ])
      ])
    }
  }))
}
`

export function mailerAsset(path: string): { type: string; body: string } | null {
  return path === 'ui/native/entry.js' ? { type: 'text/javascript', body: ENTRY } : null
}
