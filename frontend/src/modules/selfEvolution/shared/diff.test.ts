import { describe, expect, it, vi } from "vitest";

vi.mock("@/components/request", () => ({ BASE_URL: "https://example.com" }));

import {
  buildCoreDownloadUrl,
  buildDiffFileTree,
  buildUnifiedDiffFromInlineMap,
  getDiffArtifactFiles,
  getDiffLineType,
  getDownloadFileName,
  getInlineDiffMap,
  getInlineDiffText,
  normalizeDiffPath,
  normalizeFetchedDiffArtifact,
  parseUnifiedDiff,
} from "./diff";

describe("buildCoreDownloadUrl", () => {
  it("passes through an absolute http(s) url unchanged", () => {
    expect(buildCoreDownloadUrl("https://cdn.example.com/a.diff")).toBe("https://cdn.example.com/a.diff");
  });

  it("prefixes a relative path with /api/core/", () => {
    expect(buildCoreDownloadUrl("artifacts/a.diff")).toBe("https://example.com/api/core/artifacts/a.diff");
  });

  it("avoids double-prefixing a path already under api/core/", () => {
    expect(buildCoreDownloadUrl("api/core/artifacts/a.diff")).toBe("https://example.com/api/core/artifacts/a.diff");
  });

  it("returns an empty string for a blank path", () => {
    expect(buildCoreDownloadUrl("")).toBe("");
    expect(buildCoreDownloadUrl(undefined)).toBe("");
  });
});

describe("getDiffArtifactFiles", () => {
  it("extracts a files array with valid diff paths", () => {
    const files = getDiffArtifactFiles({
      files: [
        { path: "a.ts", diff_path: "a.diff", additions: 3, deletions: 1 },
        { path: "b.ts" },
      ],
    });
    expect(files).toHaveLength(1);
    expect(files[0]).toMatchObject({ path: "a.ts", diffPath: "a.diff", additions: 3, deletions: 1 });
  });

  it("falls back to a single direct diff_path entry", () => {
    const files = getDiffArtifactFiles({ diff_path: "single.diff", path: "single.ts" });
    expect(files).toEqual([{ path: "single.ts", diffPath: "single.diff", additions: undefined, deletions: undefined, changeKind: undefined }]);
  });

  it("recurses into nested data/result/payload wrappers", () => {
    const files = getDiffArtifactFiles({ data: { files: [{ path: "a.ts", diff_path: "a.diff" }] } });
    expect(files).toHaveLength(1);
  });

  it("returns an empty array for unrelated values", () => {
    expect(getDiffArtifactFiles("not-a-record")).toEqual([]);
    expect(getDiffArtifactFiles(null)).toEqual([]);
  });
});

describe("getInlineDiffMap / buildUnifiedDiffFromInlineMap / getInlineDiffText", () => {
  it("finds an inline diff text map under known keys", () => {
    const map = getInlineDiffMap({ diff: { "a.ts": "diff content" } });
    expect(map).toEqual({ "a.ts": "diff content" });
  });

  it("joins multiple diff entries with a blank line separator", () => {
    const unified = buildUnifiedDiffFromInlineMap({ "a.ts": "diff a", "b.ts": "diff b" });
    expect(unified).toBe("diff a\n\ndiff b");
  });

  it("prefers an inline diff map over a plain diff string", () => {
    const text = getInlineDiffText({ diff: { "a.ts": "diff --git a/a b/a" } });
    expect(text).toContain("diff --git a/a b/a");
  });

  it("recognizes a raw unified diff string directly", () => {
    expect(getInlineDiffText("diff --git a/x b/x\n@@\n")).toContain("diff --git");
  });

  it("returns undefined when there's no diff content anywhere", () => {
    expect(getInlineDiffText({ unrelated: true })).toBeUndefined();
  });
});

describe("normalizeFetchedDiffArtifact", () => {
  it("returns content unchanged when it already has a diff --git header", () => {
    const content = "diff --git a/a b/a\n@@\n+x";
    expect(normalizeFetchedDiffArtifact({ path: "a", diffPath: "a.diff" }, content)).toBe(content);
  });

  it("adds a diff header while preserving existing --- / +++ file headers", () => {
    const content = "--- a/a\n+++ b/a\n@@\n+x";
    const result = normalizeFetchedDiffArtifact({ path: "a", diffPath: "a.diff" }, content);
    expect(result.startsWith("diff --git a/a b/a")).toBe(true);
  });

  it("synthesizes full diff headers when none are present", () => {
    const result = normalizeFetchedDiffArtifact({ path: "a.ts", diffPath: "a.diff" }, "+added line");
    expect(result).toContain("diff --git a/a.ts b/a.ts");
    expect(result).toContain("--- a/a.ts");
    expect(result).toContain("+++ b/a.ts");
  });

  it("returns an empty string for blank content", () => {
    expect(normalizeFetchedDiffArtifact({ path: "a.ts", diffPath: "a.diff" }, "   ")).toBe("");
  });
});

describe("getDownloadFileName", () => {
  it("extracts the filename from a download url, stripping query/hash", () => {
    expect(getDownloadFileName("https://x.com/dir/report.diff?token=1#frag", "fallback.diff")).toBe("report.diff");
  });

  it("returns the fallback name for a blank url", () => {
    expect(getDownloadFileName("", "fallback.diff")).toBe("fallback.diff");
  });
});

describe("getDiffLineType", () => {
  it("classifies meta/hunk/add/remove/context lines", () => {
    expect(getDiffLineType("diff --git a/x b/x")).toBe("meta");
    expect(getDiffLineType("@@ -1,2 +1,2 @@")).toBe("hunk");
    expect(getDiffLineType("+added")).toBe("add");
    expect(getDiffLineType("-removed")).toBe("remove");
    expect(getDiffLineType(" unchanged")).toBe("context");
  });
});

describe("normalizeDiffPath", () => {
  it("strips the a/ or b/ prefix", () => {
    expect(normalizeDiffPath("a/src/index.ts")).toBe("src/index.ts");
  });

  it("truncates everything before LazyMind/", () => {
    expect(normalizeDiffPath("a/Users/me/repo/LazyMind/src/index.ts")).toBe("src/index.ts");
  });
});

describe("parseUnifiedDiff", () => {
  it("splits a multi-file diff into separate ParsedDiffFile entries with counts", () => {
    const diffText = [
      "diff --git a/a.ts b/a.ts",
      "--- a/a.ts",
      "+++ b/a.ts",
      "+added",
      "-removed",
      "diff --git a/b.ts b/b.ts",
      "--- a/b.ts",
      "+++ b/b.ts",
      "+only added",
    ].join("\n");
    const files = parseUnifiedDiff(diffText);
    expect(files).toHaveLength(2);
    expect(files[0].additions).toBe(1);
    expect(files[0].deletions).toBe(1);
    expect(files[1].additions).toBe(1);
    expect(files[1].deletions).toBe(0);
  });

  it("creates a fallback file entry when there is no diff --git header", () => {
    const files = parseUnifiedDiff("");
    expect(files).toHaveLength(1);
    expect(files[0].id).toBe("diff-file-fallback");
  });
});

describe("buildDiffFileTree", () => {
  it("groups files under shared directory nodes and sorts dirs before files", () => {
    const files = parseUnifiedDiff(
      ["diff --git a/src/a.ts b/src/a.ts", "diff --git a/README.md b/README.md"].join("\n"),
    );
    const tree = buildDiffFileTree(files);
    expect(tree[0].nodeType).toBe("dir");
    expect(tree[0].name).toBe("src");
    expect(tree[1].name).toBe("README.md");
  });
});
