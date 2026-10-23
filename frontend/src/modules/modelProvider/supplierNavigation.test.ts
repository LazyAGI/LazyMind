import { describe, expect, it } from "vitest";
import {
  filterSupplierNavigation,
  hasProgrammingScopeNotice,
  programmingScopeLink,
  projectSupplierNavigation,
} from "./supplierNavigation";
import type { OrderedSupplier } from "./supplierOrdering";

interface Supplier extends OrderedSupplier { configured?: boolean }

const services: Supplier[] = [
  { id: "ark-standard", name: "Doubao", brandKey: "Doubao", serviceKey: "standard" },
  { id: "ark-coding", name: "DoubaoCodingPlan", brandKey: "Doubao", serviceKey: "coding" },
  { id: "ark-agent", name: "DoubaoAgentPlan", brandKey: "Doubao", serviceKey: "agent" },
  { id: "nova-standard", name: "SenseNova", brandKey: "SenseNova", serviceKey: "standard" },
  { id: "nova-token", name: "SenseNovaTokenPlan", brandKey: "SenseNova", serviceKey: "token" },
  { id: "qwen-standard", name: "Qwen", brandKey: "Qwen", serviceKey: "standard" },
  { id: "qwen-coding", name: "QwenCodingPlan", brandKey: "Qwen", serviceKey: "coding" },
  { id: "qwen-token", name: "QwenTokenPlan", brandKey: "Qwen", serviceKey: "token" },
  { id: "qwen-team", name: "QwenTokenPlanTeam", brandKey: "Qwen", serviceKey: "token_team" },
  { id: "glm-standard", name: "GLM", brandKey: "GLM", serviceKey: "standard" },
  { id: "glm-coding", name: "GLMCodingPlan", brandKey: "GLM", serviceKey: "coding" },
  { id: "kimi-standard", name: "Kimi", brandKey: "Kimi", serviceKey: "standard" },
  { id: "kimi-code", name: "KimiCode", brandKey: "Kimi", serviceKey: "code" },
  { id: "minimax-standard", name: "Minimax", brandKey: "Minimax", serviceKey: "standard" },
  { id: "minimax-plan", name: "MiniMaxMPlan", brandKey: "Minimax", serviceKey: "m_plan" },
];
const singles = ["Claude", "DeepSeek", "OpenAI", "OpenRouter", "SiliconFlow", "vLLM", "LMDeploy"]
  .map((name) => ({ id: name.toLowerCase(), name }));
const custom: Supplier[] = [
  { id: "custom-aihubmix", name: "AIhubmix", isCustom: true, protocolName: "Doubao", brandKey: "Doubao", serviceKey: "standard" },
  { id: "custom-packyapi", name: "packyapi", is_custom: true, protocol_name: "Doubao", brand_key: "Doubao", service_key: "coding" },
  { id: "custom-qwen", name: "qwen自用", isCustom: true, protocolName: "OpenAI", brandKey: "Qwen", serviceKey: "standard" },
];

