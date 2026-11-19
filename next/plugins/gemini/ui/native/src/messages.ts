// Gemini UI messages, registered under plugin.gemini.* via host.addMessages.
// vue-i18n syntax: avoid the special characters @ | { } in plain text.
const zh = {
  apiKey: { help: 'Google AI Studio（Gemini API）的 Key，以 x-goog-api-key 发送；加密保存，保存后不再明文显示。' },
  baseUrl: {
    placeholder: '留空使用默认地址 https://generativelanguage.googleapis.com',
    help: '使用 Google 官方接口时保持默认；也可以填写 Gemini 兼容端点，请求发往 <Base URL>/v1beta/models/<模型>:<操作>；地址末尾带 /v1beta 或 /v1 也可以，请求时这一段换成 /v1beta。'
  }
}

type Messages = typeof zh

const en: Messages = {
  apiKey: { help: 'Google AI Studio (Gemini API) key, sent as x-goog-api-key. Stored encrypted and never shown again.' },
  baseUrl: {
    placeholder: 'Leave empty to use https://generativelanguage.googleapis.com',
    help: 'Leave the default for Google, or enter a Gemini-compatible endpoint; requests go to <Base URL>/v1beta/models/<model>:<action>. A trailing /v1beta or /v1 is fine: requests replace that one segment with /v1beta.'
  }
}

export default { zh, en }
