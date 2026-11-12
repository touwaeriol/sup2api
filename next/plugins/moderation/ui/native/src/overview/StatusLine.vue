<script setup lang="ts">
import { computed } from 'vue'
import { SAlert, SBadge, SButton, type Tone } from '@sub2api/ui'
import { exemptGroups, groupCoverage } from '../bindingOps'
import { useCatalog } from '../catalog'
import { joinList } from '../format'
import { canManage, canManageSettings, useModHost } from '../host'
import { modeTone } from '../tones'
import type { Status } from '../types'
import type { SectionKey } from '../nav'

// Running state (new-api status-line) as short label + badge entries:
// moderation (switch and the default level: the level of users without a
// watch entry, `mode` of the settings), flows, moderated / unmoderated groups,
// channels; plus the setup hint when nothing is bound or moderation is off.
// Long explanations (group names) are the badges' tooltips.
const props = defineProps<{ status: Status }>()
const emit = defineEmits<{ (e: 'go-tab', tab: SectionKey): void }>()
const t = useModHost().t
const catalog = useCatalog()

interface Badge {
  text: string
  tone: Tone
  title?: string
  dot?: boolean
}

/** off, or the default level (GET /status default_level; the mode says the same on a backend without it). */
const level = computed(() => (!props.status.mode || props.status.mode === 'off' ? 'off' : props.status.default_level || props.status.mode))

const state = computed<Badge[]>(() => {
  const run = !props.status.enabled ? { tone: 'gray' as const, key: 'off' } : props.status.running ? { tone: 'success' as const, key: 'running' } : { tone: 'warning' as const, key: 'stopped' }
  return [
    { text: t(`statusLine.${run.key}`), tone: run.tone, dot: true },
    { text: t(`mode.${level.value}`), tone: modeTone(level.value), title: t(`statusLine.levelHint.${level.value}`) }
  ]
})

const flows = computed<Badge[]>(() => {
  const on = catalog.state.flows.filter((f) => f.enabled).length
  const off = catalog.state.flows.length - on
  const list: Badge[] = [{ text: t('statusLine.flows', { n: on }), tone: on ? 'success' : 'warning' }]
  if (off) list.push({ text: t('statusLine.flowsOff', { n: off }), tone: 'gray' })
  return list
})

/** moderated / unmoderated groups of the core; null when groups are not readable */
const groups = computed<Badge[] | null>(() => {
  const all = catalog.state.groups
  if (!all.length) return null
  const moderated = new Set(groupCoverage(all.map((g) => g.id), catalog.state.flows, exemptGroups(catalog.state.bindings)).moderated)
  const unmoderated = all.filter((g) => !moderated.has(g.id)).map((g) => g.name)
  const list: Badge[] = [{ text: t('statusLine.bound', { n: moderated.size }), tone: moderated.size ? 'success' : 'gray' }]
  if (unmoderated.length) list.push({ text: t('statusLine.unbound', { n: unmoderated.length }), tone: 'warning', title: t('statusLine.unboundHint', { groups: joinList(unmoderated) }) })
  return list
})

const channels = computed<Badge[]>(() => {
  const down = catalog.state.channels.filter((c) => !c.enabled).length
  return [down ? { text: t('statusLine.channelsDown', { n: down }), tone: 'warning' } : { text: t('statusLine.channelsOk'), tone: 'success' }]
})

// The settings link only for those who may edit the settings (canManageSettings).
const settingsLink = computed(() => canManageSettings())
const entries = computed(() =>
  [
    { key: 'state', badges: state.value },
    { key: 'flows', badges: flows.value },
    { key: 'groups', badges: groups.value },
    { key: 'channels', badges: channels.value }
  ].filter((e): e is { key: string; badges: Badge[] } => !!e.badges)
)

const notice = computed<{ tone: 'warning' | 'info'; text: string; action: 'flows' | 'policy' | '' } | null>(() => {
  if (!props.status.runtime.configured) return { tone: 'warning', text: t('overview.notConfigured'), action: canManage() ? 'flows' : '' }
  if (props.status.mode === 'off') return { tone: 'info', text: t('overview.offHint'), action: settingsLink.value ? 'policy' : '' }
  return null
})
</script>

<template>
  <dl class="mod-status-line">
    <div v-for="e in entries" :key="e.key" class="mod-status-item">
      <dt>{{ t(`statusLine.label.${e.key}`) }}</dt>
      <dd>
        <SBadge v-for="b in e.badges" :key="b.text" :tone="b.tone" :dot="b.dot" size="xs" :title="b.title">{{ b.text }}</SBadge>
      </dd>
    </div>
  </dl>

  <SAlert v-if="notice" :tone="notice.tone" class="mod-block">
    {{ notice.text }}
    <template v-if="notice.action" #actions>
      <SButton :variant="notice.action === 'flows' ? 'primary' : 'secondary'" size="sm" @click="emit('go-tab', notice.action)">
        {{ notice.action === 'flows' ? t('tabs.flows') : t('settings') }}
      </SButton>
    </template>
  </SAlert>
</template>
