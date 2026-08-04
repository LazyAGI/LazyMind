import { beforeEach, describe, expect, it, vi } from "vitest";
import { axiosInstance } from "@/components/request";
import {
  RollbackConflictError,
  buildSkillUpdatePayload,
  compareSkillFileDiff,
  createSkillAsset,
  getSkillReviewSummary,
  hasSkillDraftChanges,
  isSkillAgentDraftContext,
  listSkillAssetsPage,
  listSkillMarketPage,
  listSkillReviewTasks,
  probeSkillAgentReviewMode,
  rollbackSkill,
  submitSkillDraftReviewActions,
} from "./skillApi";

const apiMocks = vi.hoisted(() => ({
  createSkill: vi.fn(),
  listSkills: vi.fn(),
  rollback: vi.fn(),
  diffFile: vi.fn(),
  listMarket: vi.fn(),
}));

vi.mock("@/components/request", () => ({
  axiosInstance: { get: vi.fn(), post: vi.fn() },
  BASE_URL: "",
  localizeErrorCode: (code?: string, fallback = "") => fallback || code || "",
}));

vi.mock("@/api/generated/core-client", () => ({
  Configuration: class {},
  SkillDiffApiFactory: () => ({
    apiCoreSkillDiffTreePost: vi.fn(),
    apiCoreSkillDiffFilePost: apiMocks.diffFile,
  }),
  SkillDraftsApiFactory: () => ({}),
  SkillFsApiFactory: () => ({}),
  SkillMarketApiFactory: () => ({
    apiCoreSkillMarketGet: apiMocks.listMarket,
  }),
  SkillRevisionsApiFactory: () => ({
    apiCoreSkillsSkillIdRollbackPost: apiMocks.rollback,
  }),
  SkillSharesApiFactory: () => ({}),
  SkillsApiFactory: () => ({
    apiCoreSkillsPost: apiMocks.createSkill,
    apiCoreSkillsGet: apiMocks.listSkills,
  }),
}));

const mockedGet = axiosInstance.get as unknown as ReturnType<typeof vi.fn>;
const mockedPost = axiosInstance.post as unknown as ReturnType<typeof vi.fn>;

beforeEach(() => {
  mockedGet.mockReset();
  mockedPost.mockReset();
  Object.values(apiMocks).forEach((mockFn) => mockFn.mockReset());
});

describe("hasSkillDraftChanges / isSkillAgentDraftContext", () => {
  it("reports draft changes when uncommitted or overlay exists", () => {
    expect(
      hasSkillDraftChanges({
        hasUncommittedDraft: false,
        overlayCount: 2,
        baseRevisionId: "",
        conversationId: "",
        draftVersion: 0,
        taskId: "",
      }),
    ).toBe(true);
    expect(
      hasSkillDraftChanges({
        hasUncommittedDraft: false,
        overlayCount: 0,
        baseRevisionId: "",
        conversationId: "",
        draftVersion: 0,
        taskId: "",
      }),
    ).toBe(false);
  });

  it("requires both draft changes and a task/conversation id for agent context", () => {
    const base = {
      hasUncommittedDraft: true,
      overlayCount: 0,
      baseRevisionId: "",
      draftVersion: 0,
    };
    expect(isSkillAgentDraftContext({ ...base, conversationId: "conv-1", taskId: "" })).toBe(true);
    expect(isSkillAgentDraftContext({ ...base, conversationId: "", taskId: "" })).toBe(false);
  });
});

describe("buildSkillUpdatePayload", () => {
  it("maps camelCase fields to the snake_case update request", () => {
    expect(
      buildSkillUpdatePayload({
        name: "n",
        description: "d",
        category: "c",
        tags: ["a"],
        autoEvo: true,
        isEnabled: false,
      }),
    ).toEqual({
      auto_evo: true,
      category: "c",
      description: "d",
      is_enabled: false,
      name: "n",
      tags: ["a"],
    });
  });
});

