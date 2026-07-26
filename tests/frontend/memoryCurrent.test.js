import { beforeEach, describe, expect, it, vi } from "vitest";

const currentMemoryClientMocks = vi.hoisted(() => ({
  apiCoreMemoryProfileGet: vi.fn(),
  apiCoreMemoryProfilePatch: vi.fn(),
  apiCoreMemoryPreferencesGet: vi.fn(),
  apiCoreMemoryPreferencesOrderPut: vi.fn(),
  apiCoreMemoryPreferencesNameDelete: vi.fn(),
  apiCoreMemoryPreferencesNameGet: vi.fn(),
  apiCoreMemorySoulGet: vi.fn(),
  apiCoreMemorySoulPatch: vi.fn(),
}));

vi.mock("@/api/generated/core-client", () => ({
  Configuration: class Configuration {},
  DefaultApiFactory: () => currentMemoryClientMocks,
}));

vi.mock("@/components/request", () => ({
  axiosInstance: {},
  BASE_URL: "/gateway",
}));

import {
  deletePreferenceMemory,
  getPreferenceMemory,
  getProfileMemory,
  listPreferenceMemories,
  getSoulMemory,
  patchProfileMemory,
  patchSoulMemory,
  reorderPreferenceMemories,
} from "../../frontend/src/modules/memory/currentMemoryApi.ts";
import {
  getPreferenceResidentUsageTone,
  isCurrentMemoryConflict,
  isCurrentMemoryResourceNotFound,
  isPreferenceResident,
  mergePreferenceOrderWithLatest,
  movePreferenceItem,
} from "../../frontend/src/modules/memory/currentMemoryViewModel.ts";

beforeEach(() => {
  Object.values(currentMemoryClientMocks).forEach((mock) => mock.mockReset());
});

describe("Preference Core interface", () => {
  it("normalizes the ordered item index and keeps its ETag", async () => {
    currentMemoryClientMocks.apiCoreMemoryPreferencesGet.mockResolvedValue({
      data: {
        data: {
          items: [
            {
              name: "response.language",
              summary: "默认使用中文回复",
              created_at: "2026-07-20T08:00:00Z",
              updated_at: "2026-07-24T08:00:00Z",
            },
          ],
          etag: "preferences-v7",
          total_size: 1,
          resident_index_usage: {
            used_items: 1,
            max_items: 100,
            over_limit: false,
          },
          updated_at: 1784880000000,
        },
      },
    });

    await expect(listPreferenceMemories()).resolves.toEqual({
      items: [
        {
          name: "response.language",
          summary: "默认使用中文回复",
          createdAt: "2026-07-20T08:00:00Z",
          updatedAt: "2026-07-24T08:00:00Z",
        },
      ],
      etag: "preferences-v7",
      totalSize: 1,
      residentIndexUsage: {
        usedItems: 1,
        maxItems: 100,
        overLimit: false,
      },
      updatedAt: 1784880000000,
    });
    expect(
      currentMemoryClientMocks.apiCoreMemoryPreferencesGet,
    ).toHaveBeenCalledWith();
  });

  it("keeps compatibility with a list response that has no usage metadata", async () => {
    currentMemoryClientMocks.apiCoreMemoryPreferencesGet.mockResolvedValue({
      data: {
        data: {
          items: [],
          etag: "preferences-v6",
          total_size: 0,
          updated_at: 1784880000000,
        },
      },
    });

    await expect(listPreferenceMemories()).resolves.toEqual({
      items: [],
      etag: "preferences-v6",
      totalSize: 0,
      updatedAt: 1784880000000,
    });
  });

  it("loads the selected item with its structured Reference detail", async () => {
    currentMemoryClientMocks.apiCoreMemoryPreferencesNameGet.mockResolvedValue(
      {
        data: {
          data: {
            item: {
              name: "response.language",
              summary: "默认使用中文回复",
              created_at: "2026-07-20T08:00:00Z",
              updated_at: "2026-07-24T08:00:00Z",
            },
            reference_status: "available",
            reference: {
              name: "response.language",
              summary: "默认使用中文回复",
              created_at: "2026-07-20T08:00:00Z",
              updated_at: "2026-07-24T08:00:00Z",
              source: {
                kind: "chat_explicit",
                conversation_id: "conversation-1",
              },
              application_scenarios: "## 技术讨论",
              preference_details: "**默认**使用中文",
              reason: "用户明确要求",
            },
          },
        },
      },
    );

    await expect(
      getPreferenceMemory("response.language"),
    ).resolves.toMatchObject({
      item: { name: "response.language" },
      referenceStatus: "available",
      reference: {
        source: {
          kind: "chat_explicit",
          conversationId: "conversation-1",
        },
        preferenceDetails: "**默认**使用中文",
      },
    });
    expect(
      currentMemoryClientMocks.apiCoreMemoryPreferencesNameGet,
    ).toHaveBeenCalledWith({ name: "response.language" });
  });

  it("keeps a missing Reference as a detail state instead of a request failure", async () => {
    currentMemoryClientMocks.apiCoreMemoryPreferencesNameGet.mockResolvedValue({
      data: {
        data: {
          item: {
            name: "response.language",
            summary: "默认使用中文回复",
            created_at: "2026-07-20T08:00:00Z",
            updated_at: "2026-07-24T08:00:00Z",
          },
          reference_status: "missing",
          reference: null,
        },
      },
    });

    await expect(
      getPreferenceMemory("response.language"),
    ).resolves.toMatchObject({
      item: { name: "response.language" },
      referenceStatus: "missing",
      reference: null,
    });
  });

  it("reorders with the current ETag, deletes by name, and preserves Core errors", async () => {
    currentMemoryClientMocks.apiCoreMemoryPreferencesOrderPut.mockResolvedValue({
      data: {
        data: {
          items: [
            {
              name: "second",
              summary: "Second",
              created_at: "2026-07-20T08:00:00Z",
              updated_at: "2026-07-24T08:00:00Z",
            },
            {
              name: "first",
              summary: "First",
              created_at: "2026-07-20T08:00:00Z",
              updated_at: "2026-07-24T08:00:00Z",
            },
          ],
          etag: "preferences-v8",
          total_size: 2,
          updated_at: 1784880120000,
        },
      },
    });
    currentMemoryClientMocks.apiCoreMemoryPreferencesNameDelete.mockResolvedValue(
      { data: undefined },
    );

    await expect(
      reorderPreferenceMemories(["second", "first"], "preferences-v7"),
    ).resolves.toMatchObject({
      etag: "preferences-v8",
      items: [{ name: "second" }, { name: "first" }],
    });
    expect(
      currentMemoryClientMocks.apiCoreMemoryPreferencesOrderPut,
    ).toHaveBeenCalledWith({
      currentMemoryPreferenceOrderRequest: {
        ordered_names: ["second", "first"],
        expected_etag: "preferences-v7",
      },
    });

    await deletePreferenceMemory("second");
    expect(
      currentMemoryClientMocks.apiCoreMemoryPreferencesNameDelete,
    ).toHaveBeenCalledWith({ name: "second" });

    const conflict = Object.assign(new Error("preference etag conflict"), {
      response: { status: 409 },
    });
    currentMemoryClientMocks.apiCoreMemoryPreferencesOrderPut.mockRejectedValue(
      conflict,
    );
    await expect(
      reorderPreferenceMemories(["first", "second"], "preferences-v7"),
    ).rejects.toBe(conflict);
  });
});

