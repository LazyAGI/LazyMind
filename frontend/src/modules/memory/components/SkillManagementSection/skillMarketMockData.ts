import type { StructuredAsset } from "../../shared";

export type MarketSkillAsset = StructuredAsset & {
  isMock?: boolean;
  marketSource?: "builtin" | "admin";
  marketItemId?: string;
};

const createMockMarketSkill = (
  id: string,
  name: string,
  description: string,
  category: string,
  marketSource: "builtin" | "admin",
): MarketSkillAsset => ({
  id: `mock-market-${id}`,
  name,
  description,
  category,
  tags: [],
  content: `# ${name}\n\n${description}`,
  autoEvo: false,
  isEnabled: true,
  readonly: true,
  isMock: true,
  marketSource,
  marketItemId: `mock-market-${id}`,
});

export const SKILL_MARKET_MOCK_ASSETS: MarketSkillAsset[] = [
  createMockMarketSkill(
    "doc-review",
    "文档审阅助手",
    "用于审阅长文档，提取问题、风险点和修改建议，适合合同、报告和方案评审。",
    "文档处理",
    "builtin",
  ),
  createMockMarketSkill(
    "kb-qa",
    "知识库问答优化",
    "优化检索问答流程，补充问题改写、引用检查和答案结构化规则。",
    "知识库增强",
    "builtin",
  ),
];

export const isMockMarketSkill = (item: StructuredAsset): item is MarketSkillAsset =>
  Boolean((item as MarketSkillAsset).isMock);

export const getMarketSource = (item: StructuredAsset): "builtin" | "admin" | "personal" => {
  const marketSource = (item as MarketSkillAsset).marketSource;
  if (marketSource === "admin") {
    return "admin";
  }
  if (marketSource === "builtin") {
    return "builtin";
  }
  return "personal";
};

export const createInstalledSkillFromMock = (item: MarketSkillAsset): StructuredAsset => {
  const { isMock: _isMock, marketSource: _marketSource, marketItemId: _marketItemId, ...rest } = item;
  return {
    ...rest,
    id: `mock-installed-${item.id}`,
    readonly: false,
  };
};

export const resolveMarketSkillAssets = (
  skillAssets: StructuredAsset[],
): MarketSkillAsset[] => {
  if (skillAssets.length > 0) {
    return skillAssets as MarketSkillAsset[];
  }

  return SKILL_MARKET_MOCK_ASSETS;
};
