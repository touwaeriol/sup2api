// Azure OpenAI UI messages, registered under plugin.azure-openai.* via host.addMessages.
// vue-i18n syntax: avoid the special characters @ | { } in plain text.
const zh = {
  baseUrl: {
    label: '资源地址',
    placeholder: 'https://<资源名>.openai.azure.com',
    pattern: '只填资源地址，不要带 deployments、chat/completions 或 api-version',
    helpV1: 'Azure OpenAI 资源的地址，请求发往 <资源地址>/openai/v1/chat/completions、/responses、/embeddings；地址末尾带 /openai/v1 或 /openai 也可以：每次请求都从资源地址按接口重新拼出路径。',
    helpDeployment: 'Azure OpenAI 资源的地址，请求发往 <资源地址>/openai/deployments/<部署名>/chat/completions、/embeddings，部署名即账号的上游模型；地址末尾带 /openai/v1 或 /openai 也可以：每次请求都从资源地址按接口重新拼出路径。'
  },
  apiKey: { help: '资源的 Key，以 api-key 发送；加密保存，保存后不再明文显示。' },
  apiVersion: {
    label: 'API 版本',
    help: '作为查询参数 api-version 发送，例如 2024-10-21、2025-04-01-preview。'
  }
}

type Messages = typeof zh

const en: Messages = {
  baseUrl: {
    label: 'Resource endpoint',
    placeholder: 'https://<resource>.openai.azure.com',
    pattern: 'Enter the resource endpoint only, without deployments, chat/completions or api-version',
    helpV1: 'Endpoint of the Azure OpenAI resource; requests go to <resource endpoint>/openai/v1/chat/completions, /responses and /embeddings. A trailing /openai/v1 or /openai is fine: each request rebuilds the path from the resource address.',
    helpDeployment: "Endpoint of the Azure OpenAI resource; requests go to <resource endpoint>/openai/deployments/<deployment>/chat/completions and /embeddings, where the deployment is the account's upstream model. A trailing /openai/v1 or /openai is fine: each request rebuilds the path from the resource address."
  },
  apiKey: { help: 'Key of the resource, sent as api-key. Stored encrypted and never shown again.' },
  apiVersion: {
    label: 'API version',
    help: 'Sent as the api-version query parameter, e.g. 2024-10-21 or 2025-04-01-preview.'
  }
}

export default { zh, en }
