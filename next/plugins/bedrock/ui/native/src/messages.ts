// Bedrock UI messages, registered under plugin.bedrock.* via host.addMessages.
// vue-i18n syntax: avoid the special characters @ | { } in plain text.
const zh = {
  apiKey: {
    label: 'API Key',
    help: 'Amazon Bedrock 的 API Key（Bedrock 控制台 → API keys），以 Bearer 令牌发送。短期 Key 最长 12 小时有效，且只能用在创建它的区域；不会自动续期，到期后请在这里更换。加密保存。'
  },
  accessKeyId: {
    label: 'Access Key ID',
    help: {
      bedrock_aksk: 'IAM 用户的长期访问密钥，该用户要有 bedrock:InvokeModel 和 bedrock:InvokeModelWithResponseStream 权限。',
      bedrock_sts: '临时凭证的 Access Key ID（AWS STS：AssumeRole、GetSessionToken、IAM Identity Center）。'
    }
  },
  secretAccessKey: {
    label: 'Secret Access Key',
    help: {
      bedrock_aksk: 'IAM 用户的 Secret Access Key。核心用它为每个请求签名（SigV4）；加密保存，不会发送给上游。',
      bedrock_sts: '临时凭证的 Secret Access Key。核心用它为每个请求签名（SigV4）；加密保存，不会发送给上游。'
    }
  },
  sessionToken: {
    label: 'Session Token',
    help: '与临时凭证一起签发的 Session Token。加密保存。'
  },
  sessionExpiresAt: {
    label: '凭证到期时间（可选）',
    help: '只作提示，不参与任何逻辑。临时凭证不会自动续期：到期后上游会拒绝该账号，账号被禁用；请在这里粘贴新的凭证并重新启用。'
  },
  regionSection: {
    title: '区域与模型',
    description:
      'Claude 请求发往 InvokeModel。请求体按 Bedrock 的要求调整，其余按客户端原样发送：model 和 stream 移到地址里，写入 anthropic_version，删除 metadata、provider、service_tier、inference_geo 和 speed，没有请求对应 beta 特性时删除 context_management。anthropic_beta 改为 anthropic-beta 请求头、请求体的 anthropic_beta 和工具所需（computer use、tool search）的 beta 特性中 Bedrock 接受的那些。Claude Code 加上而 Bedrock 不接受的内容会被清理：工具定义里的 custom，cache_control 里的 scope（Claude 4.5 之前的模型还有 ttl），tool use ID 里 Bedrock 不接受的字符，以及模型不支持的 thinking 设置（只支持 adaptive 的模型去掉 budget_tokens，其他模型缺 budget 时补上）。output_config 和 output_format 原样发送。OpenAI 协议的请求先转换成 Claude 请求。'
  },
  region: {
    label: '区域',
    help: 'Bedrock 运行时所在的 AWS 区域，例如 us-east-1、us-west-2、eu-central-1、ap-northeast-1。请求发往 bedrock-runtime.<区域>.amazonaws.com；不支持中国区（cn-*）和隔离分区（us-iso-*、us-isob-* 等）。留空使用 us-east-1。'
  },
  prefix: {
    label: '跨区域推理前缀',
    none: '不加',
    help: '加在 Claude 请求的模型 ID 前面，用来调用跨区域推理配置，例如 global.anthropic.claude-sonnet-5-5。AWS 上现行的 Claude 模型几乎都只能通过推理配置调用，所以默认是 global：预填的模型都有 global 配置，除 GovCloud 外的区域都能调用。唯一的例外是 Opus 4.1，它只有 us. 配置，预填的映射里已经直接写了这个配置的 ID。地域前缀（us、eu、apac、jp、au、in）把请求留在该地域内，但不是每个模型都有每个地域的配置，请对照模型卡片。“不加”按原样发送模型 ID，只在映射里写的是完整的推理配置 ID 或 ARN（GovCloud 用 us-gov. 开头的 ID），或者模型在本区域有区域内（In-Region）可用性时选它。已经带前缀（us.、eu.、apac.、jp.、au.、in.、us-gov.、global.）的模型 ID 和 ARN 始终原样发送。'
  }
}

const en: typeof zh = {
  apiKey: {
    label: 'API key',
    help: 'An Amazon Bedrock API key (Bedrock console → API keys), sent as a bearer token. A short-term key lasts at most 12 hours and only works in the region it was created in; nothing renews it, so replace it here when it expires. Stored encrypted.'
  },
  accessKeyId: {
    label: 'Access key ID',
    help: {
      bedrock_aksk: 'The long-term access key of an IAM user allowed to call bedrock:InvokeModel and bedrock:InvokeModelWithResponseStream.',
      bedrock_sts: 'The access key ID of temporary credentials (AWS STS: AssumeRole, GetSessionToken, IAM Identity Center).'
    }
  },
  secretAccessKey: {
    label: 'Secret access key',
    help: {
      bedrock_aksk: 'The secret access key of the IAM user. The core signs every request with it (SigV4); it is stored encrypted and never sent upstream.',
      bedrock_sts: 'The secret access key of the temporary credentials. The core signs every request with it (SigV4); it is stored encrypted and never sent upstream.'
    }
  },
  sessionToken: {
    label: 'Session token',
    help: 'The session token issued with the temporary credentials. Stored encrypted.'
  },
  sessionExpiresAt: {
    label: 'Credentials expire at (optional)',
    help: 'A reminder only: nothing reads it. Temporary credentials are not renewed automatically. Once they expire the upstream refuses the account and it is disabled; paste new credentials here and enable it again.'
  },
  regionSection: {
    title: 'Region and models',
    description:
      'Claude requests go to InvokeModel. The body is adapted for Bedrock and otherwise sent as the client wrote it: model and stream move into the address, anthropic_version is set, metadata, provider, service_tier, inference_geo and speed are removed, and context_management is removed unless its beta feature is requested. anthropic_beta becomes the beta features Bedrock accepts out of those of the anthropic-beta header, of the body\'s anthropic_beta and of what the tools need (computer use, tool search). What Claude Code adds that Bedrock rejects is cleaned: custom in tool definitions, scope in cache_control (and ttl before Claude 4.5), characters Bedrock refuses in tool use ids, and thinking settings the model does not take (budget_tokens on models that only think adaptively, a missing budget otherwise). output_config and output_format are sent as they are. Requests in an OpenAI protocol are converted to Claude requests first.'
  },
  region: {
    label: 'Region',
    help: 'The AWS region of the Bedrock runtime, e.g. us-east-1, us-west-2, eu-central-1, ap-northeast-1. Requests go to bedrock-runtime.<region>.amazonaws.com; the China regions (cn-*) and the isolated regions (us-iso-*, us-isob-* and the like) are not supported. Leave empty for us-east-1.'
  },
  prefix: {
    label: 'Cross-region inference prefix',
    none: 'None',
    help: 'Put before the model ID of Claude requests to call a cross-region inference profile, e.g. global.anthropic.claude-sonnet-5-5. On AWS nearly every current Claude model can only be invoked through one, so the default is global: every prefilled model has a global profile, callable from every region except GovCloud. The one exception is Opus 4.1, which only has a us. profile; its prefilled mapping names that profile itself. A geography prefix (us, eu, apac, jp, au, in) keeps requests inside that geography, but not every model has every geography: check the model\'s card. None sends model IDs as they are; pick it only when the mapping holds complete inference profile IDs or ARNs (GovCloud: us-gov. IDs), or for a model with in-Region availability in this region. A model ID that already has a prefix (us., eu., apac., jp., au., in., us-gov., global.) and an ARN are always sent as they are.'
  }
}

export default { zh, en }
