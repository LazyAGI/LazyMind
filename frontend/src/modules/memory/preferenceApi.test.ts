import { beforeEach, describe, expect, it, vi } from "vitest";
import { axiosInstance } from "@/components/request";
import {
  RollbackConflictError,
  checkUserPreferenceConfigured,
  commitPersonalResourceDraft,
  confirmManagedPreferenceDraft,
  discardManagedPreferenceDraft,
  generateManagedPreferenceDraft,
  getPersonalResourceRevision,
  hasPersonalResourceDraftChanges,
  listEvolutionSuggestions,
  listPersonalResourceRevisions,
  listPreferenceAssets,
  parsePreferenceContent,
  previewManagedPreferenceDraft,
  readPersonalResourceFile,
  resolveManagedPreferenceDraftKind,
  resolvePersonalResourceApiType,
  reviewManagedPreferenceDraftHunks,
  rollbackPersonalResource,
  saveAndCommitPersonalResourceContent,
  serializePreferenceContent,
  writePersonalResourceDraft,
} from "./preferenceApi";

vi.mock("@/components/request", () => ({
  axiosInstance: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    patch: vi.fn(),
  },
  BASE_URL: "",
  localizeErrorCode: (code?: string, fallback = "") => fallback || `err:${code}`,
}));

const mockedGet = axiosInstance.get as unknown as ReturnType<typeof vi.fn>;
const mockedPost = axiosInstance.post as unknown as ReturnType<typeof vi.fn>;
const mockedPut = axiosInstance.put as unknown as ReturnType<typeof vi.fn>;

beforeEach(() => {
  mockedGet.mockReset();
  mockedPost.mockReset();
  mockedPut.mockReset();
});

describe("resolveManagedPreferenceDraftKind / resolvePersonalResourceApiType", () => {
  it("classifies memory (non-preference) resource types as memory", () => {
    expect(resolveManagedPreferenceDraftKind("memory")).toBe("memory");
    expect(resolvePersonalResourceApiType("memory")).toBe("memory");
  });

  it("classifies preference-like or unknown types as user-preference", () => {
    expect(resolveManagedPreferenceDraftKind("user_preference")).toBe("user-preference");
    expect(resolveManagedPreferenceDraftKind(undefined)).toBe("user-preference");
    expect(resolvePersonalResourceApiType("habit")).toBe("user_preference");
  });
});

describe("hasPersonalResourceDraftChanges", () => {
  it("returns true when draft status is a non-none value", () => {
    expect(
      hasPersonalResourceDraftChanges({ draftStatus: "pending", headContent: "a", draftContent: "a" }),
    ).toBe(true);
  });

  it("falls back to content comparison when status is none/empty", () => {
    expect(
      hasPersonalResourceDraftChanges({ draftStatus: "none", headContent: "a", draftContent: "b" }),
    ).toBe(true);
    expect(
      hasPersonalResourceDraftChanges({ headContent: "a ", draftContent: "a" }),
    ).toBe(false);
  });
});

describe("serializePreferenceContent / parsePreferenceContent", () => {
  it("serializes by trimming content", () => {
    expect(serializePreferenceContent({ title: "t", content: "  body  " })).toBe("body");
  });

  it("parses front matter fields and body text", () => {
    const raw = "title: 我的标题\nprotect: true\nagent_persona: 助手\n---\n\nbody text";
    const parsed = parsePreferenceContent(`---\n${raw}`);
    expect(parsed.title).toBe("我的标题");
    expect(parsed.protect).toBe(true);
    expect(parsed.agentPersona).toBe("助手");
    expect(parsed.content).toBe("body text");
  });

  it("falls back title to the first content line when no front matter title exists", () => {
    const parsed = parsePreferenceContent("first line\nsecond line", { id: "fallback-id" });
    expect(parsed.title).toBe("first line");
    expect(parsed.id).toBe("fallback-id");
  });
});

