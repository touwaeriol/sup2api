// The icon set of <SIcon>. This module is the single source of truth for the
// icon *names*: manifests (ui.menus[].icon) and the core menu (/me/menus)
// address icons by these names, so anything that wants to validate an icon
// name must read ICON_NAMES from here.
//
// Outline icons, 24x24 viewBox, stroke 1.8, no fill.

export const ICON_PATHS: Record<string, string[]> = {
  dashboard: ['M3 13h8V3H3v10z', 'M13 21h8V11h-8v10z', 'M3 21h8v-6H3v6z', 'M13 3v6h8V3h-8z'],
  group: ['M17 20h5v-2a4 4 0 00-5-3.87', 'M9 20H2v-2a4 4 0 014-4h2a4 4 0 014 4v2H9z', 'M9 10a3 3 0 100-6 3 3 0 000 6z', 'M17 10a3 3 0 100-6 3 3 0 000 6z'],
  account: ['M4 7a2 2 0 012-2h12a2 2 0 012 2v10a2 2 0 01-2 2H6a2 2 0 01-2-2V7z', 'M8 11a2 2 0 104 0 2 2 0 00-4 0z', 'M14 10h3M14 14h3M7 16c.5-1.2 1.6-2 3-2s2.5.8 3 2'],
  proxy: ['M12 21a9 9 0 100-18 9 9 0 000 18z', 'M3.6 9h16.8M3.6 15h16.8', 'M12 3c2.5 2.7 3.8 5.7 3.8 9s-1.3 6.3-3.8 9c-2.5-2.7-3.8-5.7-3.8-9S9.5 5.7 12 3z'],
  price: ['M7 7h.01', 'M3 11.6V4a1 1 0 011-1h7.6a1 1 0 01.7.3l8.4 8.4a1 1 0 010 1.4l-7.6 7.6a1 1 0 01-1.4 0L3.3 12.3a1 1 0 01-.3-.7z'],
  usage: ['M4 19V5', 'M4 19h16', 'M8 15v-4', 'M12 15V8', 'M16 15v-6'],
  sticky: ['M10 14a4 4 0 005.66 0l3-3a4 4 0 00-5.66-5.66l-1 1', 'M14 10a4 4 0 00-5.66 0l-3 3a4 4 0 005.66 5.66l1-1'],
  ledger: ['M6 3h11a2 2 0 012 2v14a2 2 0 01-2 2H6a1 1 0 01-1-1V4a1 1 0 011-1z', 'M9 7h6M9 11h6M9 15h4'],
  user: ['M16 7a4 4 0 11-8 0 4 4 0 018 0z', 'M4 21a8 8 0 0116 0'],
  image: ['M5 3h14a2 2 0 012 2v14a2 2 0 01-2 2H5a2 2 0 01-2-2V5a2 2 0 012-2z', 'M9 11a2 2 0 100-4 2 2 0 000 4z', 'M21 15l-3.1-3.1a2 2 0 00-2.8 0L6 21'],
  role: ['M12 3l8 3v6c0 4.5-3.4 8.3-8 9-4.6-.7-8-4.5-8-9V6l8-3z', 'M9 12l2 2 4-4'],
  shield: ['M12 3l8 3v6c0 4.5-3.4 8.3-8 9-4.6-.7-8-4.5-8-9V6l8-3z'],
  key: ['M15 7a2 2 0 012 2', 'M21 9a6 6 0 01-7.74 5.74L11 17H9v2H7v2H3v-3.59l6.26-6.26A6 6 0 1121 9z'],
  plugin: ['M14 4a2 2 0 10-4 0v1H6a1 1 0 00-1 1v4h1a2 2 0 110 4H5v4a1 1 0 001 1h4v-1a2 2 0 114 0v1h4a1 1 0 001-1v-4h-1a2 2 0 110-4h1V6a1 1 0 00-1-1h-4V4z'],
  puzzle: ['M14 4a2 2 0 10-4 0v1H6a1 1 0 00-1 1v4h1a2 2 0 110 4H5v4a1 1 0 001 1h4v-1a2 2 0 114 0v1h4a1 1 0 001-1v-4h-1a2 2 0 110-4h1V6a1 1 0 00-1-1h-4V4z'],
  market: ['M3 9l1.5-5h15L21 9', 'M3 9h18v1a3 3 0 01-6 0 3 3 0 01-6 0 3 3 0 01-6 0V9z', 'M5 12v8h14v-8', 'M10 20v-5h4v5'],
  publisher: ['M9 12l2 2 4-4', 'M12 3l2.4 1.8 3-.2.9 2.9 2.5 1.7-1 2.8 1 2.8-2.5 1.7-.9 2.9-3-.2L12 21l-2.4-1.8-3 .2-.9-2.9-2.5-1.7 1-2.8-1-2.8 2.5-1.7.9-2.9 3 .2L12 3z'],
  node: ['M4 5h16v5H4z', 'M4 14h16v5H4z', 'M8 7.5h.01M8 16.5h.01'],
  settings: ['M10.3 4.3c.4-1.7 3-1.7 3.4 0a1.7 1.7 0 002.6 1.1c1.5-.9 3.3.8 2.4 2.4a1.7 1.7 0 001 2.5c1.8.4 1.8 3 0 3.4a1.7 1.7 0 00-1 2.6c.9 1.5-.9 3.3-2.4 2.4a1.7 1.7 0 00-2.6 1c-.4 1.8-3 1.8-3.4 0a1.7 1.7 0 00-2.6-1c-1.5.9-3.3-.9-2.4-2.4a1.7 1.7 0 00-1-2.6c-1.7-.4-1.7-3 0-3.4a1.7 1.7 0 001-2.5c-.9-1.6.9-3.3 2.4-2.4a1.7 1.7 0 002.6-1.1z', 'M15 12a3 3 0 11-6 0 3 3 0 016 0z'],
  balance: ['M3 7a2 2 0 012-2h14a2 2 0 012 2v10a2 2 0 01-2 2H5a2 2 0 01-2-2V7z', 'M16 12h.01', 'M3 9h18'],
  chart: ['M4 19h16', 'M4 15l4-4 4 3 7-7'],
  inbox: ['M4 13h4l1.5 2h5L16 13h4', 'M5 5h14l1 8v6H4v-6l1-8z'],
  sun: ['M12 17a5 5 0 100-10 5 5 0 000 10z', 'M12 1v2M12 21v2M4.2 4.2l1.4 1.4M18.4 18.4l1.4 1.4M1 12h2M21 12h2M4.2 19.8l1.4-1.4M18.4 5.6l1.4-1.4'],
  moon: ['M21 12.8A9 9 0 1111.2 3a7 7 0 009.8 9.8z'],
  globe: ['M12 21a9 9 0 100-18 9 9 0 000 18z', 'M3.6 9h16.8M3.6 15h16.8', 'M12 3a14 14 0 010 18 14 14 0 010-18z'],
  logout: ['M15 16l4-4-4-4', 'M19 12H9', 'M12 19H6a2 2 0 01-2-2V7a2 2 0 012-2h6'],
  menu: ['M4 6h16M4 12h16M4 18h16'],
  plus: ['M12 5v14M5 12h14'],
  refresh: ['M4 4v5h5', 'M20 20v-5h-5', 'M5.1 15a7 7 0 0012.4 2.5L20 15M4 9l2.5-2.5A7 7 0 0118.9 9'],
  trash: ['M4 7h16', 'M10 11v6M14 11v6', 'M5 7l1 12a2 2 0 002 2h8a2 2 0 002-2l1-12', 'M9 7V4h6v3'],
  edit: ['M4 20h4L19 9l-4-4L4 16v4z', 'M13.5 6.5l4 4'],
  more: ['M5 12h.01M12 12h.01M19 12h.01'],
  'grip-vertical': ['M9 5a1 1 0 11-2 0 1 1 0 012 0M17 5a1 1 0 11-2 0 1 1 0 012 0M9 12a1 1 0 11-2 0 1 1 0 012 0M17 12a1 1 0 11-2 0 1 1 0 012 0M9 19a1 1 0 11-2 0 1 1 0 012 0M17 19a1 1 0 11-2 0 1 1 0 012 0'],
  check: ['M5 13l4 4L19 7'],
  x: ['M6 6l12 12M18 6L6 18'],
  warning: ['M12 9v4', 'M12 17h.01', 'M10.3 3.9L2.4 17.5A2 2 0 004.1 20.5h15.8a2 2 0 001.7-3L13.7 3.9a2 2 0 00-3.4 0z'],
  info: ['M12 21a9 9 0 100-18 9 9 0 000 18z', 'M12 11v5', 'M12 8h.01'],
  upload: ['M4 17v2a2 2 0 002 2h12a2 2 0 002-2v-2', 'M12 3v12', 'M7 8l5-5 5 5'],
  download: ['M4 17v2a2 2 0 002 2h12a2 2 0 002-2v-2', 'M12 3v12', 'M7 10l5 5 5-5'],
  eye: ['M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7S2 12 2 12z', 'M12 15a3 3 0 100-6 3 3 0 000 6z'],
  'eye-off': ['M3 3l18 18', 'M10.6 10.6a2 2 0 002.8 2.8', 'M9.9 5.1A10 10 0 0112 5c6.4 0 10 7 10 7a17 17 0 01-3.2 4.2M6.6 6.6A17 17 0 002 12s3.6 7 10 7a9.7 9.7 0 005.4-1.6'],
  lock: ['M6 11h12v10H6z', 'M8 11V7a4 4 0 118 0v4'],
  'chevron-down': ['M6 9l6 6 6-6'],
  'chevron-right': ['M9 6l6 6-6 6'],
  'arrow-left': ['M19 12H5', 'M12 19l-7-7 7-7'],
  external: ['M14 4h6v6', 'M20 4l-9 9', 'M18 14v5a1 1 0 01-1 1H5a1 1 0 01-1-1V7a1 1 0 011-1h5'],
  search: ['M11 18a7 7 0 100-14 7 7 0 000 14z', 'M20 20l-4-4'],
  play: ['M7 4v16l13-8L7 4z'],
  stop: ['M6 6h12v12H6z'],
  clock: ['M12 21a9 9 0 100-18 9 9 0 000 18z', 'M12 7v5l3 3'],
  copy: ['M8 8h11v11H8z', 'M16 8V5H5v11h3'],
  bolt: ['M13 2L4 14h7l-1 8 9-12h-7l1-8z'],
  cpu: ['M7 7h10v10H7z', 'M10 10h4v4h-4z', 'M9 3v4M15 3v4M9 17v4M15 17v4M3 9h4M3 15h4M17 9h4M17 15h4'],
  filter: ['M3 5h18l-7 8v6l-4 2v-8L3 5z'],
  link: ['M10 14a4 4 0 005.66 0l3-3a4 4 0 00-5.66-5.66l-1 1', 'M14 10a4 4 0 00-5.66 0l-3 3a4 4 0 005.66 5.66l1-1'],
  code: ['M8 9l-4 3 4 3', 'M16 9l4 3-4 3', 'M14 5l-4 14'],
  eyeglass: ['M11 18a7 7 0 100-14 7 7 0 000 14z'],
  list: ['M4 6h.01M4 12h.01M4 18h.01', 'M9 6h11M9 12h11M9 18h11']
}

