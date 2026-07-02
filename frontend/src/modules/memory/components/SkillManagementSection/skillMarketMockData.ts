import type { StructuredAsset } from "../../shared";

export type MarketSkillAsset = StructuredAsset & {
  isMock?: boolean;
  marketSource?: "builtin" | "admin";
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
  protect: false,
  autoEvo: false,
  isEnabled: true,
  isBuiltinTemplate: true,
  readonly: true,
  builtinSkillUid: `mock-${id}`,
  isMock: true,
  marketSource,
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
  createMockMarketSkill(
    "research-report",
    "算法研究报告",
    "整理论文脉络、方法对比、复现条件和工程落地判断。",
    "推荐技能",
    "builtin",
  ),
  createMockMarketSkill(
    "company-research",
    "公司研究简报",
    "汇总公司业务、产品策略、公开信息和关键风险。",
    "业务流程",
    "builtin",
  ),
  createMockMarketSkill(
    "sheet-cleanup",
    "表格清洗助手",
    "识别重复值、缺失项和格式异常，生成清洗建议和处理步骤。",
    "文档处理",
    "builtin",
  ),
  createMockMarketSkill(
    "citation-check",
    "引用一致性检查",
    "检查报告、方案和知识库回答中的引用遗漏、错配和格式问题。",
    "知识库增强",
    "builtin",
  ),
  createMockMarketSkill(
    "competitive-brief",
    "竞品分析简报",
    "对比竞品定位、功能差异、价格和营销信息，输出决策摘要。",
    "业务流程",
    "admin",
  ),
  createMockMarketSkill(
    "bid-check",
    "投标材料检查",
    "检查投标材料完整性、格式要求、关键条款响应情况和常见遗漏项。",
    "文档处理",
    "admin",
  ),
  createMockMarketSkill(
    "sales-lead",
    "销售线索分析",
    "根据客户对话和表单内容总结线索质量，输出跟进建议和风险信号。",
    "业务流程",
    "admin",
  ),
  createMockMarketSkill(
    "meeting-note",
    "会议纪要整理",
    "将会议记录整理为行动项、风险点、负责人和截止时间，适合项目复盘。",
    "团队共享",
    "admin",
  ),
  createMockMarketSkill(
    "customer-voice",
    "客户声音归纳",
    "从访谈和群聊中聚合需求、阻力和可执行机会。",
    "业务流程",
    "admin",
  ),
  createMockMarketSkill(
    "deploy-check",
    "发布前检查",
    "整理发布前检查项，包括变更说明、回滚方案、依赖服务和风险确认。",
    "研发与运维",
    "admin",
  ),
];

export const isMockMarketSkill = (item: StructuredAsset): item is MarketSkillAsset =>
  Boolean((item as MarketSkillAsset).isMock);

export const getMarketSource = (item: StructuredAsset): "builtin" | "admin" | "personal" => {
  const marketSource = (item as MarketSkillAsset).marketSource;
  if (marketSource === "admin") {
    return "admin";
  }
  if (item.isBuiltinTemplate || item.builtinSkillUid || item.originBuiltinSkillUid) {
    return "builtin";
  }
  return "personal";
};

export const createInstalledSkillFromMock = (item: MarketSkillAsset): StructuredAsset => ({
  ...item,
  id: `mock-installed-${item.builtinSkillUid || item.id}`,
  isBuiltinTemplate: false,
  isMock: false,
  readonly: false,
  originBuiltinSkillUid: item.builtinSkillUid,
  marketSource: undefined,
});

export const resolveMarketSkillAssets = (
  skillAssets: StructuredAsset[],
): MarketSkillAsset[] => {
  const realMarketItems = skillAssets.filter(
    (item) => item.isBuiltinTemplate && !item.parentId,
  );

  if (realMarketItems.length > 0) {
    return skillAssets as MarketSkillAsset[];
  }

  return [...skillAssets, ...SKILL_MARKET_MOCK_ASSETS];
};
