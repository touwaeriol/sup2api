import { describe, expect, it } from 'vitest'
import { isSectionField, protocolsErrorTexts, ruleErrors } from './editorErrors'

describe('the fields of each section', () => {
  const sectionOf = (field: string) => (['connection', 'models', 'scheduling', 'advanced'] as const).filter(s => isSectionField(s)(field))

  it('puts request forwarding (header passthrough and override, environment, conversion) in the advanced section', () => {
    for (const field of ['header_passthrough', 'environment.relay_timeout_seconds', 'param_override', 'header_override', 'conversion_loss_policy']) expect(sectionOf(field)).toEqual(['advanced'])
  })

  it("counts a fixed control and a model list's limit as scheduling fields", () => {
    for (const field of ['schedulable', 'model_limits[0].rpm_limit', 'spm_window_seconds', 'usage_limit']) expect(sectionOf(field)).toEqual(['scheduling'])
  })

  it('puts the two protocol fields, the models and the mapping in the protocols and models section', () => {
    for (const field of ['protocols[1]', 'protocols', 'model_protocols[0].models[2]', 'model_mapping.gpt', 'models[3]']) expect(sectionOf(field)).toEqual(['models'])
  })

  it('puts the basics and the credentials in the connection section, and nothing that only starts alike', () => {
    for (const field of ['name', 'proxy_url', 'credentials.api_key', 'credentials[0]']) expect(sectionOf(field)).toEqual(['connection'])
    expect(sectionOf('names')).toEqual([])
    expect(sectionOf('models_x')).toEqual([])
  })
})

describe('server errors of the protocol fields', () => {
  it('words an item of protocols with the protocol that was sent', () => {
    const errors = { protocols: '账号没有开放任何协议', 'protocols[1]': '不是本平台协议', 'protocols[9]': '无效', name: '已被使用', 'model_protocols[0].protocols[0]': '无效' }
    expect(protocolsErrorTexts(errors, ['openai.chat', 'anthropic.messages'])).toEqual(['账号没有开放任何协议', 'anthropic.messages: 不是本平台协议', 'protocols[9]: 无效'])
    expect(protocolsErrorTexts({ 'protocols[0]': '无效' }, null)).toEqual(['protocols[0]: 无效'])
    expect(protocolsErrorTexts({ name: '已被使用' }, null)).toEqual([])
  })

  it('sorts the errors of model_protocols by the saved entry they are about', () => {
    expect(ruleErrors({ model_protocols: '最多 50 条', 'model_protocols[1].models[0]': '模型重复', 'model_protocols[1].protocols[2]': '不能服务', 'model_protocols[0].protocols': '不能为空', protocols: '无效' })).toEqual({
      general: ['最多 50 条'],
      byEntry: { 0: ['不能为空'], 1: ['模型重复', '不能服务'] }
    })
    expect(ruleErrors({})).toEqual({ general: [], byEntry: {} })
  })
})
