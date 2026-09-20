import { beforeEach, describe, expect, it, vi } from "vitest";
const api = vi.hoisted(() => ({ apiCoreSkillsSkillIdGet: vi.fn(), apiCoreSkillOrganizePost: vi.fn() }));
vi.mock("@/api/generated/core-client", () => ({
  Configuration: class {}, SkillsApiFactory: () => api,
  SkillDraftsApiFactory: () => ({}), SkillFsApiFactory: () => ({}), SkillRevisionsApiFactory: () => ({}), SkillSharesApiFactory: () => ({}), SkillMarketApiFactory: () => ({}), SkillDiffApiFactory: () => ({}),
}));
vi.mock("@/components/request", () => ({ axiosInstance: {}, BASE_URL: "", localizeErrorCode: vi.fn() }));
import { buildSkillUpdatePayload, getSkillAssetDetail, normalizeSkillCallMode, organizeSkills } from "./skillApi";
describe("Skill discovery metadata and calling policy", () => {
  beforeEach(() => vi.clearAllMocks());
  it.each(["manual", "disabled"])("maps %s to manual only", (mode) => {
    expect(normalizeSkillCallMode(mode)).toBe("manual");
  });
  it("maps legacy disabled flags without losing resident or on demand modes", () => {
    expect(normalizeSkillCallMode(undefined, false)).toBe("manual");
    expect(normalizeSkillCallMode("priority")).toBe("priority");
    expect(normalizeSkillCallMode("on_demand")).toBe("on_demand");
  });
  it("preserves original revision and normalizes search metadata from detail", async () => {
    api.apiCoreSkillsSkillIdGet.mockResolvedValue({ data: { skill_id: "skill-1", name: "writer", category: "external", original_revision_id: "original", head_revision_id: "latest", field: "writing", tags: ["academic"], aliases: ["  论文  "], keywords: ["摘要"], call_mode: "manual" } });
    expect(await getSkillAssetDetail("skill-1", { loadContent: false })).toMatchObject({ originalRevisionId: "original", headRevisionId: "latest", category: "external", field: "writing", aliases: ["论文"], keywords: ["摘要"], callMode: "manual" });
  });
  it("serializes metadata and policy independently of description", () => {
    expect(buildSkillUpdatePayload({ description: "Write papers", field: "writing", tags: ["academic"], aliases: ["论文"], keywords: ["摘要"], callMode: "manual" })).toMatchObject({ description: "Write papers", field: "writing", tags: ["academic"], aliases: ["论文"], keywords: ["摘要"], call_mode: "manual" });
  });
  it.each([undefined, "deep"] as const)("sends selected organize mode %s (default light)", async (mode) => {
    api.apiCoreSkillOrganizePost.mockResolvedValue({ data: { requestid: "r", taskid: "t", status: "pending" } });
    await organizeSkills(["skills/internal/a", "skills/internal/b"], mode);
    expect(api.apiCoreSkillOrganizePost).toHaveBeenCalledWith({ skillOrganizeOpenAPIRequest: { requestid: expect.any(String), skills: ["skills/internal/a", "skills/internal/b"], mode: mode || "light" } }, { silentError: true });
  });
});
