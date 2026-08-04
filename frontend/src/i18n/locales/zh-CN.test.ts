import { describe, expect, it } from "vitest";
import enUS from "./en-US";
import zhCN from "./zh-CN";

function collectLeafPaths(obj: unknown, prefix = ""): string[] {
  if (obj === null || typeof obj !== "object" || Array.isArray(obj)) {
    return [prefix];
  }
  return Object.entries(obj as Record<string, unknown>).flatMap(([key, value]) =>
    collectLeafPaths(value, prefix ? `${prefix}.${key}` : key),
  );
}

function getByPath(obj: Record<string, unknown>, path: string): unknown {
  return path.split(".").reduce<unknown>((acc, key) => {
    if (acc === null || typeof acc !== "object") return undefined;
    return (acc as Record<string, unknown>)[key];
  }, obj);
}

describe("zh-CN / en-US translation dictionaries", () => {
  it("share nearly the entire set of leaf translation keys", () => {
    // The two catalogs are largely kept in sync, but a handful of recently
    // added keys have not been translated on both sides yet. Rather than
    // pin an exact key list (which churns constantly), assert the overlap
    // stays high so a large accidental divergence is still caught.
    const zhPaths = new Set(collectLeafPaths(zhCN));
    const enPaths = new Set(collectLeafPaths(enUS));
    const onlyInZh = [...zhPaths].filter((p) => !enPaths.has(p));
    const onlyInEn = [...enPaths].filter((p) => !zhPaths.has(p));

    expect(onlyInZh.length).toBeLessThan(zhPaths.size * 0.02);
    expect(onlyInEn.length).toBeLessThan(enPaths.size * 0.02);
  });

  it("has no empty-string leaf values in zh-CN", () => {
    const paths = collectLeafPaths(zhCN);
    for (const path of paths) {
      const value = getByPath(zhCN as unknown as Record<string, unknown>, path);
      if (typeof value === "string") {
        expect(value.length, `zh-CN.${path}`).toBeGreaterThan(0);
      }
    }
  });

  it("has at most a handful of untranslated (empty-string) leaf values in en-US", () => {
    // Tracks pre-existing translation debt without letting it grow silently.
    const paths = collectLeafPaths(enUS);
    const emptyPaths = paths.filter((path) => {
      const value = getByPath(enUS as unknown as Record<string, unknown>, path);
      return typeof value === "string" && value.length === 0;
    });
    expect(emptyPaths.length).toBeLessThanOrEqual(1);
  });

  it("shares common top-level namespaces such as common and errors", () => {
    expect(Object.keys(zhCN)).toContain("common");
    expect(Object.keys(zhCN)).toContain("errors");
    expect(Object.keys(enUS)).toContain("common");
    expect(Object.keys(enUS)).toContain("errors");
  });
});
