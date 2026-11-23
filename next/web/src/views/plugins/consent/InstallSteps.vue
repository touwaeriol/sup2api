<script setup lang="ts">
// The steps of an install not registered yet, at the first one: nothing is
// downloaded, installed or changed before the consent.
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SCard, SHint, SSteps, type StepItem } from '@sub2api/ui'

const props = defineProps<{ upgrade: boolean; version: string; from?: string; approved?: boolean }>()
const { t } = useI18n()
const kind = computed(() => (props.upgrade ? 'upgrade' : 'install'))
const steps = computed<StepItem[]>(() => ['consent', 'install', 'start'].map((key) => ({ key, title: t(`pluginInstall.steps.${kind.value}.${key}`) })))
</script>

<template>
  <SCard class="mt-4" data-testid="install-steps">
    <SSteps :steps="steps" current="consent" />
    <SHint class="mt-3">{{ t(approved ? 'pluginInstall.approvedNote' : `pluginInstall.steps.${kind}.note`, { version, from: from || '—' }) }}</SHint>
  </SCard>
</template>
