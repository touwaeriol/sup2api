// TypeSafe UI messages, registered under plugin.typesafe.* via host.addMessages.
// vue-i18n syntax: avoid the special characters @ | { } in plain text.
const zh = {
  apiKey: { help: 'TypeSafe 的 API Key，以 Authorization: Bearer 发送；加密保存，保存后不再明文显示。' },
  baseUrl: {
    placeholder: '留空使用默认地址 https://api.typesafe.ai',
    help: '使用 TypeSafe 官方接口时保持默认；请求发往 <Base URL>/v1/systemone；地址已以 /v1 结尾时不再重复添加。'
  }
}

type Messages = typeof zh

const en: Messages = {
  apiKey: { help: 'TypeSafe API key, sent as Authorization: Bearer. Stored encrypted and never shown again.' },
  baseUrl: {
    placeholder: 'Leave empty to use https://api.typesafe.ai',
    help: 'Leave the default for TypeSafe; requests go to <Base URL>/v1/systemone; if the address already ends in /v1, it is not added again.'
  }
}

export default { zh, en }
