export default {
  title: 'Publishers',
  description: 'Store publishers are kept by the official store; local publishers only sign manually uploaded packages.',
  tabs: { store: 'Store publishers', local: 'Local publishers' },
  store: {
    hint: 'Read-only, from the official store index (default channel). To add or revoke a publisher, open a PR to the store repository:',
    plugins: 'Store plugins',
    installed: 'Installed',
    empty: 'The store index lists no publishers'
  },
  local: {
    hint: 'Local publishers and their ed25519 signing keys, used only to verify manually uploaded packages.',
    idRule: 'A local publisher id cannot be sub2api or a store publisher id, so that "publisher + key" stays unique.'
  },
  create: 'New publisher',
  trustLevel: 'Trust level',
  trust_: {
    official: 'Official',
    verified: 'Verified',
    community: 'Community'
  },
  trustHint_: {
    official: 'Official publisher anchored in the built-in root key: may request every permission and ship native UI.',
    verified: 'Confirmed by an administrator: may request every permission (critical ones still confirmed one by one) and ship native UI.',
    community: 'Registered but not verified: cannot request critical permissions or ship native UI.'
  },
  keys: 'Active keys',
  signingKeys: 'Signing keys',
  keysUnavailable: 'Keys are not included in the publisher list.',
  noKeys: 'No signing keys yet.',
  addKey: 'Add key',
  addKeyTitle: 'Add signing key to {name}',
  keyId: 'Key ID',
  keyIdHint: 'Identifier referenced by plugin signatures; must be unique.',
  publicKey: 'Public key',
  publicKeyHint: 'Base64-encoded ed25519 public key (32 bytes).',
  publicKeyInvalid: 'Not a valid base64 ed25519 public key (32 bytes)',
  notBefore: 'Valid from',
  notAfter: 'Valid until',
  rangeInvalid: 'Must be later than "Valid from"',
  keyAdded: 'Key added',
  notYetValid: 'Not yet valid',
  revoke: 'Revoke',
  copyPublicKey: 'Copy public key {key}',
  revokeKey: 'Revoke key',
  revoked: 'Revoked',
  confirmRevoke: 'Revoke publisher "{name}"? All of its keys become invalid and plugins signed by it are marked as revoked (disabled by default).',
  confirmRevokeKey: 'Revoke key "{key}" of {name}? Plugins signed with it are marked as revoked (disabled by default).'
}
