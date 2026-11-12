import { describe, expect, it } from 'vitest'
import { filterChips, flowQuery, flowValue, optionText, pairOptions, type FilterField } from './eventFilters'

describe('flow select', () => {
  it.each([
    ['3:2', { flow_id: 3, flow_version: 2 }],
    ['3:', { flow_id: 3, flow_version: '' }],
    ['', { flow_id: '', flow_version: '' }]
  ])('%s → flow_id / flow_version', (v, q) => {
    expect(flowQuery(v)).toEqual(q)
    expect(flowValue(q)).toBe(v)
  })

  it('pairs the version options with the flow id', () => {
    expect(pairOptions(3, [{ value: '', label: 'all' }, { value: 2, label: 'v2' }]).map((o) => o.value)).toEqual(['3:', '3:2'])
  })
})

describe('filter chips', () => {
  const fields: FilterField[] = [
    {
      key: 'conclusion',
      label: '结论',
      options: [{ value: '', label: '结论：全部' }, { value: null, label: '人工复核', options: pairOptions('review_status', [{ value: 'pending', label: '待处理' }]) }]
    },
    { key: 'group_id', label: '分组', options: [{ value: 7, label: 'vip' }] },
    { key: 'api_key_id', label: 'API 密钥 ID' },
    { key: 'endpoint', label: '端点' }
  ]

  it('labels a select value, inside an optgroup with the group', () => {
    expect(optionText(fields[0].options!, 'review_status:pending')).toBe('人工复核 · 待处理')
    expect(optionText(fields[1].options!, '7')).toBe('vip')
    expect(optionText(fields[1].options!, 9)).toBe('9')
  })

  it('lists the set fields in field order, text trimmed', () => {
    const chips = filterChips(fields, { endpoint: ' /v1/messages ', conclusion: 'review_status:pending', group_id: 7, api_key_id: '' })
    expect(chips).toEqual([
      { key: 'conclusion', label: '结论', value: '人工复核 · 待处理' },
      { key: 'group_id', label: '分组', value: 'vip' },
      { key: 'endpoint', label: '端点', value: '/v1/messages' }
    ])
    expect(filterChips(fields, {})).toEqual([])
  })

  it('names a picked id with the text of its field', () => {
    const picked: FilterField[] = [
      { key: 'group_id', label: '分组', text: (id) => (id === '0' ? '默认分组' : `vip #${id}`) },
      { key: 'api_key_id', label: 'API Key', text: (id) => `#${id}` }
    ]
    expect(filterChips(picked, { group_id: '0', api_key_id: 12 })).toEqual([
      { key: 'group_id', label: '分组', value: '默认分组' },
      { key: 'api_key_id', label: 'API Key', value: '#12' }
    ])
  })
})
