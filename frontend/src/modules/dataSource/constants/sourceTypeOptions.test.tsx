import { describe, expect, it } from "vitest";
import { sourceTypeOptions } from "./sourceTypeOptions";

describe("sourceTypeOptions", () => {
  it("includes local, feishu and notion source types", () => {
    const types = sourceTypeOptions.map((option) => option.type);
    expect(types).toEqual(["local", "feishu", "notion"]);
  });

  it("marks the local source type as admin only", () => {
    const local = sourceTypeOptions.find((option) => option.type === "local");
    expect(local?.adminOnly).toBe(true);
  });

  it("does not mark cloud source types as admin only", () => {
    const feishu = sourceTypeOptions.find((option) => option.type === "feishu");
    const notion = sourceTypeOptions.find((option) => option.type === "notion");
    expect(feishu?.adminOnly).toBeUndefined();
    expect(notion?.adminOnly).toBeUndefined();
  });

  it("provides a logo url for cloud providers but not for local", () => {
    const local = sourceTypeOptions.find((option) => option.type === "local");
    const feishu = sourceTypeOptions.find((option) => option.type === "feishu");
    expect(local?.logoUrl).toBeUndefined();
    expect(feishu?.logoUrl).toContain("feishu.cn");
  });

  it("provides an icon node for every source type", () => {
    sourceTypeOptions.forEach((option) => {
      expect(option.icon).toBeTruthy();
    });
  });
});
