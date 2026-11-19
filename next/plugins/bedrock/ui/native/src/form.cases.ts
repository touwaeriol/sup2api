// The cases of the account form: each row is an input and what the form must
// make of it, the v-model it hands back and validate()'s verdict.
//
// The constraints (internal/bedrock/credentials.go holds the same on save):
//   api_key            required, not blank (secret)
//   access_key_id      required, ^[A-Z0-9]{16,128}$
//   secret_access_key  required, 1-1024 visible ASCII characters (secret)
//   session_token      required, 1-8192 visible ASCII characters (secret, bedrock_sts)
//   session_expires_at date-time, optional (bedrock_sts)
//   region             ^([a-z]{2}(-[a-z]+)+-\d)?$, default us-east-1
//   inference_profile_prefix  none|us|eu|apac|jp|au|in|global, default global
import { SECRET_MASK } from '@sub2api/ui'
import type { AccountFormCase } from '@sub2api/ui/testing'

/** stored: secrets masked; input: a select takes its option label. */
export type FormCase = AccountFormCase & { type: 'bedrock_apikey' | 'bedrock_aksk' | 'bedrock_sts' }

/** The label of each field's control (fillControls). */
export const LABELS = {
  api_key: 'API key',
  access_key_id: 'Access key ID',
  secret_access_key: 'Secret access key',
  session_token: 'Session token',
  session_expires_at: 'Credentials expire at (optional)',
  region: 'Region',
  inference_profile_prefix: 'Cross-region inference prefix'
}

const DEFAULTS = { region: 'us-east-1', inference_profile_prefix: 'global' }
const AKID = 'AKIAIOSFODNN7EXAMPLE'
const SECRET = 'wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY'
const TOKEN = 'IQoJb3JpZ2luX2VjEJr//////////wEaCXVzLWVhc3QtMSJHMEUCIQ=='
const KEY = 'ABSKQmVkcm9ja0FQSUtleS1leGFtcGxl'
const AKSK = { access_key_id: AKID, secret_access_key: SECRET }
/** A datetime-local value and the RFC 3339 the form stores for it (this machine's zone). */
const LOCAL = '2026-11-20T10:30'
const RFC3339 = new Date(LOCAL).toISOString()

const STORED_AKSK = { access_key_id: AKID, secret_access_key: SECRET_MASK, region: 'eu-central-1', inference_profile_prefix: 'eu' }

