import { defineComponent, h, type Component } from 'vue'
import IndexPage from './IndexPage.vue'
import NewAssetForm from './NewAssetForm.vue'
import NewGroupForm from './NewGroupForm.vue'

// The two asset library pages (manifest ui.pages, type native): the
// columns of the local index rows GET /assets and GET /asset-groups return.

export interface IndexColumn {
  key: string
  /** Message key of the header; none for the # column. */
  label?: string
  format?: 'badge' | 'datetime' | 'number'
}

export interface IndexPageSpec {
  /** Message keys. */
  title: string
  createLabel: string
  path: string
  columns: IndexColumn[]
  /** The create dialog's content: exposes submit(). */
  form: Component
}

const assets: IndexPageSpec = {
  title: 'pages.assets',
  createLabel: 'pages.newAsset',
  path: '/assets',
  form: NewAssetForm,
  columns: [
    { key: 'id' },
    { key: 'account_name', label: 'col.account' },
    { key: 'group_name', label: 'col.group' },
    { key: 'name', label: 'col.name' },
    { key: 'asset_type', label: 'col.type' },
    { key: 'status', label: 'col.status', format: 'badge' },
    { key: 'index_status', label: 'col.index', format: 'badge' },
    { key: 'upstream_id', label: 'col.upstreamId' },
    { key: 'updated_at', label: 'col.updated', format: 'datetime' }
  ]
}

const groups: IndexPageSpec = {
  title: 'pages.groups',
  createLabel: 'pages.newGroup',
  path: '/asset-groups',
  form: NewGroupForm,
  columns: [
    { key: 'id' },
    { key: 'account_name', label: 'col.account' },
    { key: 'name', label: 'col.name' },
    { key: 'title', label: 'col.title' },
    { key: 'group_type', label: 'col.type' },
    { key: 'asset_count', label: 'col.assetCount', format: 'number' },
    { key: 'index_status', label: 'col.index', format: 'badge' },
    { key: 'upstream_id', label: 'col.upstreamId' },
    { key: 'updated_at', label: 'col.updated', format: 'datetime' }
  ]
}

const page = (name: string, spec: IndexPageSpec) => defineComponent({ name, setup: () => () => h(IndexPage, { spec }) })

export const AssetsPage = page('VolcengineAssetsPage', assets)
export const AssetGroupsPage = page('VolcengineAssetGroupsPage', groups)
