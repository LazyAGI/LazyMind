import { beforeEach, describe, expect, it, vi } from "vitest";
import { axiosInstance } from "@/components/request";
import {
  addGlossaryConflictToGroups,
  batchRemoveGlossaryAssets,
  checkGlossaryWordsExist,
  createGlossaryAsset,
  createGlossaryGroupFromConflict,
  getGlossaryAssetDetail,
  listGlossaryAssets,
  listGlossaryAssetsPage,
  listGlossaryConflicts,
  mergeGlossaryAssets,
  mergeGlossaryAssetsAndAddConflictWord,
  mergeGlossaryConflictAndAddWord,
  normalizeGlossaryAsset,
  removeGlossaryAsset,
  removeGlossaryConflict,
  updateGlossaryAsset,
} from "./glossaryApi";

vi.mock("@/components/request", () => ({
  axiosInstance: {
    get: vi.fn(),
    post: vi.fn(),
    delete: vi.fn(),
  },
  BASE_URL: "",
}));

const mockedGet = axiosInstance.get as unknown as ReturnType<typeof vi.fn>;
const mockedPost = axiosInstance.post as unknown as ReturnType<typeof vi.fn>;
const mockedDelete = axiosInstance.delete as unknown as ReturnType<typeof vi.fn>;

beforeEach(() => {
  mockedGet.mockReset();
  mockedPost.mockReset();
  mockedDelete.mockReset();
});

describe("normalizeGlossaryAsset", () => {
  it("normalizes a raw backend record with snake_case fields", () => {
    const asset = normalizeGlossaryAsset({
      group_id: "g1",
      term: "雨强阈值",
      category: "降雨监测",
      aliases: ["降雨阈值"],
      source: "ai_generated",
      description: "desc",
      lock: true,
    });

    expect(asset).toEqual({
      id: "g1",
      term: "雨强阈值",
      group: "降雨监测",
      aliases: ["降雨阈值"],
      source: "ai",
      content: "desc",
      protect: true,
    });
  });

  it("falls back id to term when group_id is missing", () => {
    const asset = normalizeGlossaryAsset({ term: "术语" });
    expect(asset?.id).toBe("术语");
    expect(asset?.source).toBe("user");
  });

  it("returns null when neither id nor term is present", () => {
    expect(normalizeGlossaryAsset({})).toBeNull();
    expect(normalizeGlossaryAsset(null)).toBeNull();
  });
});

describe("listGlossaryAssetsPage", () => {
  it("uses the search endpoint when a keyword is given and extracts items/total", async () => {
    mockedPost.mockResolvedValue({
      data: {
        items: [{ group_id: "g1", term: "t1" }],
        total_size: 5,
        next_page_token: "tok",
      },
    });

    const result = await listGlossaryAssetsPage({ keyword: "关键词" });

    expect(mockedPost).toHaveBeenCalledWith(
      expect.stringContaining("word_group:search"),
      expect.objectContaining({ keyword: "关键词" }),
    );
    expect(result.records).toHaveLength(1);
    expect(result.total).toBe(5);
    expect(result.nextPageToken).toBe("tok");
  });

  it("uses the list endpoint when there is no keyword/source", async () => {
    mockedGet.mockResolvedValue({ data: { word_groups: [{ group_id: "g1", term: "t1" }] } });

    const result = await listGlossaryAssetsPage();

    expect(mockedGet).toHaveBeenCalledWith(expect.stringContaining("word_group"), expect.any(Object));
    expect(result.records).toHaveLength(1);
    expect(result.total).toBe(1);
  });

  it("listGlossaryAssets returns only the records array", async () => {
    mockedGet.mockResolvedValue({ data: { list: [{ group_id: "g1", term: "t1" }] } });
    const records = await listGlossaryAssets();
    expect(records).toHaveLength(1);
  });
});

describe("getGlossaryAssetDetail", () => {
  it("fetches a single glossary asset by id", async () => {
    mockedGet.mockResolvedValue({ data: { group_id: "g1", term: "t1" } });
    const asset = await getGlossaryAssetDetail("g1");
    expect(mockedGet).toHaveBeenCalledWith(expect.stringContaining("word_group/g1"));
    expect(asset?.id).toBe("g1");
  });
});

describe("createGlossaryAsset / updateGlossaryAsset", () => {
  const item = { id: "g1", term: "term", group: "", aliases: ["a"], source: "user" as const, content: "c", protect: true };

  it("posts create payload with mapped fields", async () => {
    mockedPost.mockResolvedValue({ data: { group_id: "g1", term: "term" } });
    await createGlossaryAsset(item);
    expect(mockedPost).toHaveBeenCalledWith(
      expect.stringContaining("word_group"),
      expect.objectContaining({ term: "term", aliases: ["a"], description: "c", lock: true }),
    );
  });

  it("includes conflictId when provided", async () => {
    mockedPost.mockResolvedValue({ data: {} });
    await createGlossaryAsset(item, { conflictId: "conflict-1" });
    expect(mockedPost).toHaveBeenCalledWith(
      expect.any(String),
      expect.objectContaining({ id: "conflict-1", conflict: true }),
    );
  });

  it("posts update payload referencing group_id", async () => {
    mockedPost.mockResolvedValue({ data: { group_id: "g1", term: "term" } });
    await updateGlossaryAsset(item);
    expect(mockedPost).toHaveBeenCalledWith(
      expect.stringContaining("word_group:update"),
      expect.objectContaining({ group_id: "g1" }),
    );
  });
});

