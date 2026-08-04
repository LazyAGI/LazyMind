import { describe, expect, it } from "vitest";
import { cloudAuthProviderOptions, cloudProviderOptions } from "./cloudProviderOptions";

describe("cloudProviderOptions", () => {
  it("includes all four provider types with local marked as adminOnly", () => {
    expect(cloudProviderOptions.map((item) => item.type)).toEqual([
      "local",
      "feishu",
      "notion",
      "googledrive",
    ]);
    const local = cloudProviderOptions.find((item) => item.type === "local");
    expect(local?.adminOnly).toBe(true);
  });

  it("assigns an icon to every provider option", () => {
    cloudProviderOptions.forEach((option) => {
      expect(option.icon).toBeTruthy();
    });
  });

  it("only defines logoUrl for feishu and notion", () => {
    const byType = Object.fromEntries(
      cloudProviderOptions.map((item) => [item.type, item.logoUrl]),
    );
    expect(byType.feishu).toContain("feishu.cn");
    expect(byType.notion).toContain("notion.so");
    expect(byType.local).toBeUndefined();
    expect(byType.googledrive).toBeUndefined();
  });
});

describe("cloudAuthProviderOptions", () => {
  it("excludes the local provider while preserving order", () => {
    expect(cloudAuthProviderOptions.map((item) => item.type)).toEqual([
      "feishu",
      "notion",
      "googledrive",
    ]);
  });

  it("is derived from cloudProviderOptions without mutating it", () => {
    expect(cloudProviderOptions).toHaveLength(4);
    expect(cloudAuthProviderOptions).toHaveLength(3);
  });
});