describe("supplier navigation projection", () => {
  it("projects fifteen services, seven single brands and three custom identities into sixteen top-level nodes", () => {
    const nodes = projectSupplierNavigation([...services, ...singles, ...custom]);
    expect(nodes).toHaveLength(16);
    expect(nodes.filter((node) => node.isBrandGroup).map((node) => node.id)).toEqual([
      "brand:Doubao", "brand:SenseNova", "brand:Qwen", "brand:GLM", "brand:Kimi", "brand:Minimax",
    ]);
    expect(nodes.flatMap((node) => node.children)).toHaveLength(25);
    for (const supplier of custom) {
      const node = nodes.find((candidate) => candidate.id === supplier.id);
      expect(node?.isBrandGroup).toBe(false);
      expect(node?.children).toEqual([supplier]);
    }
    expect(nodes.find((node) => node.id === "brand:Doubao")?.children.map((child) => child.id))
      .toEqual(["ark-standard", "ark-coding", "ark-agent"]);
  });

  it("keeps service children in fixed catalog order while retaining their UUIDs and references", () => {
    const nodes = projectSupplierNavigation([...services].reverse());
    const qwen = nodes.find((node) => node.id === "brand:Qwen");
    expect(qwen?.children.map((child) => child.id)).toEqual(["qwen-standard", "qwen-coding", "qwen-token", "qwen-team"]);
    expect(qwen?.orderAliases).toEqual(["qwen-standard", "qwen-coding", "qwen-token", "qwen-team"]);
    expect(qwen?.children[0]).toBe(services[5]);
    expect(services[0].id).toBe("ark-standard");
  });

  it("keeps a brand with only one available service as a direct selectable leaf", () => {
    const nodes = projectSupplierNavigation([services[1], singles[0]]);
    expect(nodes.map((node) => node.id)).toEqual(["ark-coding", "claude"]);
    expect(nodes.every((node) => !node.isBrandGroup)).toBe(true);
  });

  it("matches a brand's current and historical aliases with every service visible", () => {
    const nodes = projectSupplierNavigation([...services, ...custom]);
    for (const query of ["阿里云百炼", "通义千问", "Alibaba Cloud Model Studio"]) {
      const visible = filterSupplierNavigation(nodes, query, false, () => false, "en-US");
      expect(visible.map((node) => node.id)).toEqual(["brand:Qwen"]);
      expect(visible[0].children.map((child) => child.id)).toEqual(["qwen-standard", "qwen-coding", "qwen-token", "qwen-team"]);
    }
  });

  it("matches a service label without revealing its nonmatching sibling services", () => {
    const visible = filterSupplierNavigation(projectSupplierNavigation(services), " Coding Plan ", false, () => false, "zh-CN");
    expect(visible.map((node) => node.id)).toEqual(["brand:Doubao", "brand:Qwen", "brand:GLM"]);
    expect(visible.flatMap((node) => node.children.map((child) => child.id))).toEqual(["ark-coding", "qwen-coding", "glm-coding"]);
    expect(visible.every((node) => node.isBrandGroup)).toBe(true);
  });

  it("filters configured leaves while retaining the parent and never returning an empty group", () => {
    const source = services.map((supplier) => ({ ...supplier, configured: ["ark-coding", "qwen-token"].includes(supplier.id) }));
    const nodes = projectSupplierNavigation(source);
    const visible = filterSupplierNavigation(nodes, "", true, (supplier) => supplier.configured === true);
    expect(visible.map((node) => node.id)).toEqual(["brand:Doubao", "brand:Qwen"]);
    expect(visible.flatMap((node) => node.children.map((child) => child.id))).toEqual(["ark-coding", "qwen-token"]);
    expect(visible.every((node) => node.isBrandGroup)).toBe(true);
    expect(filterSupplierNavigation(nodes, "标准API", true, (supplier) => supplier.configured === true, "zh-CN")).toEqual([]);
    expect(nodes.find((node) => node.id === "brand:Qwen")?.children).toHaveLength(4);
  });

  it("maps each restricted service to its own documentation without imposing a custom-template restriction", () => {
    const scopedIds = services.filter(hasProgrammingScopeNotice).map((supplier) => supplier.id);
    const documents: Record<string, string> = {
      "ark-coding": "https://docs.volcengine.com/docs/ark/coding-plan-personal-get-started?lang=zh",
      "ark-agent": "https://docs.volcengine.com/docs/ark/agent-plan-personal-other-tools?lang=zh",
      "qwen-coding": "https://help.aliyun.com/zh/model-studio/more-tools",
      "qwen-token": "https://help.aliyun.com/zh/model-studio/more-tools",
      "qwen-team": "https://help.aliyun.com/zh/model-studio/more-tools",
      "glm-coding": "https://docs.bigmodel.cn/cn/coding-plan/tool/others",
      "kimi-code": "https://www.kimi.com/code/docs/en/kimi-code/community-guidelines.html",
      "minimax-plan": "https://platform.minimax.io/docs/m-plan/faq",
    };
    expect(scopedIds).toEqual(Object.keys(documents));
    for (const supplier of services.filter(hasProgrammingScopeNotice)) {
      expect(programmingScopeLink(supplier)).toBe(documents[supplier.id]);
    }
    expect(custom.every((supplier) => !hasProgrammingScopeNotice(supplier))).toBe(true);
    for (const supplier of [...custom, ...services.filter((item) => !hasProgrammingScopeNotice(item))]) {
      expect(programmingScopeLink(supplier)).toBe("");
    }
  });
});
