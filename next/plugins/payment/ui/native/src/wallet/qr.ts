// Minimal QR code encoder (ISO/IEC 18004): byte mode, error correction
// level M, versions 1–20 (up to 666 bytes, plenty for a payment link). The
// console ships no QR library and plugin UIs have no dependencies of their
// own, so the waiting dialog draws its code from this.

/** Square of modules, true = dark. */
export type QrMatrix = boolean[][]

// Level M, by version (index 0 unused): EC codewords per block, number of blocks.
const ECC_PER_BLOCK = [0, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26, 30, 22, 22, 24, 24, 28, 28, 26, 26, 26]
const BLOCKS = [0, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5, 5, 8, 9, 9, 10, 10, 11, 13, 14, 16]
const MAX_VERSION = 20
const FORMAT_BITS_M = 0

const bit = (x: number, i: number) => ((x >>> i) & 1) !== 0

function rawModules(ver: number): number {
  let n = (16 * ver + 128) * ver + 64
  if (ver >= 2) {
    const align = Math.floor(ver / 7) + 2
    n -= (25 * align - 10) * align - 55
    if (ver >= 7) n -= 36
  }
  return n
}

const dataCodewords = (ver: number) => Math.floor(rawModules(ver) / 8) - ECC_PER_BLOCK[ver] * BLOCKS[ver]

// ---- data codewords ----

function pickVersion(len: number): number {
  for (let v = 1; v <= MAX_VERSION; v++) {
    const header = 4 + (v < 10 ? 8 : 16)
    if (header + len * 8 <= dataCodewords(v) * 8) return v
  }
  throw new Error('qr: data too long')
}

function encodeData(bytes: Uint8Array, ver: number): number[] {
  const bits: number[] = []
  const put = (val: number, n: number) => {
    for (let i = n - 1; i >= 0; i--) bits.push((val >>> i) & 1)
  }
  put(4, 4)
  put(bytes.length, ver < 10 ? 8 : 16)
  bytes.forEach((b) => put(b, 8))
  const capacity = dataCodewords(ver) * 8
  put(0, Math.min(4, capacity - bits.length))
  put(0, (8 - (bits.length % 8)) % 8)
  for (let pad = 0xec; bits.length < capacity; pad ^= 0xec ^ 0x11) put(pad, 8)
  const out: number[] = []
  for (let i = 0; i < bits.length; i += 8) out.push(bits.slice(i, i + 8).reduce((a, b) => (a << 1) | b, 0))
  return out
}

// ---- Reed–Solomon over GF(256), polynomial 0x11D ----

function gfMul(x: number, y: number): number {
  let z = 0
  for (let i = 7; i >= 0; i--) {
    z = (z << 1) ^ ((z >>> 7) * 0x11d)
    z ^= ((y >>> i) & 1) * x
  }
  return z
}

function rsDivisor(degree: number): number[] {
  const res = new Array<number>(degree).fill(0)
  res[degree - 1] = 1
  let root = 1
  for (let i = 0; i < degree; i++) {
    for (let j = 0; j < degree; j++) {
      res[j] = gfMul(res[j], root)
      if (j + 1 < degree) res[j] ^= res[j + 1]
    }
    root = gfMul(root, 2)
  }
  return res
}

function rsRemainder(data: number[], divisor: number[]): number[] {
  const res = divisor.map(() => 0)
  for (const b of data) {
    const factor = b ^ (res.shift() as number)
    res.push(0)
    divisor.forEach((coef, i) => (res[i] ^= gfMul(coef, factor)))
  }
  return res
}

/** Splits the data into blocks, appends each block's EC codewords and interleaves them. */
function withEcc(data: number[], ver: number): number[] {
  const numBlocks = BLOCKS[ver]
  const ecc = ECC_PER_BLOCK[ver]
  const raw = Math.floor(rawModules(ver) / 8)
  const numShort = numBlocks - (raw % numBlocks)
  const shortLen = Math.floor(raw / numBlocks)
  const divisor = rsDivisor(ecc)
  const blocks: number[][] = []
  for (let i = 0, k = 0; i < numBlocks; i++) {
    const dat = data.slice(k, k + shortLen - ecc + (i < numShort ? 0 : 1))
    k += dat.length
    const rem = rsRemainder(dat, divisor)
    if (i < numShort) dat.push(0)
    blocks.push(dat.concat(rem))
  }
  const out: number[] = []
  for (let i = 0; i < blocks[0].length; i++) {
    blocks.forEach((b, j) => {
      if (i !== shortLen - ecc || j >= numShort) out.push(b[i])
    })
  }
  return out
}

// ---- the matrix ----

class Grid {
  readonly size: number
  readonly dark: QrMatrix
  readonly fixed: boolean[][]

  constructor(readonly ver: number) {
    this.size = ver * 4 + 17
    this.dark = Array.from({ length: this.size }, () => new Array<boolean>(this.size).fill(false))
    this.fixed = Array.from({ length: this.size }, () => new Array<boolean>(this.size).fill(false))
  }

  set(x: number, y: number, dark: boolean) {
    this.dark[y][x] = dark
    this.fixed[y][x] = true
  }
}

function alignPositions(ver: number, size: number): number[] {
  if (ver === 1) return []
  const n = Math.floor(ver / 7) + 2
  const step = Math.floor((ver * 8 + n * 3 + 5) / (n * 4 - 4)) * 2
  const res = [6]
  for (let pos = size - 7; res.length < n; pos -= step) res.splice(1, 0, pos)
  return res
}

