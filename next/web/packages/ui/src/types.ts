/** Indices are zero-based within the supplied rows. The source array is never mutated. */
export interface TableReorderEvent<T> {
  row: T
  key: string | number
  from: number
  to: number
  rows: T[]
}

export interface TableColumn {
  key: string
  label: string
  width?: string
  align?: 'left' | 'right' | 'center'
  class?: string
  /** Keep the cell on one line. */
  nowrap?: boolean
  /**
   * Truncate the cell with an ellipsis: true = max width 16rem, or a CSS
   * width ('12rem'). Default rendering gets the full value as `title`.
   */
  ellipsis?: boolean | string
  /**
   * Take the width the other columns leave and truncate the cell to one line
   * (cell slot content too). Phone cards are not affected.
   */
  grow?: boolean
  /** Extra class of the header cell. */
  headerClass?: string
  /** False keeps a required column visible in the column picker. */
  hideable?: boolean
  /** Initial visibility when column settings are enabled. */
  defaultHidden?: boolean
  /** Built-in cell rendering; a cell slot still takes precedence. */
  format?: 'ip'
  /** IP cells: show a copy button (default true). */
  copyable?: boolean
  /** IP cells: clicking the address emits STable's filter event (default false). */
  filterable?: boolean
  /**
   * Place on the phone card (STable `mobile` 'card'): 'title' / 'status'
   * in the header, 'subtitle' under it, 'meta' in the label/value grid,
   * 'detail' behind the details toggle, 'hidden' left out. When no column
   * declares it, the card infers places (see tableCard.ts).
   */
  mobile?: 'title' | 'subtitle' | 'status' | 'meta' | 'detail' | 'hidden'
  /** 2 = a meta / detail item spans the whole card row. */
  mobileSpan?: 1 | 2
}

export interface TabItem {
  key: string
  label: string
  badge?: string | number
  disabled?: boolean
}

/** A tab of STabbedForm (UI 4.1); `summary` is the line under its title and `icon` (an SIcon name) a tile before it, in the side layout. */
export interface FormTab extends TabItem {
  summary?: string
  icon?: string
}

export interface SelectOption {
  value: string | number | boolean | null
  label: string
  disabled?: boolean
  /** Searchable select: short grey monospace text ending the row (an id, "#7"); the closed field shows it after the label. */
  hint?: string
  /** Searchable select: a second, smaller grey line under the label. */
  description?: string
  /** Searchable select: a small grey tag after the label ("Disabled"). */
  tag?: string
  /** Searchable select: dims the label (an inactive entry that can still be picked). */
  muted?: boolean
  /** Searchable select: a row that is not a listed value ("Name contains …"): primary label, a dashed line under it. */
  accent?: boolean
  /** Group entry (<optgroup>): `label` + nested `options`; its own `value` is ignored. */
  options?: SelectOption[]
}

export interface MenuAction {
  key: string
  label: string
  danger?: boolean
  disabled?: boolean
  hidden?: boolean
  /** SIcon name shown before the label. */
  icon?: string
  /** Draw a separator line above this item. */
  divider?: boolean
  /** Renders a menuitemcheckbox; omitted for ordinary actions. */
  checked?: boolean
}

/** `purple` is the older name of `accent` (same colours). */
export type Tone = 'primary' | 'success' | 'warning' | 'danger' | 'gray' | 'purple' | 'accent' | 'info'

/** Status tones of SAlert / SChip / SStatusDot / SProgress. */
export type StatusTone = 'neutral' | 'primary' | 'info' | 'success' | 'warning' | 'danger' | 'accent'

/** SModal / SFormModal `mobile`: their form on phones; `auto` picks a sheet or a full-screen dialog by width. */
export type ModalMobile = 'auto' | 'sheet' | 'fullscreen' | 'center'

/** One chip of SChipList. `title` is its native tooltip. */
export interface ChipListItem {
  key: string
  label: string
  tone?: StatusTone
  title?: string
}

/** One applied filter of SFilterChips, shown as "label: value". */
export interface FilterChipItem {
  key: string
  label: string
  value: string
}

export interface ChoiceOption {
  value: string | number | boolean | null
  label: string
  description?: string
  /** SIcon name. */
  icon?: string
  disabled?: boolean
  tone?: StatusTone
}

export interface DescItem {
  key: string
  label: string
  value?: unknown
  mono?: boolean
  copyable?: boolean
  /** Span both columns (columns = 2). */
  full?: boolean
}

export type StepState = 'done' | 'current' | 'pending' | 'error'

export interface StepItem {
  key: string
  title: string
  description?: string
  state?: StepState
}

/** One field of an SOptionMatrix: a row of radios under the shared columns. */
export interface OptionMatrixRow {
  key: string
  label: string
  /** Second line under the label (e.g. what an inherited value resolves to). */
  hint?: string
  /** Narrows the choices; columns missing here render disabled. */
  options?: SelectOption[]
  disabled?: boolean
  /** Draws a marker beside the label (e.g. differs from the inherited value). */
  marked?: boolean
  /** The value that applies while the row's own value defers (e.g. "follow"); its cell is tinted. */
  effective?: SelectOption['value']
  testid?: string
}

/** A labelled block of SOptionMatrix rows whose header sets them all at once. */
export interface OptionMatrixGroup {
  key: string
  label: string
  rows: OptionMatrixRow[]
  /** Short text beside the label (e.g. a count of changed rows). */
  badge?: string
  /** Applies when the matrix is `collapsible`; open unless false. */
  defaultOpen?: boolean
}