describe("Preference reorder behavior", () => {
  const item = (name) => ({
    name,
    summary: name,
    createdAt: "2026-07-20T08:00:00Z",
    updatedAt: "2026-07-24T08:00:00Z",
  });

  it("moves one item without mutating the source list", () => {
    const source = [item("first"), item("second"), item("third")];
    const moved = movePreferenceItem(source, "first", "third");

    expect(moved.map((entry) => entry.name)).toEqual([
      "second",
      "third",
      "first",
    ]);
    expect(source.map((entry) => entry.name)).toEqual([
      "first",
      "second",
      "third",
    ]);
  });

  it("rebases a retained local order onto the latest server item set", () => {
    const rebased = mergePreferenceOrderWithLatest(
      [item("second"), item("first"), item("deleted")],
      [item("first"), item("second"), item("new")],
    );

    expect(rebased.map((entry) => entry.name)).toEqual([
      "second",
      "first",
      "new",
    ]);
  });

  it("recognizes only HTTP 409 as a reorder conflict", () => {
    expect(
      isCurrentMemoryConflict({ response: { status: 409 } }),
    ).toBe(true);
    expect(
      isCurrentMemoryConflict({ response: { status: 500 } }),
    ).toBe(false);
    expect(isCurrentMemoryConflict(new Error("conflict"))).toBe(false);
  });

  it("treats only the Core resource response as empty, not a missing route", () => {
    expect(
      isCurrentMemoryResourceNotFound({
        response: {
          status: 404,
          data: { message: "current memory resource not found" },
        },
      }),
    ).toBe(true);
    expect(
      isCurrentMemoryResourceNotFound({
        response: { status: 404, data: "404 page not found" },
      }),
    ).toBe(false);
    expect(
      isCurrentMemoryResourceNotFound({
        response: {
          status: 500,
          data: { message: "current memory resource not found" },
        },
      }),
    ).toBe(false);
  });
});

