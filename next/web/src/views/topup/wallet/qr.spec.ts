import { describe, expect, it } from 'vitest'
import { encodeQr, qrPath, type QrMatrix } from './qr'

// An independent reading of the symbols, after ISO/IEC 18004: the format
// information, and for version 1 the whole data (unmask, zigzag, Reed-Solomon
// syndromes, byte-mode segment), so the encoder is checked against the
// standard rather than against itself.

const MASKS: Array<(x: number, y: number) => boolean> = [
  (x, y) => (x + y) % 2 === 0,
  (_, y) => y % 2 === 0,
  (x) => x % 3 === 0,
  (x, y) => (x + y) % 3 === 0,
  (x, y) => (Math.floor(x / 3) + Math.floor(y / 2)) % 2 === 0,
  (x, y) => ((x * y) % 2) + ((x * y) % 3) === 0,
  (x, y) => (((x * y) % 2) + ((x * y) % 3)) % 2 === 0,
  (x, y) => (((x + y) % 2) + ((x * y) % 3)) % 2 === 0
]

/** BCH(15,5) remainder of the 5 format data bits (generator 0x537). */
function formatBch(data: number): number {
  let rem = data << 10
  for (let i = 14; i >= 10; i--) if ((rem >>> i) & 1) rem ^= 0x537 << (i - 10)
  return rem
}

/** The format information next to the top-left finder: error correction level and mask, checked. */
function readFormat(m: QrMatrix): { level: number; mask: number } {
  const at = (x: number, y: number): number => (m[y][x] ? 1 : 0)
  const cells = [0, 1, 2, 3, 4, 5, 7, 8].map((y) => at(8, y)).concat([8, 5, 4, 3, 2, 1, 0].map((x) => at(x, 8)))
  const raw = cells.reduce((v, b, i) => v | (b << i), 0) ^ 0x5412
  expect(raw & 0x3ff, 'format BCH').toBe(formatBch(raw >>> 10))
  return { level: raw >>> 13, mask: (raw >>> 10) & 7 }
}

/** Version 1 function modules: finders with separators and format areas, timing lines. */
const isFunctionV1 = (x: number, y: number) => (x < 9 && y < 9) || (x >= 13 && y < 9) || (x < 9 && y >= 13) || x === 6 || y === 6

/** The 26 codewords of a version 1 symbol, unmasked, in zigzag order. */
function readCodewordsV1(m: QrMatrix, mask: number): number[] {
  const bits: number[] = []
  for (let right = 20; right >= 1; right -= 2) {
    if (right === 6) right = 5
    for (let vert = 0; vert < 21; vert++) {
      const y = ((right + 1) & 2) === 0 ? 20 - vert : vert
      for (const x of [right, right - 1]) if (!isFunctionV1(x, y)) bits.push(Number(m[y][x] !== MASKS[mask](x, y)))
    }
  }
  return Array.from({ length: bits.length / 8 }, (_, i) => bits.slice(i * 8, i * 8 + 8).reduce((v, b) => (v << 1) | b, 0))
}

/** GF(256) (x^8+x^4+x^3+x^2+1): exp and log tables. */
const EXP: number[] = []
const LOG: number[] = []
for (let i = 0, x = 1; i < 255; i++, x = (x << 1) ^ (x & 0x80 ? 0x11d : 0)) [EXP[i], LOG[x]] = [x, i]
const mul = (a: number, b: number) => (a && b ? EXP[(LOG[a] + LOG[b]) % 255] : 0)

/** The block's syndromes at α^0…α^(ecc-1): all zero for a valid Reed-Solomon codeword. */
const syndromes = (block: number[], ecc: number) => Array.from({ length: ecc }, (_, i) => block.reduce((s, c) => mul(s, EXP[i]) ^ c, 0))

/** The bytes of a byte-mode segment at the start of the data codewords. */
function byteSegment(data: number[]): number[] {
  const bits = data.flatMap((c) => Array.from({ length: 8 }, (_, i) => (c >>> (7 - i)) & 1))
  const read = (from: number, n: number) => bits.slice(from, from + n).reduce((v, b) => (v << 1) | b, 0)
  expect(read(0, 4), 'byte mode').toBe(0b0100)
  return Array.from({ length: read(4, 8) }, (_, i) => read(12 + i * 8, 8))
}

describe('QR encoder', () => {
  it('encodes a short link as a valid version 1-M symbol that reads back', () => {
    const text = 'https://p.ex/q' // 14 bytes, version 1-M's capacity
    const m = encodeQr(text)
    expect(m).toHaveLength(21)
    const { level, mask } = readFormat(m)
    expect(level, 'level M').toBe(0)
    const words = readCodewordsV1(m, mask)
    expect(words).toHaveLength(26)
    expect(syndromes(words, 10)).toEqual(new Array(10).fill(0))
    expect(new TextDecoder().decode(new Uint8Array(byteSegment(words.slice(0, 16))))).toBe(text)
  })

  it('draws the finders, the timing lines and the dark module', () => {
    const m = encodeQr('wxp://f2f0abcdefghijklmnopqrstuvwxyz0123456789')
    const s = m.length
    for (const [cx, cy] of [[3, 3], [s - 4, 3], [3, s - 4]]) {
      for (let d = -3; d <= 3; d++) expect([m[cy - 3][cx + d], m[cy + d][cx - 3], m[cy + 3][cx + d], m[cy + d][cx + 3]]).toEqual([true, true, true, true])
      expect([m[cy - 2][cx], m[cy][cx - 2]]).toEqual([false, false])
    }
    for (let i = 8; i < s - 8; i++) expect([m[6][i], m[i][6]]).toEqual([i % 2 === 0, i % 2 === 0])
    expect(m[s - 8][8]).toBe(true)
    readFormat(m)
  })

  it('grows with the text: version by byte-mode capacity, up to version 20', () => {
    const size = (n: number) => encodeQr('x'.repeat(n)).length
    expect([size(14), size(15), size(84), size(85), size(666)]).toEqual([21, 25, 37, 41, 97])
    expect(() => encodeQr('x'.repeat(667))).toThrow(/too long/)
    expect(encodeQr('https://pay.example.com/a')).toEqual(encodeQr('https://pay.example.com/a'))
  })

  it('writes one square per dark module, offset by the margin', () => {
    const m = encodeQr('a')
    const dark = m.flat().filter(Boolean).length
    const path = qrPath(m, 4)
    expect(path.match(/M/g)).toHaveLength(dark)
    expect(path.startsWith('M4 4h1v1h-1z')).toBe(true)
  })
})
