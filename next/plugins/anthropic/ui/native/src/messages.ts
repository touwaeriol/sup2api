// Anthropic UI messages, registered under plugin.anthropic.* via host.addMessages.
// vue-i18n syntax: avoid the special characters @ | { } in plain text.
const zh = {
  apiKey: { help: 'Anthropic 控制台的 API Key，加密保存，保存后不再明文显示。' },
  baseUrl: {
    placeholder: '留空使用默认地址 https://api.anthropic.com',
    help: '使用 Anthropic 官方接口时保持默认；也可以填写 Anthropic 兼容端点，请求发往 <Base URL>/v1/messages；地址已以 /v1 结尾时不再重复添加。'
  }
}

type Messages = typeof zh

const en: Messages = {
  apiKey: { help: 'Anthropic Console API key. Stored encrypted and never shown again.' },
  baseUrl: {
    placeholder: 'Leave empty to use https://api.anthropic.com',
    help: 'Leave the default for Anthropic, or enter an Anthropic-compatible endpoint; requests go to <Base URL>/v1/messages; if the address already ends in /v1, it is not added again.'
  }
}

export default { zh, en }
