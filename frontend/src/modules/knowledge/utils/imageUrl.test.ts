import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: {
    getAuthHeaders: vi.fn(() => ({ authorization: "Bearer test-token" })),
  },
}));

vi.mock("@/components/request", () => ({
  BASE_URL: "https://api.example.com",
  localizeErrorCode: (code?: string) => `error:${code}`,
}));

vi.mock("@/modules/knowledge/utils/request", () => ({
  normalizeProxyableUrl: (uri?: string) => uri || "",
}));

import {
  basenameFromPath,
  collapseImagesToKeys,
  expandImagesInMarkdown,
  isExpiredSignedUrl,
  resolveCoreAssetUrl,
  resolveMarkdownImageUrl,
  resolveMarkdownImageUrlAsync,
} from "./imageUrl";

describe("isExpiredSignedUrl", () => {
  it("returns false when the url has no expires param", () => {
    expect(isExpiredSignedUrl("/static-files/a.png")).toBe(false);
  });

  it("returns false for an expires timestamp in the future", () => {
    const future = Math.floor(Date.now() / 1000) + 3600;
    expect(isExpiredSignedUrl(`/static-files/a.png?expires=${future}`)).toBe(false);
  });

  it("returns true for an expires timestamp in the past", () => {
    const past = Math.floor(Date.now() / 1000) - 3600;
    expect(isExpiredSignedUrl(`/static-files/a.png?expires=${past}`)).toBe(true);
  });
});

describe("basenameFromPath", () => {
  it("returns the last path segment, stripping any query string", () => {
    expect(basenameFromPath("/a/b/c/file.png?x=1")).toBe("file.png");
  });

  it("returns the whole string when there is no slash", () => {
    expect(basenameFromPath("file.png")).toBe("file.png");
  });
});

describe("resolveCoreAssetUrl", () => {
  it("returns empty string for empty/undefined path", () => {
    expect(resolveCoreAssetUrl()).toBe("");
    expect(resolveCoreAssetUrl("")).toBe("");
  });

  it("prefixes a /static-files/ path with /api/core and the window origin", () => {
    expect(resolveCoreAssetUrl("/static-files/abc.png")).toBe(
      "http://localhost:3000/api/core/static-files/abc.png",
    );
  });

  it("passes through an absolute http(s) URL via normalizeProxyableUrl", () => {
    expect(resolveCoreAssetUrl("https://cdn.example.com/img.png")).toBe(
      "https://cdn.example.com/img.png",
    );
  });

  it("prefixes an /api/core/ path with the window origin", () => {
    expect(resolveCoreAssetUrl("/api/core/static-files/xyz.png")).toBe(
      "http://localhost:3000/api/core/static-files/xyz.png",
    );
  });

  it("returns raw upload-root-marker paths unchanged", () => {
    expect(resolveCoreAssetUrl("/var/lib/lazymind/uploads/foo.png")).toBe(
      "/var/lib/lazymind/uploads/foo.png",
    );
  });
});

describe("expandImagesInMarkdown", () => {
  it("returns non-string input unchanged", () => {
    expect(expandImagesInMarkdown(undefined as unknown as string)).toBeUndefined();
    expect(expandImagesInMarkdown("")).toBe("");
  });

  it("expands a static-files image reference to a resolved core asset url", () => {
    const md = "![alt text](/static-files/abc.png)";
    const result = expandImagesInMarkdown(md);
    expect(result).toBe(
      "![alt text](http://localhost:3000/api/core/static-files/abc.png)",
    );
  });

  it("leaves markdown without matching images unchanged", () => {
    const md = "no images here, just text";
    expect(expandImagesInMarkdown(md)).toBe(md);
  });

  it("leaves an image reference unchanged when resolution yields the same url", () => {
    const md = "![a](plain-relative-name.png)";
    expect(expandImagesInMarkdown(md)).toBe(md);
  });
});

describe("collapseImagesToKeys", () => {
  it("returns the source unchanged when keys is not an array", () => {
    expect(collapseImagesToKeys("text", undefined as unknown as string[])).toBe("text");
  });

  it("collapses an expanded signed url back to its storage key basename", () => {
    const keys = ["/static-files/abc.png?expires=999999999999"];
    const md =
      "![alt](http://localhost:3000/api/core/static-files/abc.png?expires=999999999999&sig=xyz)";
    const result = collapseImagesToKeys(md, keys);
    expect(result).toBe("![alt](abc.png)");
  });

  it("leaves the markdown unchanged when no key matches the image url", () => {
    const md = "![alt](/some/other/path.png)";
    expect(collapseImagesToKeys(md, ["/static-files/abc.png"])).toBe(md);
  });
});

describe("resolveMarkdownImageUrl", () => {
  it("returns data: urls unchanged", () => {
    expect(resolveMarkdownImageUrl("data:image/png;base64,AAA")).toBe(
      "data:image/png;base64,AAA",
    );
  });

  it("resolves a /static-files/ url via resolveCoreAssetUrl", () => {
    expect(resolveMarkdownImageUrl("/static-files/abc.png")).toBe(
      "http://localhost:3000/api/core/static-files/abc.png",
    );
  });

  it("returns the raw url unchanged when nothing matches and it is not http(s)", () => {
    expect(resolveMarkdownImageUrl("relative/name.png")).toBe("relative/name.png");
  });

  it("resolves via a matching imageKey basename", () => {
    const result = resolveMarkdownImageUrl("abc.png", ["/static-files/abc.png"]);
    expect(result).toBe("http://localhost:3000/api/core/static-files/abc.png");
  });
});

describe("resolveMarkdownImageUrlAsync", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("returns the trimmed value unchanged for empty or data: urls", async () => {
    expect(await resolveMarkdownImageUrlAsync("")).toBe("");
    expect(await resolveMarkdownImageUrlAsync("data:image/png;base64,AAA")).toBe(
      "data:image/png;base64,AAA",
    );
  });

  it("returns a plain https url unchanged when it has no upload markers", async () => {
    const url = "https://cdn.example.com/plain.png";
    expect(await resolveMarkdownImageUrlAsync(url)).toBe(url);
  });

  it("fetches a signed url for a /static-files/ path and resolves it via resolveCoreAssetUrl", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({
        urls: { "/static-files/async-1.png": "/static-files/async-1.png?expires=9999999999" },
      }),
    });
    vi.stubGlobal("fetch", fetchMock);

    const result = await resolveMarkdownImageUrlAsync("/static-files/async-1.png");

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(result).toBe(
      "http://localhost:3000/api/core/static-files/async-1.png?expires=9999999999",
    );
  });

  it("propagates an error when signing fails", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: false }));

    await expect(
      resolveMarkdownImageUrlAsync("/static-files/async-2.png"),
    ).rejects.toThrow("error:2000509");
  });
});