describe("createSkillAsset", () => {
  it("builds an uploaded_zip source request and returns the created skill id", async () => {
    apiMocks.createSkill.mockResolvedValue({ data: { skill_id: "sk-1" } });

    const result = await createSkillAsset({
      name: "skill",
      category: "cat",
      source: { type: "uploaded_zip", uploadId: "up-1" },
    });

    expect(apiMocks.createSkill).toHaveBeenCalledWith({
      skillCreateManagedOpenAPIRequest: expect.objectContaining({
        source: { type: "uploaded_zip", upload_id: "up-1" },
      }),
    });
    expect(result).toBe("sk-1");
  });

  it("builds a url source request", async () => {
    apiMocks.createSkill.mockResolvedValue({ data: { skill_id: "sk-2" } });

    await createSkillAsset({
      name: "skill",
      category: "cat",
      source: { type: "url", url: "http://example.com/skill.zip" },
    });

    expect(apiMocks.createSkill).toHaveBeenCalledWith({
      skillCreateManagedOpenAPIRequest: expect.objectContaining({
        source: { type: "url", url: "http://example.com/skill.zip" },
      }),
    });
  });
});

describe("listSkillAssetsPage", () => {
  it("normalizes skill list items into structured asset records", async () => {
    apiMocks.listSkills.mockResolvedValue({
      data: {
        items: [
          {
            id: "sk-1",
            skill_id: "sk-1",
            name: "Skill One",
            description: "desc",
            category: "cat",
            tags: ["a", " ", "b"],
            auto_evo: true,
            is_enabled: false,
          },
        ],
        total: 1,
        page: 1,
        page_size: 200,
      },
    });

    const result = await listSkillAssetsPage({ keyword: "s" });

    expect(result.records[0]).toEqual(
      expect.objectContaining({
        id: "sk-1",
        skillId: "sk-1",
        name: "Skill One",
        tags: ["a", "b"],
        autoEvo: true,
        isEnabled: false,
      }),
    );
    expect(result.total).toBe(1);
  });

  it("falls back to defaults when the response omits pagination fields", async () => {
    apiMocks.listSkills.mockResolvedValue({ data: { items: [] } });
    const result = await listSkillAssetsPage();
    expect(result).toEqual({ records: [], total: 0, page: 1, pageSize: 200 });
  });
});

describe("listSkillMarketPage", () => {
  it("normalizes market items including nested source skill data", async () => {
    apiMocks.listMarket.mockResolvedValue({
      data: {
        items: [
          {
            market_item_id: "mi-1",
            source_skill_id: "sk-1",
            tags: ["a"],
            status: "published",
            installed: true,
            source: {
              id: "sk-1",
              skill_id: "sk-1",
              name: "Skill One",
              description: "desc",
              category: "cat",
            },
          },
        ],
        total: 1,
        page: 1,
        page_size: 20,
      },
    });

    const result = await listSkillMarketPage({ keyword: "kw" });

    expect(result.records[0]).toEqual(
      expect.objectContaining({
        marketItemId: "mi-1",
        sourceSkillId: "sk-1",
        name: "Skill One",
        installed: true,
      }),
    );
    expect(result.pageSize).toBe(20);
  });

  it("falls back to defaults when the response is empty", async () => {
    apiMocks.listMarket.mockResolvedValue({ data: {} });
    const result = await listSkillMarketPage();
    expect(result).toEqual({ records: [], total: 0, page: 1, pageSize: 20 });
  });
});

describe("rollbackSkill", () => {
  it("returns the new head revision on success", async () => {
    apiMocks.rollback.mockResolvedValue({
      data: { head_revision_id: "rev-2", revision_no: 3 },
    });

    const result = await rollbackSkill("sk-1", "rev-1");

    expect(apiMocks.rollback).toHaveBeenCalledWith({
      skillId: "sk-1",
      skillRollbackOpenAPIRequest: {
        revision_id: "rev-1",
        target_revision_id: "rev-1",
      },
    });
    expect(result).toEqual({ headRevisionId: "rev-2", revisionNo: 3 });
  });

  it("throws RollbackConflictError on 409 responses", async () => {
    apiMocks.rollback.mockRejectedValue({ response: { status: 409 } });
    await expect(rollbackSkill("sk-1", "rev-1")).rejects.toBeInstanceOf(RollbackConflictError);
  });

  it("rethrows other errors unchanged", async () => {
    const error = new Error("network down");
    apiMocks.rollback.mockRejectedValue(error);
    await expect(rollbackSkill("sk-1", "rev-1")).rejects.toBe(error);
  });
});

