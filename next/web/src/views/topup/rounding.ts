// Kept apart from the views (no Vue, no messages) so the mock rounds as the
// console does.

/**
 * Rounds half away from zero to `digits` decimals, as the server does. The
 * product is cut to 12 significant digits first, so float noise (693.4999…
 * for 100 × 7.3 × 0.95) does not decide the rounding.
 */
export const roundTo = (v: number, digits: number) => {
  const f = 10 ** digits
  return (Math.sign(v) * Math.round(Number((Math.abs(v) * f).toPrecision(12)))) / f
}
