// Server field errors -> form paths, shared by the console and the form
// framework of @sub2api/ui (useForm's serverErrors, usePluginSettings).

/**
 * The dotted form path ('a.b.0') of a server field error. Account
 * credentials come back as `credentials.a.b`, plugin settings as JSON
 * pointers under `values` (`values/a/0/b`, `~1` = '/', `~0` = '~') or dotted
 * (`values.a.0.b`); the prefix is dropped. '' is the whole object.
 */
export function toFieldPath(field: string): string {
  const rest = field.replace(/^(credentials|values)(?=[./]|$)/, '').replace(/^[./]/, '')
  return rest
    .split('/')
    .map((s) => s.replace(/~1/g, '/').replace(/~0/g, '~'))
    .join('.')
}

/**
 * Field errors of PUT /plugins/:key/settings (ApiError.fields) as form paths;
 * errors of the whole object come back in `root`.
 */
export function settingsFieldErrors(fields: Record<string, string>): { fields: Record<string, string>; root: string[] } {
  const out: Record<string, string> = {}
  const root: string[] = []
  for (const [k, v] of Object.entries(fields)) {
    const p = toFieldPath(k)
    if (p) out[p] = v
    else root.push(v)
  }
  return { fields: out, root }
}