describe("Preference resident usage behavior", () => {
  it.each([
    [0, 100, false, "normal"],
    [79, 100, false, "normal"],
    [80, 100, false, "warning"],
    [100, 100, false, "error"],
    [120, 100, true, "error"],
  ])(
    "maps %d / %d overLimit=%s to %s",
    (usedItems, maxItems, overLimit, expected) => {
      expect(
        getPreferenceResidentUsageTone(usedItems, maxItems, overLimit),
      ).toBe(expected);
    },
  );

  it("marks only entries before the configured limit as resident", () => {
    expect(isPreferenceResident(99, 100)).toBe(true);
    expect(isPreferenceResident(100, 100)).toBe(false);
    expect(isPreferenceResident(100, undefined)).toBe(true);
  });
});

describe("Soul and Profile Core interface", () => {
  it("normalizes the current typed documents returned by the generated client", async () => {
    currentMemoryClientMocks.apiCoreMemorySoulGet.mockResolvedValue({
      data: {
        data: {
          document: {
            identity: {
              name: "LazyMind",
              role: "personal_ai_assistant",
              description: "Personal collaborator",
            },
            mission: {
              primary_goal: "Help the user",
              success_definition: "Useful outcomes",
            },
            interaction: {
              relationship_mode: "collaborator",
              default_tone: "warm_direct",
              initiative_level: "proactive",
              challenge_level: "constructive",
              decision_mode: "recommend_then_confirm",
            },
            epistemic: {
              uncertainty_style: "explicit",
              verification_mode: "when_material",
            },
          },
          updated_at: 1784880000000,
        },
      },
    });
    currentMemoryClientMocks.apiCoreMemoryProfileGet.mockResolvedValue({
      data: {
        data: {
          document: {
            identity: {
              preferred_name: null,
              aliases: ["面壁者"],
              pronouns: null,
            },
            locale: {
              languages: ["zh-CN"],
              timezone: "Asia/Shanghai",
              region: "CN",
            },
            professional: {
              roles: ["Agent Engineer"],
              organization: null,
              industry: "AI",
              expertise_domains: ["Agent Memory"],
            },
            accessibility: {
              communication_needs: [],
            },
          },
          updated_at: 1784880060000,
        },
      },
    });

    await expect(getSoulMemory()).resolves.toMatchObject({
      updatedAt: 1784880000000,
      document: {
        identity: { name: "LazyMind" },
        interaction: { initiative_level: "proactive" },
      },
    });
    await expect(getProfileMemory()).resolves.toMatchObject({
      updatedAt: 1784880060000,
      document: {
        identity: { preferred_name: null, aliases: ["面壁者"] },
        locale: { languages: ["zh-CN"] },
      },
    });
  });

  it("sends a single nested field in each explicit save", async () => {
    currentMemoryClientMocks.apiCoreMemorySoulPatch.mockResolvedValue({
      data: {
        data: {
          document: {
            identity: {
              name: "LazyMind",
              role: "personal_ai_assistant",
              description: "Updated",
            },
            mission: {
              primary_goal: "Help",
              success_definition: "Useful",
            },
            interaction: {
              relationship_mode: "collaborator",
              default_tone: "warm_direct",
              initiative_level: "proactive",
              challenge_level: "constructive",
              decision_mode: "recommend_then_confirm",
            },
            epistemic: {
              uncertainty_style: "explicit",
              verification_mode: "when_material",
            },
          },
          updated_at: 1784880120000,
        },
      },
    });
    currentMemoryClientMocks.apiCoreMemoryProfilePatch.mockResolvedValue({
      data: {
        data: {
          document: {
            identity: {
              preferred_name: null,
              aliases: [],
              pronouns: null,
            },
            locale: {
              languages: [],
              timezone: null,
              region: null,
            },
            professional: {
              roles: [],
              organization: null,
              industry: null,
              expertise_domains: [],
            },
            accessibility: {
              communication_needs: [],
            },
          },
          updated_at: 1784880180000,
        },
      },
    });

    await patchSoulMemory({
      interaction: { initiative_level: "reactive" },
    });
    await patchProfileMemory({
      identity: { preferred_name: null },
    });

    expect(
      currentMemoryClientMocks.apiCoreMemorySoulPatch,
    ).toHaveBeenCalledWith({
      currentMemoryOperationsRequest: {
        interaction: { initiative_level: "reactive" },
      },
    });
    expect(
      currentMemoryClientMocks.apiCoreMemoryProfilePatch,
    ).toHaveBeenCalledWith({
      currentMemoryOperationsRequest: {
        identity: { preferred_name: null },
      },
    });
  });
});
