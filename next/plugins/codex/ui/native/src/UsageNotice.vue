<script setup lang="ts">
// The personal-use notice shown with every authorization flow: the
// subscription is for its owner only, with OpenAI's own statements one click
// away. The links are plain external links; the plugin requests neither.
import { SAlert, SIcon, SLink } from '@sub2api/ui'
import { useCodexHost } from './host'
import { USAGE_POLICY_LINKS } from './flow'

const t = useCodexHost().t
</script>

<template>
  <SAlert tone="warning" dense :title="t('notice.title')" data-testid="codex-notice">
    <p>{{ t('notice.body') }}</p>
    <div class="cx-actions">
      <SLink v-for="link in USAGE_POLICY_LINKS" :key="link.key" :href="link.href" external class="cx-link" :data-testid="`codex-notice-${link.key}`">
        <SIcon name="external" class="cx-icon" />{{ t(`notice.links.${link.key}`) }}
      </SLink>
    </div>
  </SAlert>
</template>