describe("compareSkillFileDiff / probeSkillAgentReviewMode", () => {
  it("normalizes a diff file response including review metadata", async () => {
    apiMocks.diffFile.mockResolvedValue({
      data: {
        path: "SKILL.md",
        status: "modified",
        review_id: "rev-123",
        review_version: 2,
      },
    });

    const result = await compareSkillFileDiff("sk-1", "SKILL.md");
    expect(result.path).toBe("SKILL.md");
    expect(result.review).toEqual(
      expect.objectContaining({ reviewId: "rev-123", reviewVersion: 2 }),
    );
  });

  it("returns false when the skill is not in an agent draft context", async () => {
    const result = await probeSkillAgentReviewMode(
      "sk-1",
      { hasUncommittedDraft: false, overlayCount: 0, taskId: "", conversationId: "", baseRevisionId: "", draftVersion: 0 },
      ["SKILL.md"],
    );
    expect(result).toBe(false);
    expect(apiMocks.diffFile).not.toHaveBeenCalled();
  });

  it("returns false when there are no changed paths even in agent draft context", async () => {
    const result = await probeSkillAgentReviewMode(
      "sk-1",
      { hasUncommittedDraft: true, overlayCount: 0, taskId: "task-1", conversationId: "", baseRevisionId: "", draftVersion: 0 },
      [],
    );
    expect(result).toBe(false);
  });

  it("returns true when the file diff has an active review session", async () => {
    apiMocks.diffFile.mockResolvedValue({
      data: { path: "SKILL.md", review_id: "rev-1" },
    });
    const result = await probeSkillAgentReviewMode(
      "sk-1",
      { hasUncommittedDraft: true, overlayCount: 0, taskId: "task-1", conversationId: "", baseRevisionId: "", draftVersion: 0 },
      ["SKILL.md"],
    );
    expect(result).toBe(true);
  });
});

describe("getSkillReviewSummary", () => {
  it("normalizes summary fields from a snake_case payload", async () => {
    mockedGet.mockResolvedValue({
      data: {
        qualified_session_count: 5,
        user_turn_count: 10,
        window_start: "2024-01-01",
        window_end: "2024-01-02",
      },
    });

    const result = await getSkillReviewSummary();
    expect(result).toEqual(
      expect.objectContaining({
        qualifiedSessionCount: 5,
        userTurnCount: 10,
        windowStart: "2024-01-01",
        windowEnd: "2024-01-02",
      }),
    );
  });
});

describe("listSkillReviewTasks", () => {
  it("filters out empty task entries and normalizes valid ones", async () => {
    mockedGet.mockResolvedValue({
      data: {
        items: [
          { requestid: "req-1", status: "running", run_status: "ok" },
          {},
        ],
        total: 1,
      },
    });

    const result = await listSkillReviewTasks({ page: 2, pageSize: 10 });
    expect(result.records).toHaveLength(1);
    expect(result.records[0]).toEqual(
      expect.objectContaining({ requestId: "req-1", status: "running" }),
    );
    expect(mockedGet).toHaveBeenCalledWith(
      expect.stringContaining("/skill-review/tasks"),
      expect.objectContaining({ params: expect.objectContaining({ page: 2, page_size: 10 }) }),
    );
  });
});

describe("submitSkillDraftReviewActions", () => {
  it("maps decision items to the API's accepted/rejected vocabulary", async () => {
    mockedPost.mockResolvedValue({
      data: { review_version: 3, can_undo: true },
    });

    const result = await submitSkillDraftReviewActions("sk-1", "rev-1", {
      expectedReviewVersion: 2,
      items: [{ hunkId: "h1", decision: "accept", path: "SKILL.md" }],
    });

    expect(mockedPost).toHaveBeenCalledWith(
      expect.stringContaining("/draft-review/rev-1/actions"),
      expect.objectContaining({
        expected_review_version: 2,
        items: [{ hunk_id: "h1", decision: "accepted", path: "SKILL.md" }],
      }),
    );
    expect(result).toEqual(
      expect.objectContaining({ reviewVersion: 3, canUndo: true }),
    );
  });
});