export const CASES: FormCase[] = [
  // ---------------------------------------------------------------- bedrock_aksk
  { name: 'aksk: new account, valid keys', type: 'bedrock_aksk', input: AKSK, out: { ...AKSK, ...DEFAULTS }, ok: true },
  { name: 'aksk: new account, nothing typed', type: 'bedrock_aksk', out: DEFAULTS, ok: false },
  { name: 'aksk: access key ID missing', type: 'bedrock_aksk', input: { secret_access_key: SECRET }, out: { secret_access_key: SECRET, ...DEFAULTS }, ok: false },
  { name: 'aksk: secret missing', type: 'bedrock_aksk', input: { access_key_id: AKID }, out: { access_key_id: AKID, ...DEFAULTS }, ok: false },
  { name: 'aksk: lower-case access key ID', type: 'bedrock_aksk', input: { ...AKSK, access_key_id: 'akiaiosfodnn7example' }, out: { ...AKSK, access_key_id: 'akiaiosfodnn7example', ...DEFAULTS }, ok: false },
  { name: 'aksk: access key ID of 15 characters', type: 'bedrock_aksk', input: { ...AKSK, access_key_id: 'AKIA12345678901' }, out: { ...AKSK, access_key_id: 'AKIA12345678901', ...DEFAULTS }, ok: false },
  { name: 'aksk: access key ID of 128 characters', type: 'bedrock_aksk', input: { ...AKSK, access_key_id: 'A'.repeat(128) }, out: { ...AKSK, access_key_id: 'A'.repeat(128), ...DEFAULTS }, ok: true },
  { name: 'aksk: access key ID of 129 characters', type: 'bedrock_aksk', input: { ...AKSK, access_key_id: 'A'.repeat(129) }, out: { ...AKSK, access_key_id: 'A'.repeat(129), ...DEFAULTS }, ok: false },
  { name: 'aksk: secret with a space', type: 'bedrock_aksk', input: { ...AKSK, secret_access_key: 'wJalr XUtn' }, out: { ...AKSK, secret_access_key: 'wJalr XUtn', ...DEFAULTS }, ok: false },
  { name: 'aksk: secret of 1024 characters', type: 'bedrock_aksk', input: { ...AKSK, secret_access_key: 's'.repeat(1024) }, out: { ...AKSK, secret_access_key: 's'.repeat(1024), ...DEFAULTS }, ok: true },
  { name: 'aksk: secret of 1025 characters', type: 'bedrock_aksk', input: { ...AKSK, secret_access_key: 's'.repeat(1025) }, out: { ...AKSK, secret_access_key: 's'.repeat(1025), ...DEFAULTS }, ok: false },
  { name: 'aksk: another region and prefix', type: 'bedrock_aksk', input: { ...AKSK, region: 'ap-northeast-1', inference_profile_prefix: 'jp' }, out: { ...AKSK, region: 'ap-northeast-1', inference_profile_prefix: 'jp' }, ok: true },
  { name: 'aksk: prefix none', type: 'bedrock_aksk', input: { ...AKSK, inference_profile_prefix: 'None' }, out: { ...AKSK, region: 'us-east-1', inference_profile_prefix: 'none' }, ok: true },
  { name: 'aksk: GovCloud region', type: 'bedrock_aksk', input: { ...AKSK, region: 'us-gov-west-1' }, out: { ...AKSK, ...DEFAULTS, region: 'us-gov-west-1' }, ok: true },
  { name: 'aksk: region cleared (the default)', type: 'bedrock_aksk', input: { ...AKSK, region: '' }, out: { ...AKSK, ...DEFAULTS, region: '' }, ok: true },
  { name: 'aksk: upper-case region', type: 'bedrock_aksk', input: { ...AKSK, region: 'US-EAST-1' }, out: { ...AKSK, ...DEFAULTS, region: 'US-EAST-1' }, ok: false },
  { name: 'aksk: region without its number', type: 'bedrock_aksk', input: { ...AKSK, region: 'us-east' }, out: { ...AKSK, ...DEFAULTS, region: 'us-east' }, ok: false },
  { name: 'aksk: edit, nothing changed', type: 'bedrock_aksk', stored: STORED_AKSK, out: STORED_AKSK, ok: true },
  { name: 'aksk: edit, secret replaced', type: 'bedrock_aksk', stored: STORED_AKSK, input: { secret_access_key: 'newSecret' }, out: { ...STORED_AKSK, secret_access_key: 'newSecret' }, ok: true },
  { name: 'aksk: edit, region changed', type: 'bedrock_aksk', stored: STORED_AKSK, input: { region: 'eu-west-3' }, out: { ...STORED_AKSK, region: 'eu-west-3' }, ok: true },
  { name: 'aksk: edit, an unknown key goes back as it is', type: 'bedrock_aksk', stored: { ...STORED_AKSK, legacy: 'x' }, out: { ...STORED_AKSK, legacy: 'x' }, ok: true },
  // The defaults fill a new account only (useCredentialForm); ValidateCredentials
  // stores a missing region / prefix as us-east-1 / global.
  {
    name: 'aksk: edit, an account stored without region and prefix',
    type: 'bedrock_aksk',
    stored: { access_key_id: AKID, secret_access_key: SECRET_MASK },
    out: { access_key_id: AKID, secret_access_key: SECRET_MASK },
    ok: true
  },
  // ValidateCredentials refuses it too.
  { name: 'aksk: edit, a stored prefix outside the list', type: 'bedrock_aksk', stored: { ...STORED_AKSK, inference_profile_prefix: 'xx' }, out: { ...STORED_AKSK, inference_profile_prefix: 'xx' }, ok: false },

  // ---------------------------------------------------------------- bedrock_apikey
  { name: 'apikey: new account, valid key', type: 'bedrock_apikey', input: { api_key: KEY }, out: { api_key: KEY, ...DEFAULTS }, ok: true },
  { name: 'apikey: nothing typed', type: 'bedrock_apikey', out: DEFAULTS, ok: false },
  { name: 'apikey: blank key', type: 'bedrock_apikey', input: { api_key: '   ' }, out: { api_key: '   ', ...DEFAULTS }, ok: false },
  // any key that is not blank, as ValidateCredentials checks on save
  { name: 'apikey: key of 7 characters', type: 'bedrock_apikey', input: { api_key: 'ABSK123' }, out: { api_key: 'ABSK123', ...DEFAULTS }, ok: true },
  { name: 'apikey: key of 2049 characters', type: 'bedrock_apikey', input: { api_key: 'k'.repeat(2049) }, out: { api_key: 'k'.repeat(2049), ...DEFAULTS }, ok: true },
  { name: 'apikey: key with a space', type: 'bedrock_apikey', input: { api_key: 'ABSK Qmed rock' }, out: { api_key: 'ABSK Qmed rock', ...DEFAULTS }, ok: true },
  { name: 'apikey: key with a non-ASCII character', type: 'bedrock_apikey', input: { api_key: 'ABSKQmedröck' }, out: { api_key: 'ABSKQmedröck', ...DEFAULTS }, ok: true },
  { name: 'apikey: bad region', type: 'bedrock_apikey', input: { api_key: KEY, region: 'east' }, out: { api_key: KEY, ...DEFAULTS, region: 'east' }, ok: false },
  { name: 'apikey: edit, stored key kept', type: 'bedrock_apikey', stored: { api_key: SECRET_MASK, region: 'us-west-2', inference_profile_prefix: 'us' }, out: { api_key: SECRET_MASK, region: 'us-west-2', inference_profile_prefix: 'us' }, ok: true },

  // ---------------------------------------------------------------- bedrock_sts
  { name: 'sts: new account, valid credentials', type: 'bedrock_sts', input: { ...AKSK, session_token: TOKEN }, out: { ...AKSK, session_token: TOKEN, ...DEFAULTS }, ok: true },
  { name: 'sts: session token missing', type: 'bedrock_sts', input: AKSK, out: { ...AKSK, ...DEFAULTS }, ok: false },
  { name: 'sts: session token with a space', type: 'bedrock_sts', input: { ...AKSK, session_token: 'IQoJ b3Jp' }, out: { ...AKSK, session_token: 'IQoJ b3Jp', ...DEFAULTS }, ok: false },
  { name: 'sts: session token of 8193 characters', type: 'bedrock_sts', input: { ...AKSK, session_token: 't'.repeat(8193) }, out: { ...AKSK, session_token: 't'.repeat(8193), ...DEFAULTS }, ok: false },
  { name: 'sts: with an expiry', type: 'bedrock_sts', input: { ...AKSK, session_token: TOKEN, session_expires_at: LOCAL }, out: { ...AKSK, session_token: TOKEN, session_expires_at: RFC3339, ...DEFAULTS }, ok: true },
  {
    name: 'sts: edit, expiry cleared (the key is not sent)',
    type: 'bedrock_sts',
    stored: { ...STORED_AKSK, session_token: SECRET_MASK, session_expires_at: RFC3339 },
    input: { session_expires_at: '' },
    out: { ...STORED_AKSK, session_token: SECRET_MASK },
    ok: true
  },
  // ValidateCredentials refuses it too (checkExpiryHint).
  {
    name: 'sts: edit, a stored expiry that is not a date-time',
    type: 'bedrock_sts',
    stored: { ...STORED_AKSK, session_token: SECRET_MASK, session_expires_at: 'tomorrow' },
    out: { ...STORED_AKSK, session_token: SECRET_MASK, session_expires_at: 'tomorrow' },
    ok: false
  },
  { name: 'sts: edit, stored secrets kept', type: 'bedrock_sts', stored: { ...STORED_AKSK, session_token: SECRET_MASK }, out: { ...STORED_AKSK, session_token: SECRET_MASK }, ok: true }
]
