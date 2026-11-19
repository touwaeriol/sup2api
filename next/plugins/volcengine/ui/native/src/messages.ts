// Volcengine UI messages, registered under plugin.volcengine.* via host.addMessages.
// vue-i18n syntax: avoid the special characters @ | { } in plain text.
const zh = {
  apikey: {
    keyHelp: '官方方舟 API Key，支持 OpenAI 兼容接口、Anthropic Messages 与官方豆包视频，加密保存。',
    baseUrlPlaceholder: '留空使用默认地址 https://ark.cn-beijing.volces.com',
    baseUrlHelp: '官方方舟服务根地址。OpenAI、图片与视频使用 /api/v3；Anthropic Messages 使用 /api/compatible/v1/messages。地址已以 /api/v3 结尾（方舟 SDK 的地址）时 OpenAI、图片与视频不再重复添加，Anthropic Messages 仍发往根地址下的 /api/compatible/v1/messages。其他主机需要 account:settings:custom 权限。',
    accessKeyHelp: '火山引擎账号的 Access Key ID（控制台「访问控制」）。只有素材库需要它：素材库用账号的 AK/SK 签名，不用 API Key。它和 Secret Key 都留空，这个账号就不参与素材库。',
    secretKeyHelp: 'AK/SK 中的密钥部分。加密保存，保存后不再明文显示。两者只填一个会被拒绝。',
    assetBaseUrlPlaceholder: '留空使用默认地址 https://ark.cn-beijing.volcengineapi.com',
    assetBaseUrlHelp: '方舟素材库 OpenAPI 的地址。注意这是方舟的控制面，与上面的 Base URL（API 主机）不是同一个主机：素材库调用是带签名的 POST /，Action 放在查询参数里。填写其他地址需要 account:settings:custom 权限。',
    regionHelp: '素材库调用的签名区域；它是签名的一部分，填错了上游报的是签名错误而不是区域错误。方舟国内用 cn-beijing，海外（BytePlus）用 ap-southeast-1。'
  },
  relay: {
    keyHelp: '上游 API Key，以 Authorization: Bearer 发送，加密保存。',
    baseUrlHelp: '必填服务根地址，不带尾部 /v1 或 /api/v3。下面的相对端点按此地址解析。',
    videoEndpoint: '视频端点',
    videoEndpointHelp: '填写完整任务集合路径或 http(s) 完整 URL。提交向此地址 POST，查询追加 /任务ID；不要只填路径前缀。留空沿用旧视频前缀规则。',
    assetEndpoint: '素材库端点',
    assetEndpointHelp: '填写完整路径或 http(s) 完整 URL。调用自动追加 Action、Version 参数，使用下面独立的 AK/SK。留空仅兼容已显式填写的旧素材地址；两者都未填时不启用素材库，不会默认发送到官方端点。',
    accessKeyHelp: '素材库 Access Key；与 Secret Key、素材库端点一起填写。',
    secretKeyHelp: '素材库 Secret Key，与 API Key 分开加密保存；须与 Access Key 配对。',
    regionHelp: '素材库调用的签名区域；它是签名的一部分，填错了上游报的是签名错误而不是区域错误。中转的是方舟国内就用 cn-beijing，海外（BytePlus）用 ap-southeast-1。',
    legacy: '兼容配置',
    legacyHelp: '仅供视频端点、素材库端点出现之前创建的旧中转接入使用；新账号保持留空。',
    apiPrefix: '兼容配置：文本 API 前缀',
    apiPrefixPlaceholder: '留空使用 /v1',
    apiPrefixHelp: '保留已有文本与图片中转接入，默认 /v1。',
    videoApiPrefix: '兼容配置：视频 API 前缀',
    videoApiPrefixPlaceholder: '留空则跟随「API 路径前缀」',
    videoApiPrefixHelp: '仅在新视频端点留空时使用，自动追加 /contents/generations/tasks。留空跟随文本 API 前缀。',
    assetBaseUrl: '兼容配置：旧素材地址',
    assetBaseUrlHelp: '保留已有完整素材 URL，仅在新素材库端点留空时使用；空值不会选择官方服务。'
  },
  asset: {
    section: '素材库（可选）',
    sectionHelp: '仅在使用方舟素材库时填写；留空则该账号不参与素材库。',
    accessKey: '素材库 Access Key',
    accessKeyPlaceholder: '留空则不启用素材库',
    secretKey: '素材库 Secret Key',
    secretKeyPlaceholder: '填了 Access Key 就必须填',
    baseUrl: '素材端点',
    region: '素材库区域',
    regionPlaceholder: '留空使用 cn-beijing'
  },
  pages: {
    assets: '方舟素材库',
    groups: '方舟素材组',
    newAsset: '新建素材',
    newGroup: '新建素材组'
  },
  col: {
    account: '账号',
    group: '素材组',
    name: '名称',
    title: '标题',
    type: '类型',
    status: '上游状态',
    index: '索引',
    assetCount: '素材数',
    upstreamId: '方舟 id',
    updated: '更新时间'
  },
  newGroup: {
    account: '账号',
    accountHelp: '在哪个方舟账号上创建素材组，填账号的数字 id。只有填了 Access Key / Secret Key 的账号才能用素材库；账号 id 见 GET /api/v1/p/volcengine/accounts。',
    name: '名称',
    nameHelp: '发给方舟的素材组名称。上游不要求唯一，请起一个自己能认出来的名字。',
    title: '标题',
    description: '描述',
    groupType: '素材组类型',
    groupTypeHelp: '方舟的素材组类型。除非火山文档另有说明，保持 AIGC 即可。'
  },
  newAsset: {
    group: '素材组',
    groupHelp: '素材组页面里的 # 列（本站自己的 id，不是方舟 id）。账号由素材组决定，因此素材不会落到别的账号的素材组里。',
    name: '名称',
    assetType: '素材类型',
    assetTypeHelp: '该素材在方舟里的类型（如 Image、Video）。留空则由方舟按地址判断。',
    url: '素材地址',
    urlHelp: '方舟从哪里取这个素材。方舟是异步下载的，所以素材一开始处于处理中状态；重新打开可以看到最新状态。'
  }
}