describe("listPreferenceAssets", () => {
  it("normalizes managed items and dedupes by resource id", async () => {
    mockedGet.mockResolvedValue({
      data: {
        items: [
          {
            resource_id: "pref-1",
            resource_type: "user_preference",
            title: "标题",
            content: "内容",
          },
          { resource_id: "not-managed" },
        ],
      },
    });

    const assets = await listPreferenceAssets();
    expect(assets).toHaveLength(1);
    expect(assets[0].id).toBe("pref-1");
  });
});

describe("checkUserPreferenceConfigured", () => {
  it("returns true when a preference asset has any configured field", async () => {
    mockedGet.mockResolvedValue({
      data: {
        items: [
          {
            resource_id: "pref-1",
            resource_type: "user_preference",
            title: "t",
            content: "c",
            agent_persona: "persona",
          },
        ],
      },
    });

    expect(await checkUserPreferenceConfigured()).toBe(true);
  });

  it("returns false when no matching preference asset exists", async () => {
    mockedGet.mockResolvedValue({ data: { items: [] } });
    expect(await checkUserPreferenceConfigured()).toBe(false);
  });

  it("returns false when the request throws", async () => {
    mockedGet.mockRejectedValue(new Error("network error"));
    expect(await checkUserPreferenceConfigured()).toBe(false);
  });
});

describe("listEvolutionSuggestions (legacy no-op)", () => {
  it("returns an empty result shape without calling the network", async () => {
    const result = await listEvolutionSuggestions({ page: 2, pageSize: 30 });
    expect(result).toEqual({ items: [], page: 2, pageSize: 30, total: 0, hasMore: false });
    expect(mockedGet).not.toHaveBeenCalled();
  });
});

describe("generateManagedPreferenceDraft / previewManagedPreferenceDraft / confirmManagedPreferenceDraft / discardManagedPreferenceDraft", () => {
  it("generates a draft using the user instruction or a default fallback", async () => {
    mockedPost.mockResolvedValue({
      data: { draft_content: "draft", draft_status: "pending", suggestion_ids: ["s1"] },
    });
    const result = await generateManagedPreferenceDraft("memory", { userInstruct: "" });
    expect(mockedPost).toHaveBeenCalledWith(
      expect.stringContaining("memory:generate"),
      expect.objectContaining({ user_instruct: expect.any(String) }),
    );
    expect(result.draftContent).toBe("draft");
    expect(result.suggestionIds).toEqual(["s1"]);
  });

  it("previews a draft and maps snake_case fields", async () => {
    mockedGet.mockResolvedValue({
      data: {
        draft_content: "draft",
        draft_status: "pending",
        draft_version: 3,
        head_content: "head",
        review_id: "review-1",
      },
    });
    const preview = await previewManagedPreferenceDraft("user-preference");
    expect(preview.draftVersion).toBe(3);
    expect(preview.currentContent).toBe("head");
    expect(preview.reviewId).toBe("review-1");
  });

  it("confirms a draft by first previewing to get expected version and review id", async () => {
    mockedGet.mockResolvedValue({ data: { draft_version: 5, review_id: "review-1" } });
    mockedPost.mockResolvedValue({ data: { revision_id: "rev-1", revision_no: 2 } });

    const result = await confirmManagedPreferenceDraft("user-preference");

    expect(mockedPost).toHaveBeenCalledWith(
      expect.stringContaining("user_preference:commit"),
      expect.objectContaining({ expected_draft_version: 5, source_ref_id: "review-1" }),
    );
    expect(result).toEqual({ content: "", revisionId: "rev-1", version: 2 });
  });

  it("discards a draft and reports whether it was discarded", async () => {
    mockedPost.mockResolvedValue({ data: { discarded: false } });
    expect(await discardManagedPreferenceDraft("memory")).toBe(false);
  });
});

describe("reviewManagedPreferenceDraftHunks", () => {
  it("submits hunk decisions and returns the mutation result", async () => {
    mockedPost.mockResolvedValue({
      data: { can_undo: true, draft_content: "d", draft_version: 2, review_version: 4 },
    });

    const result = await reviewManagedPreferenceDraftHunks("memory", {
      reviewId: "review-1",
      expectedReviewVersion: 3,
      items: [{ hunkId: "h1", decision: "accept" }],
    });

    expect(mockedPost).toHaveBeenCalledWith(
      expect.stringContaining("draft-review/review-1/actions"),
      expect.objectContaining({
        expected_review_version: 3,
        items: [{ hunk_id: "h1", decision: "accept" }],
      }),
    );
    expect(result.canUndo).toBe(true);
    expect(result.reviewVersion).toBe(4);
  });
});

