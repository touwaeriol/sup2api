import { describe, expect, it } from 'vitest'
import { ApiError } from '@sub2api/host'
import { i18n } from '@/i18n'
import { errorMessage, fieldErrors } from './errors'

const invalid = (fields: { field: string; code?: string; message: string }[]) => new ApiError(400, { code: 'invalid_argument', message: 'invalid argument', details: { fields } })

describe('errorMessage', () => {
  it('words a version whose package is gone (410 package_gone) in both languages', () => {
    const e = new ApiError(410, { code: 'package_gone', message: 'no reachable node holds the package of this version; bring a node that holds it back online, or upload it again' })
    i18n.global.locale.value = 'zh'
    expect(errorMessage(e)).toBe(i18n.global.t('plugins.errors.packageGone'))
    expect(errorMessage(e)).toContain('没有能连上的节点持有该插件版本的安装包')
    i18n.global.locale.value = 'en'
    expect(errorMessage(e)).toContain('No reachable node holds the package')
  })

  it('words a busy cluster change (409 cluster_change_busy) in both languages', () => {
    const e = new ApiError(409, { code: 'cluster_change_busy', message: 'another cluster change is being submitted; retry' })
    i18n.global.locale.value = 'zh'
    expect(errorMessage(e)).toContain('请稍后重试')
    i18n.global.locale.value = 'en'
    expect(errorMessage(e)).toContain('Try again in a moment')
  })
})

describe('fieldErrors', () => {
  it('translates the codes a form knows and keeps the server message otherwise', () => {
    const e = invalid([
      { field: 'values/key', code: 'reserved', message: 'reserved key' },
      { field: 'values/url', code: 'format', message: 'must be a URL' },
      { field: 'name', message: 'required' }
    ])
    const codes = { reserved: 'roles.keyReserved' }
    i18n.global.locale.value = 'zh'
    expect(fieldErrors(e, undefined, codes)).toEqual({ 'values/key': '这是内置角色保留的标识，请换一个', 'values/url': 'must be a URL', name: 'required' })
    i18n.global.locale.value = 'en'
    expect(fieldErrors(e, 'values/', codes)).toEqual({ key: 'This key is reserved for a built-in role; choose another', url: 'must be a URL' })
    expect(fieldErrors(e)).toEqual({ 'values/key': 'reserved key', 'values/url': 'must be a URL', name: 'required' })
  })

  it('maps the notification test codes in both languages, falling back for the old "invalid"', () => {
    const codes = {
      unknown_channel: 'settings.notifications.errors.unknown_channel',
      recipients_count: 'settings.notifications.errors.recipients_count',
      invalid_recipient: 'settings.notifications.errors.invalid_recipient'
    }
    const e = invalid([
      { field: 'channel', code: 'unknown_channel', message: 'unknown notification channel' },
      { field: 'recipients', code: 'invalid_recipient', message: 'invalid recipient x' }
    ])
    i18n.global.locale.value = 'zh'
    expect(fieldErrors(e, undefined, codes)).toEqual({ channel: '没有这个通知渠道，可能提供它的插件已停用', recipients: '收件人格式不符合该渠道的要求' })
    i18n.global.locale.value = 'en'
    expect(fieldErrors(invalid([{ field: 'recipients', code: 'recipients_count', message: 'between 1 and 20 recipients' }]), undefined, codes)).toEqual({ recipients: 'Give 1-20 recipients' })
    expect(fieldErrors(invalid([{ field: 'recipients', code: 'invalid', message: 'between 1 and 20 recipients' }]), undefined, codes)).toEqual({ recipients: 'between 1 and 20 recipients' })
  })
})

describe('errorMessage of resource scope refusals', () => {
  it('words would_leave_scope and the refused proxy creation in the console language', () => {
    i18n.global.locale.value = 'zh'
    expect(errorMessage(new ApiError(403, { code: 'would_leave_scope', message: 'the change would move the account out of your range' }))).toBe('修改后账号将不在你的管理范围内')
    i18n.global.locale.value = 'en'
    const proxy = new ApiError(403, { code: 'permission_denied', message: 'x', details: { permission: 'proxy:manage:own' } })
    expect(errorMessage(proxy)).toBe(i18n.global.t('proxies.cannotCreate'))
    expect(errorMessage(new ApiError(403, { code: 'permission_denied', message: 'account:read is required', details: { permission: 'account:read' } }))).toBe('account:read is required')
  })

  it('words not_in_scope of a field', () => {
    const e = invalid([{ field: 'group_ids', code: 'not_in_scope', message: 'bind the account to a group you manage' }])
    i18n.global.locale.value = 'zh'
    expect(fieldErrors(e, undefined, { not_in_scope: 'accounts.scopeErrors.notInScope' })).toEqual({ group_ids: '账号必须绑定到你管理的账号分组' })
    i18n.global.locale.value = 'en'
  })
})
