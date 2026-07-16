const HASH_CHUNK_SIZE = 2 * 1024 * 1024; // 2MB

const K = new Uint32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1,
  0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
  0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786,
  0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147,
  0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
  0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b,
  0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a,
  0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
  0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
]);

function rotr(n: number, x: number): number {
  return (x >>> n) | (x << (32 - n));
}

function bytesToHex(bytes: Uint8Array): string {
  let hex = "";
  for (let i = 0; i < bytes.length; i++) {
    hex += bytes[i]!.toString(16).padStart(2, "0");
  }
  return hex;
}

/** Incremental SHA-256 hasher for chunked file hashing. */
class Sha256Hasher {
  private h0 = 0x6a09e667;
  private h1 = 0xbb67ae85;
  private h2 = 0x3c6ef372;
  private h3 = 0xa54ff53a;
  private h4 = 0x510e527f;
  private h5 = 0x9b05688c;
  private h6 = 0x1f83d9ab;
  private h7 = 0x5be0cd19;

  private readonly block = new Uint8Array(64);
  private blockOffset = 0;
  private bytesHashed = 0;

  update(chunk: Uint8Array): void {
    let offset = 0;
    while (offset < chunk.length) {
      const take = Math.min(64 - this.blockOffset, chunk.length - offset);
      this.block.set(chunk.subarray(offset, offset + take), this.blockOffset);
      this.blockOffset += take;
      offset += take;
      if (this.blockOffset === 64) {
        this.compress(this.block);
        this.blockOffset = 0;
      }
    }
    this.bytesHashed += chunk.length;
  }

  digest(): Uint8Array {
    const bitLenHi = Math.floor(this.bytesHashed / 0x20000000);
    const bitLenLo = (this.bytesHashed << 3) >>> 0;

    this.block[this.blockOffset++] = 0x80;
    if (this.blockOffset > 56) {
      this.block.fill(0, this.blockOffset);
      this.compress(this.block);
      this.blockOffset = 0;
    }
    this.block.fill(0, this.blockOffset, 56);
    const view = new DataView(this.block.buffer);
    view.setUint32(56, bitLenHi, false);
    view.setUint32(60, bitLenLo, false);
    this.compress(this.block);

    const out = new Uint8Array(32);
    const outView = new DataView(out.buffer);
    outView.setUint32(0, this.h0, false);
    outView.setUint32(4, this.h1, false);
    outView.setUint32(8, this.h2, false);
    outView.setUint32(12, this.h3, false);
    outView.setUint32(16, this.h4, false);
    outView.setUint32(20, this.h5, false);
    outView.setUint32(24, this.h6, false);
    outView.setUint32(28, this.h7, false);
    return out;
  }

  private compress(block: Uint8Array): void {
    const w = new Uint32Array(64);
    const view = new DataView(block.buffer, block.byteOffset, 64);
    for (let i = 0; i < 16; i++) {
      w[i] = view.getUint32(i * 4, false);
    }
    for (let i = 16; i < 64; i++) {
      const s0 =
        rotr(7, w[i - 15]!) ^ rotr(18, w[i - 15]!) ^ (w[i - 15]! >>> 3);
      const s1 =
        rotr(17, w[i - 2]!) ^ rotr(19, w[i - 2]!) ^ (w[i - 2]! >>> 10);
      w[i] = (w[i - 16]! + s0 + w[i - 7]! + s1) >>> 0;
    }

    let a = this.h0;
    let b = this.h1;
    let c = this.h2;
    let d = this.h3;
    let e = this.h4;
    let f = this.h5;
    let g = this.h6;
    let h = this.h7;

    for (let i = 0; i < 64; i++) {
      const S1 = rotr(6, e) ^ rotr(11, e) ^ rotr(25, e);
      const ch = (e & f) ^ (~e & g);
      const temp1 = (h + S1 + ch + K[i]! + w[i]!) >>> 0;
      const S0 = rotr(2, a) ^ rotr(13, a) ^ rotr(22, a);
      const maj = (a & b) ^ (a & c) ^ (b & c);
      const temp2 = (S0 + maj) >>> 0;

      h = g;
      g = f;
      f = e;
      e = (d + temp1) >>> 0;
      d = c;
      c = b;
      b = a;
      a = (temp1 + temp2) >>> 0;
    }

    this.h0 = (this.h0 + a) >>> 0;
    this.h1 = (this.h1 + b) >>> 0;
    this.h2 = (this.h2 + c) >>> 0;
    this.h3 = (this.h3 + d) >>> 0;
    this.h4 = (this.h4 + e) >>> 0;
    this.h5 = (this.h5 + f) >>> 0;
    this.h6 = (this.h6 + g) >>> 0;
    this.h7 = (this.h7 + h) >>> 0;
  }
}

/**
 * Compute SHA-256 of a browser File's raw bytes.
 * Reads the file in chunks to avoid loading the entire file into memory.
 * Returns a 64-char lowercase hex string.
 */
export async function computeFileSha256(file: File): Promise<string> {
  // Prefer native digest for smaller files; fall back to incremental hashing for large ones.
  if (typeof crypto !== "undefined" && crypto.subtle && file.size <= HASH_CHUNK_SIZE * 8) {
    const buffer = await file.arrayBuffer();
    const digest = await crypto.subtle.digest("SHA-256", buffer);
    return bytesToHex(new Uint8Array(digest));
  }

  const hasher = new Sha256Hasher();
  let offset = 0;
  while (offset < file.size) {
    const end = Math.min(offset + HASH_CHUNK_SIZE, file.size);
    const chunk = new Uint8Array(await file.slice(offset, end).arrayBuffer());
    hasher.update(chunk);
    offset = end;
  }
  return bytesToHex(hasher.digest());
}

export const CHECK_HASHES_BATCH_SIZE = 500;