const en: typeof zh = {
  apikey: {
    keyHelp: 'Official Ark API key for OpenAI-compatible APIs, Anthropic Messages and official video. Stored encrypted.',
    baseUrlPlaceholder: 'Leave empty to use https://ark.cn-beijing.volces.com',
    baseUrlHelp:
      'Official Ark service root. OpenAI/image/video use /api/v3; Anthropic Messages uses /api/compatible/v1/messages. An address already ending in /api/v3 (the Ark SDK base URL) does not get it twice on OpenAI, image and video requests; Anthropic Messages still goes to /api/compatible/v1/messages on the root. Other hosts require account:settings:custom.',
    accessKeyHelp:
      "Volcengine account access key id (console: Access Control). Only needed for the asset library, which is signed with the account's AK/SK instead of the API key. Leave this and the secret key empty and the account simply does not take part in the asset library.",
    secretKeyHelp: 'The secret half of the pair. Stored encrypted and never shown again. Filling in only one of the two is refused.',
    assetBaseUrlPlaceholder: 'Leave empty to use https://ark.cn-beijing.volcengineapi.com',
    assetBaseUrlHelp:
      'The Ark asset library OpenAPI endpoint. NOTE this is the Ark control plane and a DIFFERENT host from the Base URL above (which is the API host): asset calls are a signed POST / with ?Action=... Any other address needs the account:settings:custom permission.',
    regionHelp:
      'Signing region of the asset library calls; it is part of the signature, so a wrong value is rejected upstream as a signature error rather than a region error. cn-beijing for Ark in China, ap-southeast-1 for the overseas (BytePlus) stack.'
  },
  relay: {
    keyHelp: 'The upstream API key, sent as Authorization: Bearer and stored encrypted.',
    baseUrlHelp: 'Required service root, without a trailing /v1 or /api/v3. Relative endpoints below resolve against this address.',
    videoEndpoint: 'Video task collection endpoint',
    videoEndpointHelp:
      'Full task collection path or absolute HTTP(S) URL. Submit uses POST here; polling appends /task_id. Do not enter only a path prefix. Leave empty to preserve the legacy video-prefix behavior.',
    assetEndpoint: 'Asset library endpoint',
    assetEndpointHelp:
      'Full path or absolute HTTP(S) URL. Asset calls append Action and Version query parameters and use the separate AK/SK below. If empty, an explicitly configured legacy asset URL is used; otherwise the asset library is disabled. Never defaults to an official endpoint.',
    accessKeyHelp: 'Optional asset Access Key. Fill together with Secret Key and an asset endpoint.',
    secretKeyHelp: 'Asset Secret Key, stored encrypted separately from the API key. Fill together with Access Key.',
    regionHelp:
      'Signing region of the asset library calls; it is part of the signature, so a wrong value is rejected upstream as a signature error rather than a region error. cn-beijing for a relay in front of Ark in China, ap-southeast-1 for the overseas (BytePlus) stack.',
    legacy: 'Legacy settings',
    legacyHelp: 'Only for relay integrations created before the video and asset endpoints existed; new accounts leave these empty.',
    apiPrefix: 'Legacy text API prefix',
    apiPrefixPlaceholder: 'Leave empty to use /v1',
    apiPrefixHelp: 'Preserves existing text/image relay integrations; defaults to /v1.',
    videoApiPrefix: 'Legacy video API prefix',
    videoApiPrefixPlaceholder: 'Leave empty to follow the API path prefix',
    videoApiPrefixHelp: 'Used only when the new video endpoint is empty. Appends /contents/generations/tasks. Empty follows the text API prefix.',
    assetBaseUrl: 'Legacy asset URL',
    assetBaseUrlHelp: 'Existing full asset URL, used only when the new asset endpoint is empty. An empty value does not select the official service.'
  },
  asset: {
    section: 'Asset library (optional)',
    sectionHelp: 'Only needed for the Ark asset library; leave these empty and the account does not take part in it.',
    accessKey: 'Asset library access key',
    accessKeyPlaceholder: 'Leave empty to keep the asset library off',
    secretKey: 'Asset library secret key',
    secretKeyPlaceholder: 'Required together with the access key',
    baseUrl: 'Asset endpoint',
    region: 'Asset region',
    regionPlaceholder: 'Leave empty to use cn-beijing'
  },
  pages: {
    assets: 'Ark asset library',
    groups: 'Ark asset groups',
    newAsset: 'New asset',
    newGroup: 'New asset group'
  },
  col: {
    account: 'Account',
    group: 'Group',
    name: 'Name',
    title: 'Title',
    type: 'Type',
    status: 'Upstream status',
    index: 'Index',
    assetCount: 'Assets',
    upstreamId: 'Ark id',
    updated: 'Updated'
  },
  newGroup: {
    account: 'Account',
    accountHelp:
      'Numeric id of the Ark account the group is created on. Only accounts with an access key and secret key can hold asset groups; GET /api/v1/p/volcengine/accounts lists the ids.',
    name: 'Name',
    nameHelp: 'The group name sent to Ark. It is not unique upstream, so pick something you will recognize.',
    title: 'Title',
    description: 'Description',
    groupType: 'Group type',
    groupTypeHelp: "Ark's asset group type. Leave AIGC unless the Volcengine documentation tells you otherwise."
  },
  newAsset: {
    group: 'Asset group',
    groupHelp:
      "The # column of the asset groups page (this site's own id, not the Ark id). The account is taken from the group, so an asset can never land under another account's group.",
    name: 'Name',
    assetType: 'Asset type',
    assetTypeHelp: "Ark's asset type for this material (e.g. Image, Video). Leave empty to let Ark decide from the URL.",
    url: 'URL',
    urlHelp:
      'Where Ark fetches the material. Ark downloads it asynchronously, so the asset starts out in a processing state; reopen it to see the current status.'
  }
}

export default { zh, en }
