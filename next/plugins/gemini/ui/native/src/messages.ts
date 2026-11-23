// Gemini UI messages, registered under plugin.gemini.* via host.addMessages.
// vue-i18n syntax: avoid the special characters @ | { } in plain text.
const zh = {
  apiKey: { help: 'Google AI Studio（Gemini API）的 Key，以 x-goog-api-key 发送；加密保存，保存后不再明文显示。' },
  baseUrl: {
    placeholder: '留空使用默认地址 https://generativelanguage.googleapis.com',
    help: '使用 Google 官方接口时保持默认；也可以填写 Gemini 兼容端点，请求发往 <Base URL>/<版本>/models/<模型>:<操作>，版本跟随客户端调用的 /v1beta 或 /v1（转换来的请求用 /v1beta）；地址末尾带 /v1beta 或 /v1 也可以，请求时这一段换成该版本。'
  }
}

type Messages = typeof zh

const en: Messages = {
  apiKey: { help: 'Google AI Studio (Gemini API) key, sent as x-goog-api-key. Stored encrypted and never shown again.' },
  baseUrl: {
    placeholder: 'Leave empty to use https://generativelanguage.googleapis.com',
    help: 'Leave the default for Google, or enter a Gemini-compatible endpoint; requests go to <Base URL>/<version>/models/<model>:<action>, the version being the /v1beta or /v1 the client called (/v1beta for a converted request). A trailing /v1beta or /v1 is fine: requests replace that one segment with the version.'
  }
}

export default { zh, en }
