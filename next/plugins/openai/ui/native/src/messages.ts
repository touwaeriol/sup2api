// OpenAI UI messages, registered under plugin.openai.* via host.addMessages.
// vue-i18n syntax: avoid the special characters @ | { } in plain text.
const zh = {
  apiKey: { help: 'OpenAI 平台的 API Key，以 Authorization: Bearer 发送；加密保存，保存后不再明文显示。' },
  baseUrl: {
    placeholder: '留空使用默认地址 https://api.openai.com',
    help: '使用 OpenAI 官方接口时保持默认；也可以填写 OpenAI 兼容端点，请求发往 <Base URL>/v1/chat/completions、/v1/responses、/v1/embeddings；地址已以 /v1 结尾时不再重复添加。'
  },
  advanced: '高级（可选）',
  organization: {
    label: '组织 ID',
    help: '以 OpenAI-Organization 发送，并覆盖客户端自己带的头；留空则沿用客户端带的头。'
  },
  project: {
    label: '项目 ID',
    help: '以 OpenAI-Project 发送，并覆盖客户端自己带的头；留空则沿用客户端带的头。'
  }
}

type Messages = typeof zh

const en: Messages = {
  apiKey: { help: 'OpenAI Platform API key, sent as Authorization: Bearer. Stored encrypted and never shown again.' },
  baseUrl: {
    placeholder: 'Leave empty to use https://api.openai.com',
    help: 'Leave the default for OpenAI, or enter an OpenAI-compatible endpoint; requests go to <Base URL>/v1/chat/completions, /v1/responses and /v1/embeddings; if the address already ends in /v1, it is not added again.'
  },
  advanced: 'Advanced (optional)',
  organization: {
    label: 'Organization ID',
    help: "Sent as OpenAI-Organization and replaces the header a client sends. Leave empty to pass the client's own header through."
  },
  project: {
    label: 'Project ID',
    help: "Sent as OpenAI-Project and replaces the header a client sends. Leave empty to pass the client's own header through."
  }
}

export default { zh, en }
