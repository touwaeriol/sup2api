// Read-only form preview sourced from the real plugin, never bundled in production.
// Start Vite mock mode with SUB2API_MOCK_VOLCENGINE=1 and open /accounts.
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { resolve, sep } from 'node:path'
import { fail, on } from './router'
import { builtinPlatforms } from './platforms'
import { caller, hasPerm } from './core'

if (process.env.SUB2API_MOCK_VOLCENGINE === '1') {
  const root = fileURLToPath(new URL('../../plugins/volcengine/', import.meta.url))
  const read = (path: string) => {
    const absolute = resolve(root,path)
    if (!absolute.startsWith(resolve(root)+sep)) throw new Error('Plugin fixture path escapes its source directory')
    return JSON.parse(readFileSync(absolute,'utf8'))
  }
  const manifest = read('manifest.json')
  const platforms = [...builtinPlatforms, ...manifest.platforms.map((p: any) => ({ ...p, builtin: false }))]
  const accountTypes = manifest.accountTypes.map((type: any) => ({
    plugin_key: manifest.key, plugin_name: manifest.name, plugin_version: manifest.version,
    asset_base: `/plugin-ui/${manifest.key}/${manifest.version}`, trust: 'official',
    type: type.id, label: type.label, description: type.description, form: type.form,
    sensitive_fields: type.sensitiveFields,
    default_models: type.defaultModels || [], default_model_mapping: type.defaultModelMapping || {},
    platforms: type.platforms.map((p: any) => { const definition = platforms.find(x => x.id===p.platform); return { id:p.platform,label:definition?.label || p.platform,builtin:definition?.builtin || false,available:!!definition } }),
    endpoints: type.platforms.flatMap((p: any) => (platforms.find(x => x.id===p.platform)?.endpoints || []).map((e: any) => ({ ...e,platform:p.platform,native:true,grade:'native' })))
  }))
  on('GET','/account-types',()=>accountTypes)
  on('GET','/me/platforms',()=>platforms.map(p=>({ id:p.id,label:p.label,builtin:p.builtin,endpoints:p.endpoints })))
  on('GET','/account-types/volcengine/:type/form',req=>{
    const type=manifest.accountTypes.find((a: any)=>a.id===req.params.type)
    if (!type) return fail(404,'not_found','Unknown Volcengine account type')
    const schema=read(type.form.schema),ui_schema=read(type.form.uiSchema)
    if (!hasPerm(caller(req),'account:settings:custom')) {
      for (const guard of type.guardedSettings || []) {
        if (!schema.properties?.[guard.field]) continue
        schema.properties[guard.field].enum=[...guard.allowed]
        if (guard.allowed.length===1) ui_schema[guard.field]={...ui_schema[guard.field],'ui:readonly':true}
      }
    }
    return {schema,ui_schema}
  })
  on('POST','/accounts',()=>fail(405,'method_not_allowed','Read-only Volcengine form fixture; account creation is disabled'))
}
