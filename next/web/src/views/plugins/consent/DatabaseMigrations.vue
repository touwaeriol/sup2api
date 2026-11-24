<script setup lang="ts">
// The consent page's migration scripts (review database_migrations): the ones
// approving runs (new, or changed since they ran) with their SQL, and the ones
// already run as they are behind a toggle. Rendered only when the version has
// scripts.
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SBadge, SCard, SDisclosure, SHint } from '@sub2api/ui'
import type { PluginReview, ReviewMigration } from '@/api/types'
import { asArray } from '../pluginUtil'
import { tablePrefix } from '../pluginId'
import MigrationScript from '../parts/MigrationScript.vue'

const props = defineProps<{ review: PluginReview }>()
const { t } = useI18n()

const migrations = computed(() => asArray<ReviewMigration>(props.review.database_migrations))
const pending = computed(() => migrations.value.filter((m) => m.status !== 'applied'))
const applied = computed(() => migrations.value.filter((m) => m.status === 'applied'))
</script>

<template>
  <SCard
    v-if="migrations.length"
    :title="t('plugins.database.migrations')"
    :subtitle="t('plugins.database.migrationsHint', { prefix: tablePrefix(review.plugin_key) })"
    class="mt-4"
    data-testid="database-migrations"
  >
    <ul v-if="pending.length" class="divide-y divide-line">
      <MigrationScript v-for="m in pending" :id="m.id" :key="m.id" :sql="m.sql">
        <SBadge :tone="m.status === 'changed' ? 'warning' : 'info'">{{ m.status === 'changed' ? t('plugins.database.willRerun') : t('plugins.database.willRun') }}</SBadge>
      </MigrationScript>
    </ul>
    <SHint v-else>{{ t('plugins.database.nothingToRun') }}</SHint>
    <SDisclosure v-if="applied.length" variant="section" :label="t('plugins.database.appliedList', { n: applied.length })" class="mt-3 border-t border-line pt-3">
      <ul class="divide-y divide-line" data-testid="database-migrations-applied">
        <MigrationScript v-for="m in applied" :id="m.id" :key="m.id" :sql="m.sql" />
      </ul>
    </SDisclosure>
  </SCard>
</template>