/** Every icon name <SIcon> can render, sorted. Authoritative for validation. */
export const ICON_NAMES: readonly string[] = Object.freeze(Object.keys(ICON_PATHS).sort())

/** Reports whether name is a renderable icon. */
export function hasIcon(name: unknown): name is string {
  return typeof name === 'string' && Object.prototype.hasOwnProperty.call(ICON_PATHS, name)
}

/**
 * Rendered instead of an unknown name: a box with a question mark, which no
 * real icon looks like. A blank-looking square would be indistinguishable
 * from a styling bug, so the placeholder is deliberately loud.
 */
export const MISSING_ICON_PATHS: string[] = ['M4 4h16v16H4z', 'M9.6 9.4a2.5 2.5 0 114 2c-.9.6-1.6 1.2-1.6 2.4', 'M12 17.5h.01']

const warned = new Set<string>()

/**
 * Paths for name, falling back to MISSING_ICON_PATHS. Unknown names are
 * warned about once per (name, where) pair — silence is what let
 * anthropic's `"icon": "list"` ship as an empty square.
 *
 * @param where caller context for the warning, e.g. `plugin "anthropic" menu "anthropic-models"`
 */
export function iconPaths(name: string, where?: string): string[] {
  if (hasIcon(name)) return ICON_PATHS[name]
  const k = `${name}\u0000${where || ''}`
  if (!warned.has(k)) {
    warned.add(k)
    console.warn(
      `[@sub2api/ui] unknown icon "${name}"${where ? ` (${where})` : ''}; rendering the missing-icon placeholder. ` +
        `Valid names: import { ICON_NAMES } from "@sub2api/ui".`
    )
  }
  return MISSING_ICON_PATHS
}
