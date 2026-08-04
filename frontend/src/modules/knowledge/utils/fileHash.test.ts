import { createHash } from "node:crypto";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CHECK_HASHES_BATCH_SIZE, computeFileSha256 } from "./fileHash";

function nodeSha256Hex(content: Uint8Array): string {
  return createHash("sha256").update(content).digest("hex");
}

describe("computeFileSha256", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("computes the correct SHA-256 hex digest for a small file via crypto.subtle", async () => {
    const content = new TextEncoder().encode("hello world");
    const file = new File([content], "hello.txt");

    const hash = await computeFileSha256(file);

    expect(hash).toBe(nodeSha256Hex(content));
    expect(hash).toHaveLength(64);
  });

  it("computes the correct digest for an empty file", async () => {
    const file = new File([], "empty.txt");
    const hash = await computeFileSha256(file);
    expect(hash).toBe(nodeSha256Hex(new Uint8Array()));
  });

  it("falls back to the incremental hasher when crypto.subtle is unavailable", async () => {
    const content = new TextEncoder().encode("fallback path content");
    const file = new File([content], "fallback.txt");

    vi.stubGlobal("crypto", { subtle: undefined });

    const hash = await computeFileSha256(file);
    expect(hash).toBe(nodeSha256Hex(content));
  });

  it("hashes content spanning multiple internal chunks identically to a single-shot hash", async () => {
    // 5MB forces at least two 2MB chunk reads inside the incremental hasher fallback path.
    const size = 5 * 1024 * 1024 + 37;
    const content = new Uint8Array(size);
    for (let i = 0; i < size; i++) {
      content[i] = i % 251;
    }
    const file = new File([content], "large.bin");

    vi.stubGlobal("crypto", { subtle: undefined });

    const hash = await computeFileSha256(file);
    expect(hash).toBe(nodeSha256Hex(content));
  });
});

describe("CHECK_HASHES_BATCH_SIZE", () => {
  it("is a positive batch size constant", () => {
    expect(CHECK_HASHES_BATCH_SIZE).toBe(500);
  });
});
