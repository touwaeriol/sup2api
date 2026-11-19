// Relay UI messages, registered under plugin.relay.* via host.addMessages.
// vue-i18n syntax: avoid the special characters @ | { } in plain text.
const zh = {
  baseUrl: {
    help: 'Anthropic 兼容中转站的地址，请求发往 <Base URL>/v1/messages；地址已以 /v1 结尾时不再重复添加。'
  },
  apiKey: { help: '中转站发放的 Key，以 x-api-key 发送；加密保存，保存后不再明文显示。' }
}

type Messages = typeof zh

const en: Messages = {
  baseUrl: {
    help: 'Address of the Anthropic-compatible relay; requests go to <Base URL>/v1/messages; if the address already ends in /v1, it is not added again.'
  },
  apiKey: { help: 'Key issued by the relay, sent as x-api-key. Stored encrypted and never shown again.' }
}

export default { zh, en }
