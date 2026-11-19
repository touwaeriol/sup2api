// The cases of the account forms: each row is an input and what the form
// must make of it, the v-model it hands back and validate()'s verdict.
//
// The constraints (internal/vertex/credentials.go holds the same on save):
//   service_account_json / authorized_user_json  required, 2-32768 characters (secret)
//   api_key      required, not blank (secret)
//   project_id   ^([a-z][a-z0-9-]{4,28}[a-z0-9])?$
//   location     ^([a-z0-9-]{2,32})?$, default global for the key file types
//   access_token, expires_at, token_for  the core's (token renewal): never shown, kept as they are
import { SECRET_MASK } from '@sub2api/ui'
import type { AccountFormCase } from '@sub2api/ui/testing'

export type VertexType = 'vertex_sa' | 'vertex_authorized_user' | 'vertex_apikey'

/** stored: secrets masked. */
export type FormCase = AccountFormCase & { type: VertexType }

/** The labels of each type's fields (fillControls). */
export const LABELS: Record<VertexType, Record<string, string>> = {
  vertex_sa: { service_account_json: 'Service account key', project_id: 'Project ID', location: 'Location' },
  vertex_authorized_user: { authorized_user_json: 'User credentials file', project_id: 'Project ID', location: 'Location' },
  vertex_apikey: { api_key: 'API key', project_id: 'Project ID (optional)', location: 'Location (optional)' }
}

const SA = JSON.stringify({
  type: 'service_account',
  project_id: 'my-project-123456',
  private_key_id: 'abc123',
  private_key: '-----BEGIN PRIVATE KEY-----\\nMIIEv...\\n-----END PRIVATE KEY-----\\n',
  client_email: 'vertex@my-project-123456.iam.gserviceaccount.com',
  token_uri: 'https://oauth2.googleapis.com/token'
})
const USER = JSON.stringify({ type: 'authorized_user', client_id: 'id.apps.googleusercontent.com', client_secret: 'secret', refresh_token: '1//0refresh', quota_project_id: 'my-project-123456' })
const KEY = 'AQ.Ab8RN6-example-key'
const GLOBAL = { location: 'global' }
/** A key file account as the editor loads it: the file and the token masked, the token state as the core wrote it. */
const TOKEN = { access_token: SECRET_MASK, expires_at: 1800000000, token_for: '9f86d081884c7d659a2feaa0c55ad015' }
const STORED_SA = { service_account_json: SECRET_MASK, project_id: 'my-project-123456', location: 'global', ...TOKEN }
const STORED_USER = { authorized_user_json: SECRET_MASK, project_id: 'my-project-123456', location: 'us-central1', ...TOKEN }

