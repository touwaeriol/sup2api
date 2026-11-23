// The plugin package chosen on the plugins page, kept in memory until its
// consent page installs it or is left. Nothing is sent before the consent:
// a reload loses the file, and the page asks for it again.

let pending: File | null = null

export function setPendingUpload(file: File) {
  pending = file
}

export function pendingUpload(): File | null {
  return pending
}

export function clearPendingUpload() {
  pending = null
}
