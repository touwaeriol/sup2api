<script setup lang="ts">
// The asset group create dialog (POST /asset-groups).
import { SField, SForm, SInput, STextarea } from '@sub2api/ui'
import { useCreateForm } from './createForm'
import { useVolcHost } from './host'

const t = useVolcHost().t
const { form, submit } = useCreateForm(
  '/asset-groups',
  {
    account_id: { required: true, type: 'integer', min: 1 },
    name: { required: true, whitespace: true, min: 1, max: 128 },
    title: { max: 256 },
    description: { max: 1024 },
    group_type: { max: 64 }
  },
  { group_type: 'AIGC' }
)
defineExpose({ submit })
</script>

<template>
  <SForm :form="form">
    <div class="volcengine-fields">
      <SField prop="account_id" :label="t('newGroup.account')" :hint="t('newGroup.accountHelp')">
        <SInput v-bind="form.bind('account_id')" type="number" :min="1" :step="1" />
      </SField>
      <SField prop="name" :label="t('newGroup.name')" :hint="t('newGroup.nameHelp')">
        <SInput v-bind="form.bind('name')" />
      </SField>
      <SField prop="title" :label="t('newGroup.title')">
        <SInput v-bind="form.bind('title')" />
      </SField>
      <SField prop="description" :label="t('newGroup.description')">
        <STextarea v-bind="form.bind('description')" />
      </SField>
      <SField prop="group_type" :label="t('newGroup.groupType')" :hint="t('newGroup.groupTypeHelp')">
        <SInput v-bind="form.bind('group_type')" placeholder="AIGC" />
      </SField>
    </div>
  </SForm>
</template>
