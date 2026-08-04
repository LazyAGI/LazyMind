import { describe, expect, it } from "vitest";
import {
  collectMarketTags,
  filterInstalledSkills,
  filterMarketSkills,
  isMarketSkillInstalled,
  mapSkillAssetRecordToStructuredAsset,
} from "./skillHelpers";
import type { StructuredAsset } from "../../shared";
import type { SkillAssetRecord } from "../../skillApi";
import type { MarketSkillAsset } from "./skillMarketMockData";

const buildAsset = (overrides: Partial<StructuredAsset> = {}): StructuredAsset => ({
  id: "skill-1",
  name: "应急预案",
  description: "用于应急场景的模板",
  category: "应急",
  tags: ["模板"],
  content: "content",
  ...overrides,
});

describe("mapSkillAssetRecordToStructuredAsset", () => {
  it("maps the relevant fields from a skill asset record", () => {
    const record: SkillAssetRecord = {
      id: "skill-1",
      skillId: "skill-1",
      name: "示例",
      skillName: "示例",
      description: "desc",
      category: "cat",
      tags: ["a", "b"],
      content: "body",
      headRevisionId: "rev-1",
      draft: { hasUncommittedDraft: true, taskId: "task-1", version: 2 },
      autoEvo: true,
      isEnabled: false,
      deletedAt: "2024-01-01",
      deletedBy: "user-1",
    };

    expect(mapSkillAssetRecordToStructuredAsset(record)).toEqual({
      id: "skill-1",
      name: "示例",
      description: "desc",
      category: "cat",
      tags: ["a", "b"],
      content: "body",
      headRevisionId: "rev-1",
      draft: { hasUncommittedDraft: true, taskId: "task-1", version: 2 },
      autoEvo: true,
      isEnabled: false,
      deletedAt: "2024-01-01",
      deletedBy: "user-1",
    });
  });
});

describe("filterInstalledSkills", () => {
  const items = [
    buildAsset({ id: "1", name: "雨强阈值模板", description: "降雨监测", category: "监测" }),
    buildAsset({ id: "2", name: "边坡巡检", description: "巡检记录", category: "巡检" }),
  ];

  it("filters by category", () => {
    const result = filterInstalledSkills(items, { keyword: "", category: "监测", source: "all" });
    expect(result.map((item) => item.id)).toEqual(["1"]);
  });

  it("filters by keyword across name and description", () => {
    const result = filterInstalledSkills(items, { keyword: "巡检", source: "all" });
    expect(result.map((item) => item.id)).toEqual(["2"]);
  });

  it("filters out everything when source does not match (resolveSkillSourceType is always personal)", () => {
    const result = filterInstalledSkills(items, { keyword: "", source: "builtin" });
    expect(result).toHaveLength(0);
  });

  it("returns all items when no filters are active", () => {
    const result = filterInstalledSkills(items, { keyword: "", source: "all" });
    expect(result).toHaveLength(2);
  });
});

describe("filterMarketSkills", () => {
  const items = [
    buildAsset({ id: "1", name: "雨强阈值模板", tags: ["监测", "预警"] }) as MarketSkillAsset,
    buildAsset({ id: "2", name: "边坡巡检", tags: ["巡检"], description: "巡检记录" }) as MarketSkillAsset,
  ];
  items[0].marketSource = "builtin";
  items[1].marketSource = "admin";

  it("filters by tag", () => {
    const result = filterMarketSkills(items, { keyword: "", tag: "预警", source: "all" });
    expect(result.map((item) => item.id)).toEqual(["1"]);
  });

  it("filters by market source", () => {
    const result = filterMarketSkills(items, { keyword: "", tag: "all", source: "builtin" });
    expect(result.map((item) => item.id)).toEqual(["1"]);
  });

  it("filters by keyword", () => {
    const result = filterMarketSkills(items, { keyword: "巡检", tag: "all", source: "all" });
    expect(result.map((item) => item.id)).toEqual(["2"]);
  });
});

describe("isMarketSkillInstalled", () => {
  it("returns true when the market item itself is flagged installed", () => {
    const marketItem = buildAsset({ id: "m1" }) as MarketSkillAsset;
    marketItem.installed = true;
    expect(isMarketSkillInstalled([], marketItem)).toBe(true);
  });

  it("matches installed skills by marketItemId", () => {
    const marketItem = buildAsset({ id: "m1", name: "模板A" }) as MarketSkillAsset;
    marketItem.marketItemId = "market-1";
    const installed = buildAsset({ id: "s1", name: "不同名称" }) as MarketSkillAsset;
    installed.marketItemId = "market-1";

    expect(isMarketSkillInstalled([installed], marketItem)).toBe(true);
  });

  it("matches installed skills by normalized name when no marketItemId overlap", () => {
    const marketItem = buildAsset({ id: "m1", name: "模板A" }) as MarketSkillAsset;
    const installed = buildAsset({ id: "s1", name: "模板A" }) as MarketSkillAsset;

    expect(isMarketSkillInstalled([installed], marketItem)).toBe(true);
  });

  it("returns false when nothing matches", () => {
    const marketItem = buildAsset({ id: "m1", name: "模板A" }) as MarketSkillAsset;
    const installed = buildAsset({ id: "s1", name: "模板B" }) as MarketSkillAsset;

    expect(isMarketSkillInstalled([installed], marketItem)).toBe(false);
  });
});

describe("collectMarketTags", () => {
  it("dedupes and sorts tags from all items", () => {
    const items = [
      buildAsset({ tags: ["b标签", "a标签"] }),
      buildAsset({ tags: ["a标签", ""] }),
    ];
    expect(collectMarketTags(items)).toEqual(["a标签", "b标签"]);
  });

  it("returns an empty array when there are no tags", () => {
    expect(collectMarketTags([buildAsset({ tags: [] })])).toEqual([]);
  });
});
