<script setup lang="ts">
import { computed, ref } from 'vue'
import { isApiError } from '@sub2api/host'
import { SAlert, SButton, SCard, SEmpty, SField, SGrid, SHint, SSelect, STextarea } from '@sub2api/ui'
import TestResultView from './TestResultView.vue'
import TraceView from './TraceView.vue'
import ImageInput from './ImageInput.vue'
import { useCatalog } from './catalog'
import { runTest, useModHost } from './host'
import { useAsync } from './useAsync'
import type { SectionKey } from './nav'

const emit = defineEmits<{ (e: 'go-tab', tab: SectionKey): void }>()

// Playground: POST /test {text, images, flow_id} -> result + per-stage trace.
// Nothing is cached, recorded or counted. Only the latest run shows.
const host = useModHost()
const t = host.t

const text = ref('')
const images = ref<string[]>([])
const flowId = ref<number>(0)
const required = ref(false)
const test = useAsync(() => runTest(text.value, images.value, flowId.value), () => t('test.failed'))
const result = test.data
const running = test.loading
const notConfigured = computed(() => isApiError(test.failure.value) && test.failure.value.code === 'not_configured')
const failure = computed(() => (required.value ? t('test.required') : notConfigured.value ? '' : test.error.value))

const catalog = useCatalog()
const flowOptions = computed(() => [
  { value: 0, label: t('test.defaultFlow') },
  ...catalog.state.flows.filter((f) => f.version > 0 && f.enabled).map((f) => ({ value: f.id, label: `${f.name} v${f.version}` }))
])

const samples = ['normal', 'suspicious', 'jailbreak'] as const
function useSample(k: (typeof samples)[number]) {
  text.value = t(`test.sample.${k}Text`)
}

function run() {
  required.value = !text.value.trim() && !images.value.length
  test.reset()
  if (!required.value) test.reload()
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) run()
}
</script>

<template>
  <div>
    <SHint class="mod-hint">{{ t('test.hint') }}</SHint>

    <SGrid template="repeat(auto-fit, minmax(min(340px, 100%), 1fr))" class="mod-test-grid">
      <SCard :title="t('test.title')">
        <div class="mod-form">
          <SField :label="t('test.flow')">
            <SSelect v-model="flowId" :options="flowOptions" />
          </SField>
          <ImageInput v-model="images">
            <STextarea v-model="text" class="mod-textarea" :rows="8" :placeholder="t('test.placeholder')" :aria-label="t('test.title')" @keydown="onKey" />
          </ImageInput>
        </div>
        <template #footer>
          <SHint inline size="xs">{{ t('labelColon', { label: t('test.samples') }) }}</SHint>
          <SButton v-for="s in samples" :key="s" variant="ghost" size="sm" @click="useSample(s)">{{ t(`test.sample.${s}`) }}</SButton>
          <SButton variant="primary" icon="play" class="mod-ml-auto" :loading="running" :disabled="!text.trim() && !images.length" @click="run">
            {{ running ? t('test.running') : t('test.run') }}
          </SButton>
        </template>
      </SCard>

      <SCard :title="t('test.result')">
        <SAlert v-if="notConfigured" tone="warning">
          {{ t('test.notConfigured') }}
          <template #actions>
            <SButton variant="primary" size="sm" @click="emit('go-tab', 'flows')">{{ t('tabs.flows') }}</SButton>
          </template>
        </SAlert>
        <SAlert v-else-if="failure" tone="danger">{{ failure }}</SAlert>
        <SEmpty v-else-if="!result" :text="running ? t('test.running') : t('test.empty')" icon="eyeglass" />
        <TestResultView v-else :result="result" />
      </SCard>
    </SGrid>

    <SCard v-if="result" :title="t('detail.trace')" class="mod-section">
      <TraceView :trace="result.trace" />
    </SCard>
  </div>
</template>
