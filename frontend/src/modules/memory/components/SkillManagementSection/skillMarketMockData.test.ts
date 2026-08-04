import { describe, expect, it } from "vitest";
import { getMarketSource, mapMarketSkillRecordToAsset } from "./skillMarketMockData";
import type { MarketSkillRecord } from "../../skillApi";

describe("getMarketSource", () => {
  it("returns 'admin' when marketSource is admin", () => {
    expect(getMarketSource({ marketSource: "admin" } as never)).toBe("admin");
  });

  it("returns 'builtin' when marketSource is builtin", () => {
    expect(getMarketSource({ marketSource: "builtin" } as never)).toBe("builtin");
  });

  it("returns 'personal' when marketSource is missing or unrecognized", () => {
    expect(getMarketSource({} as never)).toBe("personal");
    expect(getMarketSource({ marketSource: "other" } as never)).toBe("personal");
  });
});

describe("mapMarketSkillRecordToAsset", () => {
  const baseRecord: MarketSkillRecord = {
    id: "skill-1",
    skillId: "skill-1",
    name: "示例技能",
    skillName: "示例技能",
    description: "desc",
    category: "research",
    tags: ["a", "b"],
    content: "content",
    headRevisionId: "rev-1",
    autoEvo: false,
    isEnabled: true,
    marketItemId: "market-1",
    sourceSkillId: "skill-1",
    marketSource: "admin",
    installed: false,
  } as MarketSkillRecord;

  it("maps all relevant fields and forces readonly to true", () => {
    const asset = mapMarketSkillRecordToAsset(baseRecord);
    expect(asset).toMatchObject({
      id: "skill-1",
      name: "示例技能",
      description: "desc",
      category: "research",
      tags: ["a", "b"],
      content: "content",
      headRevisionId: "rev-1",
      marketSource: "admin",
      marketItemId: "market-1",
      sourceSkillId: "skill-1",
      installed: false,
      readonly: true,
    });
  });

  it("preserves the installed flag and installedSkillId when installed", () => {
    const asset = mapMarketSkillRecordToAsset({
      ...baseRecord,
      installed: true,
      installedSkillId: "installed-skill-1",
    });
    expect(asset.installed).toBe(true);
    expect(asset.installedSkillId).toBe("installed-skill-1");
  });
});
