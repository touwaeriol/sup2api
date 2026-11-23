// The consent page of an install not registered yet (from the market or an
// uploaded file): nothing is installed before the consent (CONTRACTS §5.7).
export default {
  uploadMissing: '插件包文件已不在页面上（例如刷新了页面），请回到插件列表重新选择文件。',
  backToList: '回插件列表',
  packageChanged: '插件包已变化，请重新审查。',
  steps: {
    install: { consent: '确认授权', install: '安装', start: '启用', note: '现在什么都还没安装。同意授权后才安装并启用；取消则不留下任何记录。' },
    upgrade: { consent: '确认授权', install: '安装新版本', start: '逐节点升级', note: '现在什么都还没改：当前运行的 v{from} 照常服务。同意授权后才安装 v{version} 并开始升级；取消则不留下任何记录。' }
  },
  approvedNote: '这个版本此前已经授权过，不会重新授权；继续后直接开始启用或升级。',
  cancelInstall: '取消安装',
  cancelUpgrade: '取消升级'
}