function drawFinder(g: Grid, cx: number, cy: number) {
  for (let dy = -4; dy <= 4; dy++) {
    for (let dx = -4; dx <= 4; dx++) {
      const d = Math.max(Math.abs(dx), Math.abs(dy))
      const x = cx + dx
      const y = cy + dy
      if (x >= 0 && x < g.size && y >= 0 && y < g.size) g.set(x, y, d !== 2 && d !== 4)
    }
  }
}

function drawAlignment(g: Grid, cx: number, cy: number) {
  for (let dy = -2; dy <= 2; dy++) {
    for (let dx = -2; dx <= 2; dx++) g.set(cx + dx, cy + dy, Math.max(Math.abs(dx), Math.abs(dy)) !== 1)
  }
}

function drawFormat(g: Grid, mask: number) {
  const data = (FORMAT_BITS_M << 3) | mask
  let rem = data
  for (let i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537)
  const bits = ((data << 10) | rem) ^ 0x5412
  const s = g.size
  for (let i = 0; i <= 5; i++) g.set(8, i, bit(bits, i))
  g.set(8, 7, bit(bits, 6))
  g.set(8, 8, bit(bits, 7))
  g.set(7, 8, bit(bits, 8))
  for (let i = 9; i < 15; i++) g.set(14 - i, 8, bit(bits, i))
  for (let i = 0; i < 8; i++) g.set(s - 1 - i, 8, bit(bits, i))
  for (let i = 8; i < 15; i++) g.set(8, s - 15 + i, bit(bits, i))
  g.set(8, s - 8, true)
}

function drawVersion(g: Grid) {
  if (g.ver < 7) return
  let rem = g.ver
  for (let i = 0; i < 12; i++) rem = (rem << 1) ^ ((rem >>> 11) * 0x1f25)
  const bits = (g.ver << 12) | rem
  for (let i = 0; i < 18; i++) {
    const a = g.size - 11 + (i % 3)
    const b = Math.floor(i / 3)
    g.set(a, b, bit(bits, i))
    g.set(b, a, bit(bits, i))
  }
}

function drawFunctionPatterns(g: Grid) {
  const s = g.size
  for (let i = 0; i < s; i++) {
    g.set(6, i, i % 2 === 0)
    g.set(i, 6, i % 2 === 0)
  }
  drawFinder(g, 3, 3)
  drawFinder(g, s - 4, 3)
  drawFinder(g, 3, s - 4)
  const pos = alignPositions(g.ver, s)
  const last = pos.length - 1
  pos.forEach((y, i) =>
    pos.forEach((x, j) => {
      const corner = (i === 0 && j === 0) || (i === 0 && j === last) || (i === last && j === 0)
      if (!corner) drawAlignment(g, x, y)
    })
  )
  drawFormat(g, 0)
  drawVersion(g)
}

function drawCodewords(g: Grid, data: number[]) {
  const s = g.size
  let i = 0
  for (let right = s - 1; right >= 1; right -= 2) {
    if (right === 6) right = 5
    const upward = ((right + 1) & 2) === 0
    for (let vert = 0; vert < s; vert++) {
      const y = upward ? s - 1 - vert : vert
      for (const x of [right, right - 1]) {
        if (g.fixed[y][x] || i >= data.length * 8) continue
        g.dark[y][x] = bit(data[i >>> 3], 7 - (i & 7))
        i++
      }
    }
  }
}

// ---- masks ----

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

function applyMask(g: Grid, mask: number) {
  const f = MASKS[mask]
  for (let y = 0; y < g.size; y++) {
    for (let x = 0; x < g.size; x++) if (!g.fixed[y][x] && f(x, y)) g.dark[y][x] = !g.dark[y][x]
  }
}

/** Penalty of runs of five or more same-coloured modules in one line. */
function runPenalty(line: boolean[]): number {
  let score = 0
  let run = 1
  for (let i = 1; i <= line.length; i++) {
    if (i < line.length && line[i] === line[i - 1]) run++
    else {
      if (run >= 5) score += run - 2
      run = 1
    }
  }
  return score
}

/** Rules 1, 2 and 4 of the standard's mask evaluation (the finder-like rule is left out). */
function penalty(m: QrMatrix): number {
  const s = m.length
  let score = 0
  let darkCount = 0
  for (let i = 0; i < s; i++) {
    score += runPenalty(m[i]) + runPenalty(m.map((row) => row[i]))
  }
  for (let y = 0; y < s; y++) {
    for (let x = 0; x < s; x++) {
      if (m[y][x]) darkCount++
      const c = m[y][x]
      if (x < s - 1 && y < s - 1 && c === m[y][x + 1] && c === m[y + 1][x] && c === m[y + 1][x + 1]) score += 3
    }
  }
  return score + Math.floor(Math.abs(darkCount * 20 - s * s * 10) / (s * s)) * 10
}

/** The QR code of a text (UTF-8). */
export function encodeQr(text: string): QrMatrix {
  const bytes = new TextEncoder().encode(text)
  const ver = pickVersion(bytes.length)
  const data = withEcc(encodeData(bytes, ver), ver)
  let best: { score: number; matrix: QrMatrix } | null = null
  for (let mask = 0; mask < 8; mask++) {
    const g = new Grid(ver)
    drawFunctionPatterns(g)
    drawCodewords(g, data)
    applyMask(g, mask)
    drawFormat(g, mask)
    const score = penalty(g.dark)
    if (!best || score < best.score) best = { score, matrix: g.dark }
  }
  return (best as { matrix: QrMatrix }).matrix
}

/** SVG path data of the dark modules, offset by `margin` modules. */
export function qrPath(m: QrMatrix, margin = 4): string {
  const parts: string[] = []
  m.forEach((row, y) => row.forEach((dark, x) => dark && parts.push(`M${x + margin} ${y + margin}h1v1h-1z`)))
  return parts.join('')
}
