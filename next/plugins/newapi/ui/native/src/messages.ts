// New API UI messages, registered under plugin.newapi.* via host.addMessages.
// vue-i18n syntax: avoid the special characters @ | { } in plain text.
const zh = {
  baseUrl: {
    label: '站点地址',
    help: '填 New API 站点的根地址，各协议和余额查询按官方路径拼在它后面。末尾带 /v1 或 /v1beta 也可以：请求时只去掉末尾这一段，再拼完整路径。'
  },
  apiKey: { help: '站点的令牌。查用户余额需要在上游给这个令牌开启"允许查询用户额度"。加密保存，保存后不再明文显示。' }
}

type Messages = typeof zh

const en: Messages = {
  baseUrl: {
    label: 'Base URL',
    help: 'Root address of the New API site; each protocol and the balance query go to their official paths under it. A trailing /v1 or /v1beta is fine: requests drop that one segment, then append the full path.'
  },
  apiKey: { help: "A token of the site. The balance includes the user's quota only if the token allows querying the user's quota on the site. Stored encrypted and never shown again." }
}

export default { zh, en }
