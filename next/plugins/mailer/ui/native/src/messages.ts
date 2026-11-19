// Mailer UI messages, registered under plugin.mailer.* via host.addMessages.
// vue-i18n syntax: avoid the special characters @ | { } in plain text.
const zh = {
  host: { label: 'SMTP 服务器', help: '留空则不发送：邮件渠道的通知会等待并重试。' },
  port: { label: '端口', help: '通常 STARTTLS 用 587，TLS 用 465。' },
  security: {
    label: '加密方式',
    starttls: 'STARTTLS（必须支持）',
    tls: '全程 TLS',
    none: '不加密（仅限本机中继）'
  },
  username: { label: '用户名', help: '服务器不需要登录时留空。' },
  password: { label: '密码', help: '加密保存。留空时保留已存储的密码；换成不需要登录的服务器时可清除。' },
  fromAddress: { label: '发件地址' },
  fromName: { label: '发件人名称' },
  timeout: { label: '超时', unit: '秒', help: '一次投递的最长耗时，包含建立连接。' }
}

const en: typeof zh = {
  host: { label: 'SMTP host', help: 'Leave empty to stop sending: notifications on the email channel wait and are retried.' },
  port: { label: 'Port', help: 'Usually 587 with STARTTLS, 465 with TLS.' },
  security: {
    label: 'Security',
    starttls: 'STARTTLS (required)',
    tls: 'TLS from the start',
    none: 'None (only for a local relay)'
  },
  username: { label: 'Username', help: 'Leave empty when the server needs no login.' },
  password: { label: 'Password', help: 'Stored encrypted. Leave empty to keep the stored password; clear it when the server needs no login.' },
  fromAddress: { label: 'From address' },
  fromName: { label: 'From name' },
  timeout: { label: 'Timeout', unit: 's', help: 'Longest time one delivery may take, connection included.' }
}

export default { zh, en }
