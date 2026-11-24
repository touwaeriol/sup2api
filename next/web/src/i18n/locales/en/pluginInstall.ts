// The consent page of an install not registered yet (from the market or an
// uploaded file): nothing is installed before the consent (CONTRACTS §5.7).
export default {
  uploadMissing: 'The plugin package is no longer on this page (it was reloaded, for example). Go back to the plugin list and choose the file again.',
  backToList: 'Back to plugins',
  packageChanged: 'The plugin package has changed. Please review it again.',
  steps: {
    install: { consent: 'Approve permissions', install: 'Install', start: 'Enable', note: 'Nothing is installed yet. The plugin is installed and enabled only once you approve; cancelling leaves nothing behind.' },
    upgrade: { consent: 'Approve permissions', install: 'Install new version', start: 'Upgrade node by node', note: 'Nothing has changed yet: the running v{from} keeps serving. v{version} is installed and the upgrade starts only once you approve; cancelling leaves nothing behind.' }
  },
  newRisk: '{n} new high-risk permission | {n} new high-risk permissions',
  newRiskTitle: 'New high-risk permissions',
  newRiskHint: 'This version newly asks for the high or critical host permissions below; acknowledge them before approving.',
  ack: 'I understand the new high-risk permissions',
  ackRequired: 'Acknowledge the new high-risk permissions first.',
  egress: 'Egress domains',
  egressHint: 'The domains the plugin may reach through the core (the scope of the net host permission).',
  egressNew: 'new',
  approvedNote: 'This version was approved before and is not approved again; continuing starts its enable or upgrade directly.',
  cancelInstall: 'Cancel install',
  cancelUpgrade: 'Cancel upgrade'
}
