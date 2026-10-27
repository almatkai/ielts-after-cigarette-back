// Redis-compatible p=14 registers, MurmurHash2/64A, Ertl's histogram estimator.
// Encoding reference: https://github.com/redis/redis/blob/unstable/src/hyperloglog.c
const M = 16384,
  MASK = (1n << 64n) - 1n,
  mult = 0xc6a4a7935bd1e995n;
const bytes = (s) => Uint8Array.from(atob(s), (c) => c.charCodeAt(0));
const encoded = (a) => btoa(String.fromCharCode(...a));
export function emptyHLL() {
  return { hll: encoded(new Uint8Array(M)) };
}
export function importHLL(base64) {
  const b = bytes(base64),
    r = new Uint8Array(M);
  if (new TextDecoder().decode(b.subarray(0, 4)) !== "HYLL" || b.length < 16)
    throw Error("Invalid source HLL");
  if (b[4] === 0) {
    if (b.length !== 12304) throw Error("Invalid dense HLL");
    for (let i = 0; i < M; i++) {
      const bit = i * 6,
        j = 16 + (bit >> 3);
      r[i] = ((b[j] >> (bit & 7)) | ((b[j + 1] ?? 0) << (8 - (bit & 7)))) & 63;
    }
  } else if (b[4] === 1) {
    let register = 0;
    for (let i = 16; i < b.length; ) {
      const v = b[i++];
      let count,
        value = 0;
      if (v & 128) {
        count = (v & 3) + 1;
        value = ((v >> 2) & 31) + 1;
      } else if (v & 64) {
        if (i >= b.length) throw Error("Truncated HLL");
        count = (((v & 63) << 8) | b[i++]) + 1;
      } else count = (v & 63) + 1;
      if (register + count > M) throw Error("Invalid sparse HLL");
      r.fill(value, register, register + count);
      register += count;
    }
    if (register !== M) throw Error("Invalid sparse HLL");
  } else throw Error("Unsupported source HLL encoding");
  if (r.some((v) => v > 51)) throw Error("Invalid HLL register");
  return { hll: encoded(r) };
}
export function murmur(value) {
  const data = new TextEncoder().encode(value);
  let hash = (0xadc83b19n ^ (BigInt(data.length) * mult)) & MASK,
    i = 0;
  for (; i + 8 <= data.length; i += 8) {
    let k = 0n;
    for (let j = 0; j < 8; j++) k |= BigInt(data[i + j]) << BigInt(j * 8);
    k = (k * mult) & MASK;
    k ^= k >> 47n;
    k = (k * mult) & MASK;
    hash = ((hash ^ k) * mult) & MASK;
  }
  if (i < data.length) {
    let k = 0n;
    for (let j = 0; i + j < data.length; j++)
      k |= BigInt(data[i + j]) << BigInt(j * 8);
    hash = ((hash ^ k) * mult) & MASK;
  }
  hash ^= hash >> 47n;
  hash = (hash * mult) & MASK;
  return hash ^ (hash >> 47n);
}
export function addHLL(state, values) {
  const registers = bytes(state.hll);
  let changed = false;
  for (const value of values) {
    let h = murmur(String(value));
    const i = Number(h & 16383n);
    h = (h >> 14n) | (1n << 50n);
    let rank = 1;
    while (!(h & 1n)) {
      rank++;
      h >>= 1n;
    }
    if (rank > registers[i]) {
      registers[i] = rank;
      changed = true;
    }
  }
  return { state: { hll: encoded(registers) }, changed };
}
export function countHLL(states) {
  if (!states.length) return 0;
  const registers = new Uint8Array(M);
  for (const state of states) {
    const r = bytes(state.hll);
    for (let i = 0; i < M; i++) registers[i] = Math.max(registers[i], r[i]);
  }
  const histogram = new Uint32Array(52);
  for (const r of registers) histogram[r]++;
  let z = M * tau((M - histogram[51]) / M);
  for (let i = 50; i >= 1; i--) z = (z + histogram[i]) * 0.5;
  z += M * sigma(histogram[0] / M);
  return Math.round((0.7213475204444817 * M * M) / z);
}
function sigma(x) {
  if (x === 1) return Infinity;
  let z = x,
    power = 1;
  for (;;) {
    x *= x;
    const previous = z;
    z += x * power;
    power *= 2;
    if (previous === z) return z;
  }
}
function tau(x) {
  if (x === 0 || x === 1) return 0;
  let z = 1 - x,
    power = 1;
  for (;;) {
    x = Math.sqrt(x);
    power *= 0.5;
    const previous = z;
    z -= (1 - x) * (1 - x) * power;
    if (previous === z) return z / 3;
  }
}