describe("personal-resource file draft lifecycle", () => {
  it("reads a personal resource file and normalizes fields", async () => {
    mockedGet.mockResolvedValue({
      data: { content: "body", draft_version: 1, revision_no: 2, agent_persona: "persona" },
    });
    const file = await readPersonalResourceFile("memory", { ref: "draft" });
    expect(mockedGet).toHaveBeenCalledWith(expect.stringContaining("ref=draft"));
    expect(file.content).toBe("body");
    expect(file.agentPersona).toBe("persona");
  });

  it("writes a draft only including expectedDraftVersion when positive", async () => {
    mockedPut.mockResolvedValue({ data: { draft_version: 7 } });
    const version = await writePersonalResourceDraft("memory", { content: "x", expectedDraftVersion: 0 });
    expect(mockedPut).toHaveBeenCalledWith(
      expect.any(String),
      { content: "x" },
    );
    expect(version).toBe(7);
  });

  it("commits a draft with the given version and message", async () => {
    mockedPost.mockResolvedValue({ data: { revision_id: "rev-1", revision_no: 3 } });
    const result = await commitPersonalResourceDraft("memory", 7, "msg");
    expect(mockedPost).toHaveBeenCalledWith(
      expect.stringContaining(":commit"),
      { expected_draft_version: 7, message: "msg" },
    );
    expect(result).toEqual({ revisionId: "rev-1", revisionNo: 3 });
  });

  it("saveAndCommitPersonalResourceContent writes then commits", async () => {
    mockedPut.mockResolvedValue({ data: { draft_version: 9 } });
    mockedPost.mockResolvedValue({ data: { revision_id: "rev-2", revision_no: 4 } });

    const result = await saveAndCommitPersonalResourceContent("memory", "new content");

    expect(result).toEqual({ revisionId: "rev-2", revisionNo: 4, draftVersion: 9 });
  });
});

describe("listPersonalResourceRevisions / getPersonalResourceRevision", () => {
  it("normalizes a list of revisions", async () => {
    mockedGet.mockResolvedValue({
      data: { items: [{ id: "r1", revision_no: 1, is_head: true }] },
    });
    const revisions = await listPersonalResourceRevisions("memory");
    expect(revisions).toHaveLength(1);
    expect(revisions[0].isHead).toBe(true);
  });

  it("normalizes a nested revisionSummary payload for a single revision", async () => {
    mockedGet.mockResolvedValue({
      data: { revision_summary: { revision_id: "r1", revision_no: 2 }, content: "body" },
    });
    const result = await getPersonalResourceRevision("memory", "r1");
    expect(result.revision.revisionId).toBe("r1");
    expect(result.content).toBe("body");
  });
});

describe("rollbackPersonalResource", () => {
  it("returns the rollback result on success", async () => {
    mockedPost.mockResolvedValue({
      data: { revision_id: "rev-1", revision_no: 5, content: "restored" },
    });
    const result = await rollbackPersonalResource("memory", { revisionId: "rev-1" });
    expect(result).toEqual({ revisionId: "rev-1", revisionNo: 5, content: "restored" });
  });

  it("throws a RollbackConflictError on HTTP 409", async () => {
    mockedPost.mockRejectedValue({ response: { status: 409 } });
    await expect(
      rollbackPersonalResource("memory", { revisionId: "rev-1" }),
    ).rejects.toBeInstanceOf(RollbackConflictError);
  });

  it("rethrows other errors unchanged", async () => {
    const error = { response: { status: 500 } };
    mockedPost.mockRejectedValue(error);
    await expect(rollbackPersonalResource("memory", { revisionId: "rev-1" })).rejects.toBe(error);
  });
});