export const CASES: FormCase[] = [
  // ---------------------------------------------------------------- vertex_sa
  { name: 'sa: new account, the key file', type: 'vertex_sa', input: { service_account_json: SA }, out: { service_account_json: SA, ...GLOBAL }, ok: true },
  { name: 'sa: nothing typed', type: 'vertex_sa', out: GLOBAL, ok: false },
  { name: 'sa: key file of 1 character', type: 'vertex_sa', input: { service_account_json: '{' }, out: { service_account_json: '{', ...GLOBAL }, ok: false },
  { name: 'sa: key file of 32768 characters', type: 'vertex_sa', input: { service_account_json: 'x'.repeat(32768) }, out: { service_account_json: 'x'.repeat(32768), ...GLOBAL }, ok: true },
  { name: 'sa: key file of 32769 characters', type: 'vertex_sa', input: { service_account_json: 'x'.repeat(32769) }, out: { service_account_json: 'x'.repeat(32769), ...GLOBAL }, ok: false },
  { name: 'sa: project and region', type: 'vertex_sa', input: { service_account_json: SA, project_id: 'other-project-1', location: 'europe-west4' }, out: { service_account_json: SA, project_id: 'other-project-1', location: 'europe-west4' }, ok: true },
  { name: 'sa: project with an underscore', type: 'vertex_sa', input: { service_account_json: SA, project_id: 'Bad_Project' }, out: { service_account_json: SA, project_id: 'Bad_Project', ...GLOBAL }, ok: false },
  { name: 'sa: project of 5 characters', type: 'vertex_sa', input: { service_account_json: SA, project_id: 'abcde' }, out: { service_account_json: SA, project_id: 'abcde', ...GLOBAL }, ok: false },
  { name: 'sa: project ending in a dash', type: 'vertex_sa', input: { service_account_json: SA, project_id: 'my-project-' }, out: { service_account_json: SA, project_id: 'my-project-', ...GLOBAL }, ok: false },
  { name: 'sa: upper-case location', type: 'vertex_sa', input: { service_account_json: SA, location: 'US' }, out: { service_account_json: SA, location: 'US' }, ok: false },
  { name: 'sa: location naming a host', type: 'vertex_sa', input: { service_account_json: SA, location: 'evil.example' }, out: { service_account_json: SA, location: 'evil.example' }, ok: false },
  { name: 'sa: location cleared (global)', type: 'vertex_sa', input: { service_account_json: SA, location: '' }, out: { service_account_json: SA, location: '' }, ok: true },
  { name: 'sa: edit, nothing changed: the token state goes back as it is', type: 'vertex_sa', stored: STORED_SA, out: STORED_SA, ok: true },
  { name: 'sa: edit, project changed: the token state is kept', type: 'vertex_sa', stored: STORED_SA, input: { project_id: 'other-project-1' }, out: { ...STORED_SA, project_id: 'other-project-1' }, ok: true },
  { name: 'sa: edit, key file replaced: the token state is kept for the plugin to judge', type: 'vertex_sa', stored: STORED_SA, input: { service_account_json: SA }, out: { ...STORED_SA, service_account_json: SA }, ok: true },
  { name: 'sa: edit, an unknown key goes back as it is', type: 'vertex_sa', stored: { ...STORED_SA, legacy: 'x' }, out: { ...STORED_SA, legacy: 'x' }, ok: true },
  // The defaults fill a new account only (useCredentialForm); ValidateCredentials stores a missing location as global.
  { name: 'sa: edit, an account stored without a location', type: 'vertex_sa', stored: { service_account_json: SECRET_MASK, ...TOKEN }, out: { service_account_json: SECRET_MASK, ...TOKEN }, ok: true },

  // ---------------------------------------------------------------- vertex_authorized_user
  { name: 'user: new account, the credentials file', type: 'vertex_authorized_user', input: { authorized_user_json: USER }, out: { authorized_user_json: USER, ...GLOBAL }, ok: true },
  { name: 'user: nothing typed', type: 'vertex_authorized_user', out: GLOBAL, ok: false },
  { name: 'user: file of 32769 characters', type: 'vertex_authorized_user', input: { authorized_user_json: 'x'.repeat(32769) }, out: { authorized_user_json: 'x'.repeat(32769), ...GLOBAL }, ok: false },
  { name: 'user: project and region', type: 'vertex_authorized_user', input: { authorized_user_json: USER, project_id: 'billing-project', location: 'us-central1' }, out: { authorized_user_json: USER, project_id: 'billing-project', location: 'us-central1' }, ok: true },
  { name: 'user: project starting with a digit', type: 'vertex_authorized_user', input: { authorized_user_json: USER, project_id: '1project' }, out: { authorized_user_json: USER, project_id: '1project', ...GLOBAL }, ok: false },
  { name: 'user: location of 33 characters', type: 'vertex_authorized_user', input: { authorized_user_json: USER, location: 'a'.repeat(33) }, out: { authorized_user_json: USER, location: 'a'.repeat(33) }, ok: false },
  { name: 'user: edit, nothing changed: the token state goes back as it is', type: 'vertex_authorized_user', stored: STORED_USER, out: STORED_USER, ok: true },
  { name: 'user: edit, location changed: the token state is kept', type: 'vertex_authorized_user', stored: STORED_USER, input: { location: 'global' }, out: { ...STORED_USER, location: 'global' }, ok: true },

  // ---------------------------------------------------------------- vertex_apikey
  { name: 'apikey: new account, the key (express address)', type: 'vertex_apikey', input: { api_key: KEY }, out: { api_key: KEY }, ok: true },
  { name: 'apikey: nothing typed', type: 'vertex_apikey', out: {}, ok: false },
  { name: 'apikey: blank key', type: 'vertex_apikey', input: { api_key: '   ' }, out: { api_key: '   ' }, ok: false },
  // any key that is not blank, as ValidateCredentials checks on save
  { name: 'apikey: key of 7 characters', type: 'vertex_apikey', input: { api_key: 'AQ.1234' }, out: { api_key: 'AQ.1234' }, ok: true },
  { name: 'apikey: key of 513 characters', type: 'vertex_apikey', input: { api_key: 'k'.repeat(513) }, out: { api_key: 'k'.repeat(513) }, ok: true },
  { name: 'apikey: key with a space', type: 'vertex_apikey', input: { api_key: 'AQ.Ab8 RN6' }, out: { api_key: 'AQ.Ab8 RN6' }, ok: true },
  { name: 'apikey: project address', type: 'vertex_apikey', input: { api_key: KEY, project_id: 'my-project-123456', location: 'us-central1' }, out: { api_key: KEY, project_id: 'my-project-123456', location: 'us-central1' }, ok: true },
  // Both or neither is the plugin's rule on save (target.check), as the section says; the form leaves it to the plugin.
  { name: 'apikey: project only (refused on save)', type: 'vertex_apikey', input: { api_key: KEY, project_id: 'my-project-123456' }, out: { api_key: KEY, project_id: 'my-project-123456' }, ok: true },
  { name: 'apikey: bad location', type: 'vertex_apikey', input: { api_key: KEY, project_id: 'my-project-123456', location: 'us_central1' }, out: { api_key: KEY, project_id: 'my-project-123456', location: 'us_central1' }, ok: false },
  { name: 'apikey: edit, stored key kept', type: 'vertex_apikey', stored: { api_key: SECRET_MASK, project_id: 'my-project-123456', location: 'global' }, out: { api_key: SECRET_MASK, project_id: 'my-project-123456', location: 'global' }, ok: true },
  { name: 'apikey: edit, key replaced', type: 'vertex_apikey', stored: { api_key: SECRET_MASK }, input: { api_key: KEY }, out: { api_key: KEY }, ok: true }
]
