import { describe, expect, it } from 'vitest'
import { isProtocolField, isSchedulingField, protocolsErrorTexts, ruleErrors, sectionOfErrors } from './editorErrors'

describe('sectionOfErrors', () => {
  it('sends request forwarding errors (header passthrough and override, environment) to the advanced section, scheduling errors first', () => {
    expect(sectionOfErrors({ header_passthrough: 'blocked' }, {})).toBe('advanced')
    expect(sectionOfErrors({ header_passthrough: 'blocked', rpm_limit: 'invalid' }, {})).toBe('scheduling')
    expect(sectionOfErrors({ name: 'required', header_passthrough: 'blocked' }, {})).toBe('connection')
    expect(sectionOfErrors({ 'environment.relay_timeout_seconds': 'invalid' }, {})).toBe('advanced')
    expect(sectionOfErrors({ param_override: 'invalid' }, {})).toBe('advanced')
    expect(sectionOfErrors({ header_override: 'blocked' }, {})).toBe('advanced')
    expect(sectionOfErrors({ conversion_loss_policy: 'invalid' }, {})).toBe('advanced')
  })

  it("counts a fixed control and a model list's limit as scheduling fields", () => {
    expect(isSchedulingField('schedulable')).toBe(true)
    expect(isSchedulingField('model_limits[0].rpm_limit')).toBe(true)
    expect(isSchedulingField('spm_window_seconds')).toBe(true)
    expect(isSchedulingField('models')).toBe(false)
    expect(sectionOfErrors({ header_passthrough: 'blocked', 'model_limits[0].models': 'invalid' }, {})).toBe('scheduling')
  })

  it('sends the errors of the two protocol fields to the protocols and models section', () => {
    expect(sectionOfErrors({ 'protocols[1]': 'invalid' }, {})).toBe('models')
    expect(sectionOfErrors({ protocols: 'no_protocol' }, {})).toBe('models')
    expect(sectionOfErrors({ 'model_protocols[0].models[2]': 'duplicate', rpm_limit: 'invalid' }, {})).toBe('models')
    expect(sectionOfErrors({ name: 'required', 'model_protocols[0].protocols[1]': 'invalid' }, {})).toBe('connection')
    expect(isProtocolField('model_protocols[3].protocols[0]')).toBe(true)
    expect(isProtocolField('protocols')).toBe(true)
    // model_mapping and the conversion policy are other fields.
    expect(isProtocolField('model_mapping.gpt')).toBe(false)
    expect(isProtocolField('conversion_loss_policy')).toBe(false)
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
