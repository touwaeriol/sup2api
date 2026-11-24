// Installed third-party plugins (plugin ids "_<publisher>__<key>", CONTRACTS
// §137): erio/codex from the store, a different plugin from the official
// codex, and ourteam/billing_bridge, a manual upload awaiting consent.
import { grant, plugins, type MockPlugin } from './plugins'

const base = { status_reason: '', egress_policy: 'allow_all', settings: null }

const erioCodex: MockPlugin = {
  ...base,
  key: '_erio__codex',
  name: { en: 'Codex Plus', zh: 'Codex 增强版' },
  description: { en: 'A community Codex plugin.', zh: '第三方 Codex 插件。' },
  status: 'enabled',
  active_version: '1.2.0',
  desired_version: '1.2.0',
  publisher: 'erio',
  trust: 'community',
  signature_status: 'indexed',
  resources: { memory_mb: 128, cpu: 0.25 },
  versions: [{ version: '1.2.0', consent_status: 'approved' }],
  grants: [grant('kv', null, '1.2.0'), grant('net', { domains: ['chatgpt.com'] }, '1.2.0')],
  manifest: { key: 'codex', version: '1.2.0', publisher: 'erio', hostCompat: '>=0.1.4' }
}

const billingBridge: MockPlugin = {
  ...base,
  key: '_ourteam__billing_bridge',
  name: { en: 'Billing bridge', zh: '内部计费桥' },
  description: { en: 'Sends ledger entries to the internal billing system.', zh: '把账本记录同步到内部计费系统。' },
  status: 'awaiting_consent',
  active_version: null,
  desired_version: '0.1.0',
  publisher: 'ourteam',
  trust: 'verified',
  resources: {},
  versions: [{ version: '0.1.0', consent_status: 'pending' }],
  grants: [],
  manifest: { key: 'billing_bridge', version: '0.1.0', publisher: 'ourteam', hostCompat: '>=0.1.0' }
}

for (const p of [erioCodex, billingBridge]) plugins.set(p.key, p)