describe("removeGlossaryAsset / batchRemoveGlossaryAssets", () => {
  it("deletes a single glossary asset by id", async () => {
    mockedDelete.mockResolvedValue({});
    await removeGlossaryAsset("g1");
    expect(mockedDelete).toHaveBeenCalledWith(expect.stringContaining("word_group/g1"));
  });

  it("batch deletes by group ids", async () => {
    mockedPost.mockResolvedValue({});
    await batchRemoveGlossaryAssets(["g1", "g2"]);
    expect(mockedPost).toHaveBeenCalledWith(
      expect.stringContaining("word_group:batchDelete"),
      { group_ids: ["g1", "g2"] },
    );
  });
});

describe("mergeGlossaryAssets / mergeGlossaryAssetsAndAddConflictWord", () => {
  it("trims term and description when merging groups", async () => {
    mockedPost.mockResolvedValue({ data: { group_id: "g1", term: "merged" } });
    await mergeGlossaryAssets({ group_ids: ["g1", "g2"], term: " merged ", description: " d " });
    expect(mockedPost).toHaveBeenCalledWith(
      expect.stringContaining("word_group:merge"),
      expect.objectContaining({ term: "merged", description: "d" }),
    );
  });

  it("merges and adds a conflict word", async () => {
    mockedPost.mockResolvedValue({ data: {} });
    await mergeGlossaryAssetsAndAddConflictWord({ id: "c1", word: "w", groupIds: ["g1"] });
    expect(mockedPost).toHaveBeenCalledWith(
      expect.stringContaining("word_group:mergeAndAddWord"),
      { id: "c1", word: "w", group_ids: ["g1"] },
    );
  });
});

describe("checkGlossaryWordsExist", () => {
  it("returns the existing words list", async () => {
    mockedPost.mockResolvedValue({ data: { existing: ["term1", "term2"] } });
    const result = await checkGlossaryWordsExist("term1", ["alias"]);
    expect(result.existing).toEqual(["term1", "term2"]);
  });
});

describe("listGlossaryConflicts / removeGlossaryConflict", () => {
  it("lists conflicts with pagination params", async () => {
    mockedGet.mockResolvedValue({
      data: { items: [{ id: "c1", word: "w1" }] },
    });
    const conflicts = await listGlossaryConflicts({ pageSize: 50 });
    expect(mockedGet).toHaveBeenCalledWith(
      expect.stringContaining("word_group_conflict"),
      expect.objectContaining({ params: { page_size: 50, page_token: "" } }),
    );
    expect(conflicts).toHaveLength(1);
  });

  it("removes a conflict by id", async () => {
    mockedDelete.mockResolvedValue({});
    await removeGlossaryConflict("c1");
    expect(mockedDelete).toHaveBeenCalledWith(expect.stringContaining("word_group_conflict/c1"));
  });
});

describe("addGlossaryConflictToGroups / mergeGlossaryConflictAndAddWord / createGlossaryGroupFromConflict", () => {
  it("posts addToGroup payload", async () => {
    mockedPost.mockResolvedValue({});
    await addGlossaryConflictToGroups({ id: "c1", word: "w", groupIds: ["g1"] });
    expect(mockedPost).toHaveBeenCalledWith(
      expect.stringContaining("word_group_conflict:addToGroup"),
      { id: "c1", word: "w", group_ids: ["g1"] },
    );
  });

  it("posts mergeAndAddWord payload and unwraps items", async () => {
    mockedPost.mockResolvedValue({ data: { items: [{ group_id: "g1", term: "t1" }] } });
    const result = await mergeGlossaryConflictAndAddWord({ id: "c1", word: "w" });
    expect(mockedPost).toHaveBeenCalledWith(
      expect.stringContaining("word_group_conflict:mergeAndAddWord"),
      { id: "c1", word: "w" },
    );
    expect(result).toHaveLength(1);
  });

  it("posts createGroup payload without expecting a return value", async () => {
    mockedPost.mockResolvedValue({});
    await createGlossaryGroupFromConflict({
      id: "c1",
      word: "w",
      term: "t",
      description: "d",
    });
    expect(mockedPost).toHaveBeenCalledWith(
      expect.stringContaining("word_group_conflict:createGroup"),
      expect.objectContaining({ id: "c1", word: "w" }),
    );
  });
});
