// Vertex UI messages, registered under plugin.vertex.* via host.addMessages.
// vue-i18n syntax: avoid the special characters @ | { } in plain text.
const zh = {
  // The key file of each key file type (t(`keyFile.${type}.label`), ...).
  keyFile: {
    vertex_sa: {
      label: '服务账号密钥',
      placeholder: '粘贴整个 JSON 密钥文件',
      help: '服务账号的 JSON 密钥（IAM 和管理 → 服务账号 → 密钥 → 添加密钥 → JSON），该服务账号要有调用 Vertex AI 的权限，例如 Vertex AI User 角色。粘贴整个文件的内容。加密保存；保存后自动获取访问令牌（旧版核心最多等待 5 分钟），之后自动续期。'
    },
    vertex_authorized_user: {
      label: '用户凭证文件',
      placeholder: '粘贴整个 application_default_credentials.json',
      help: '`gcloud auth application-default login` 生成的文件（application_default_credentials.json，"type" 为 "authorized_user"）。粘贴整个文件的内容。加密保存；保存后自动获取访问令牌（旧版核心最多等待 5 分钟），之后自动续期。'
    }
  },
  apiKey: {
    help: 'Vertex AI 的 API Key（Google Cloud 控制台 → Vertex AI → API 密钥）。加密保存。'
  },
  project: {
    label: '项目 ID',
    // The project of each key file type (t(`project.help.${type}`)).
    help: {
      vertex_sa: '留空则使用密钥文件里的 project_id。',
      vertex_authorized_user: '请求计费的项目。只有凭证文件里带 quota_project_id 时才可以留空。'
    },
    fromKeyFile: '留空使用密钥文件里的项目'
  },
  location: {
    label: '区域',
    help: '请求发往的 Vertex AI 区域：global（主机 aiplatform.googleapis.com），或 us-central1、europe-west4、asia-northeast1 这样的区域（主机 <区域>-aiplatform.googleapis.com）。并非每个模型在每个区域都可用。'
  },
  // The optional project address of an API key account.
  express: {
    title: '带项目的地址（可选）',
    description: '项目 ID 和区域要么都填，要么都留空；都留空时使用 express 地址（https://aiplatform.googleapis.com/v1/publishers/google/models/...）。',
    project: '项目 ID（可选）',
    location: '区域（可选）',
    placeholder: '留空使用 express 地址'
  }
}

const en: typeof zh = {
  keyFile: {
    vertex_sa: {
      label: 'Service account key',
      placeholder: 'Paste the whole JSON key file',
      help: 'The JSON key of a service account (IAM & Admin → Service accounts → Keys → Add key → JSON) that may call Vertex AI, e.g. with the role Vertex AI User. Paste the whole file. Stored encrypted; the access token is fetched automatically after saving (an older core can take up to five minutes) and then renewed automatically.'
    },
    vertex_authorized_user: {
      label: 'User credentials file',
      placeholder: 'Paste the whole application_default_credentials.json',
      help: 'The file written by `gcloud auth application-default login` (application_default_credentials.json, "type": "authorized_user"). Paste the whole file. Stored encrypted; the access token is fetched automatically after saving (an older core can take up to five minutes) and then renewed automatically.'
    }
  },
  apiKey: {
    help: 'A Vertex AI API key (Google Cloud console → Vertex AI → API keys). Stored encrypted.'
  },
  project: {
    label: 'Project ID',
    help: {
      vertex_sa: 'Leave empty to use the project_id of the key file.',
      vertex_authorized_user: 'The project requests are billed to. Leave empty only when the file has a quota_project_id.'
    },
    fromKeyFile: "Leave empty to use the key file's project"
  },
  location: {
    label: 'Location',
    help: 'The Vertex AI location requests go to: global (host aiplatform.googleapis.com) or a region such as us-central1, europe-west4 or asia-northeast1 (host <region>-aiplatform.googleapis.com). Not every model is served in every location.'
  },
  express: {
    title: 'Project address (optional)',
    description: 'Fill in both the project ID and the location, or leave both empty to use the express address (https://aiplatform.googleapis.com/v1/publishers/google/models/...).',
    project: 'Project ID (optional)',
    location: 'Location (optional)',
    placeholder: 'Leave empty for the express address'
  }
}

export default { zh, en }
