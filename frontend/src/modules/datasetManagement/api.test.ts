import { describe, expect, it, vi, beforeEach } from "vitest";

const apiMocks = vi.hoisted(() => ({
  evalSetsGet: vi.fn(),
  evalSetsPost: vi.fn(),
  evalSetsPatch: vi.fn(),
  evalSetsDelete: vi.fn(),
  evalSetGet: vi.fn(),
  itemsGet: vi.fn(),
  itemsPost: vi.fn(),
  itemsPatch: vi.fn(),
  itemsDelete: vi.fn(),
  itemsBatchDelete: vi.fn(),
  questionTypesGet: vi.fn(),
  datasetQuestionTypesGet: vi.fn(),
  documentsGet: vi.fn(),
  listByDatasetsPost: vi.fn(),
  importsPreviewPost: vi.fn(),
  importsAppendPost: vi.fn(),
  importTaskGet: vi.fn(),
}));

vi.mock("@/components/request", () => ({
  axiosInstance: {},
  BASE_URL: "",
  localizeErrorCode: (code?: string, fallback = "") => fallback || code || "",
}));

vi.mock("@/i18n", () => ({
  default: { t: (key: string) => key },
}));

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: {
    getUserInfo: vi.fn(() => ({ groupId: "group-1" })),
  },
}));

vi.mock("@/modules/knowledge/utils/request", () => ({
  KnowledgeBaseServiceApi: () => ({
    datasetServiceListDatasets: vi.fn(),
  }),
}));

vi.mock("@/api/generated/core-client", () => ({
  Configuration: class {},
  DocumentsApiFactory: () => ({
    apiCoreDatasetsDatasetDocumentsGet: apiMocks.documentsGet,
    apiCoreDocumentsListByDatasetsPost: apiMocks.listByDatasetsPost,
  }),
  EvalSetsApiFactory: () => ({
    apiCoreEvalSetsGet: apiMocks.evalSetsGet,
    apiCoreEvalSetsPost: apiMocks.evalSetsPost,
    apiCoreEvalSetsEvalSetIdPatch: apiMocks.evalSetsPatch,
    apiCoreEvalSetsEvalSetIdDelete: apiMocks.evalSetsDelete,
    apiCoreEvalSetsEvalSetIdGet: apiMocks.evalSetGet,
    apiCoreEvalSetsQuestionTypesGet: apiMocks.questionTypesGet,
  }),
  EvalSetItemsApiFactory: () => ({
    apiCoreEvalSetsEvalSetIdItemsGet: apiMocks.itemsGet,
    apiCoreEvalSetsEvalSetIdItemsPost: apiMocks.itemsPost,
    apiCoreEvalSetsEvalSetIdItemsItemIdPatch: apiMocks.itemsPatch,
    apiCoreEvalSetsEvalSetIdItemsItemIdDelete: apiMocks.itemsDelete,
    apiCoreEvalSetsEvalSetIdItemsBatchDeletePost: apiMocks.itemsBatchDelete,
    apiCoreEvalSetsEvalSetIdQuestionTypesGet: apiMocks.datasetQuestionTypesGet,
  }),
  EvalSetImportsApiFactory: () => ({
    apiCoreEvalSetsImportsPreviewPost: apiMocks.importsPreviewPost,
    apiCoreEvalSetsEvalSetIdImportsPost: apiMocks.importsAppendPost,
    apiCoreEvalSetImportTasksTaskIdGet: apiMocks.importTaskGet,
  }),
}));

import {
  createDataset,
  deleteDataset,
  findKnowledgeBaseDocumentById,
  getDataset,
  listDatasetItems,
  listDatasetQuestionTypes,
  listDatasets,
  listQuestionTypes,
  mergeKnowledgeDocumentOptions,
  searchKnowledgeBaseDocuments,
  updateDataset,
} from "./api";

beforeEach(() => {
  Object.values(apiMocks).forEach((mockFn) => mockFn.mockReset());
});

describe("mergeKnowledgeDocumentOptions", () => {
  it("dedupes by name, keeping the first occurrence", () => {
    const current = [{ documentId: "d1", name: "报告" }];
    const next = [
      { documentId: "d2", name: "报告" },
      { documentId: "d3", name: "手册" },
    ];
    const merged = mergeKnowledgeDocumentOptions(current, next);
    expect(merged).toEqual([
      { documentId: "d1", name: "报告" },
      { documentId: "d3", name: "手册" },
    ]);
  });

  it("drops options without a usable dedupe key", () => {
    const merged = mergeKnowledgeDocumentOptions([], [{ documentId: "", name: "" }]);
    expect(merged).toEqual([]);
  });
});

describe("findKnowledgeBaseDocumentById", () => {
  it("returns null when no knowledge base ids or document id are provided", async () => {
    expect(await findKnowledgeBaseDocumentById([], "doc1")).toBeNull();
    expect(await findKnowledgeBaseDocumentById(["kb1"], "")).toBeNull();
  });

  it("returns the matched document when found on the first page", async () => {
    apiMocks.documentsGet.mockResolvedValue({
      data: { documents: [{ document_id: "doc1", display_name: "报告" }] },
    });
    const result = await findKnowledgeBaseDocumentById(["kb1"], "doc1");
    expect(result).toEqual({ documentId: "doc1", datasetId: "kb1", name: "报告" });
    expect(apiMocks.documentsGet).toHaveBeenCalledTimes(1);
  });

  it("paginates via next_page_token until a match or no more pages", async () => {
    apiMocks.documentsGet
      .mockResolvedValueOnce({
        data: { documents: [{ document_id: "other" }], next_page_token: "p2" },
      })
      .mockResolvedValueOnce({
        data: { documents: [{ document_id: "doc1", name: "文档A" }] },
      });
    const result = await findKnowledgeBaseDocumentById(["kb1"], "doc1");
    expect(result).toEqual({ documentId: "doc1", datasetId: "kb1", name: "文档A" });
    expect(apiMocks.documentsGet).toHaveBeenCalledTimes(2);
  });

  it("returns null when the document is not found in any knowledge base", async () => {
    apiMocks.documentsGet.mockResolvedValue({ data: { documents: [] } });
    const result = await findKnowledgeBaseDocumentById(["kb1", "kb2"], "missing");
    expect(result).toBeNull();
    expect(apiMocks.documentsGet).toHaveBeenCalledTimes(2);
  });
});

describe("searchKnowledgeBaseDocuments", () => {
  it("returns an empty result set when no knowledge base ids are given", async () => {
    const result = await searchKnowledgeBaseDocuments([], "kw");
    expect(result).toEqual({ options: [] });
    expect(apiMocks.listByDatasetsPost).not.toHaveBeenCalled();
  });

  it("maps documents into options and forwards pagination metadata", async () => {
    apiMocks.listByDatasetsPost.mockResolvedValue({
      data: {
        documents: [{ document_id: "doc1", dataset_id: "kb1", display_name: "报告" }],
        next_page_token: "token-2",
        total_size: 5,
      },
    });
    const result = await searchKnowledgeBaseDocuments(["kb1"], "报告");
    expect(result.options).toEqual([{ documentId: "doc1", datasetId: "kb1", name: "报告" }]);
    expect(result.nextPageToken).toBe("token-2");
    expect(result.totalSize).toBe(5);
  });
});

describe("listQuestionTypes / listDatasetQuestionTypes", () => {
  it("maps question type items to plain string values", async () => {
    apiMocks.questionTypesGet.mockResolvedValue({
      data: { items: [{ value: "事实问答" }, { label: "总结问答" }] },
    });
    expect(await listQuestionTypes()).toEqual(["事实问答", "总结问答"]);
  });

  it("returns an empty array for listDatasetQuestionTypes when datasetId is blank", async () => {
    expect(await listDatasetQuestionTypes("")).toEqual([]);
    expect(apiMocks.datasetQuestionTypesGet).not.toHaveBeenCalled();
  });

  it("fetches dataset-scoped question types when datasetId is provided", async () => {
    apiMocks.datasetQuestionTypesGet.mockResolvedValue({
      data: { items: [{ value: "排障问答" }] },
    });
    expect(await listDatasetQuestionTypes("dataset-1")).toEqual(["排障问答"]);
  });
});

describe("listDatasets / getDataset", () => {
  it("maps eval set list items to DatasetListItem shape", async () => {
    apiMocks.evalSetsGet.mockResolvedValue({
      data: {
        items: [
          {
            id: "ds1",
            name: "数据集A",
            description: "desc",
            created_by: "u1",
            created_by_name: "王博超",
            group_id: "g1",
            created_at: "2026-01-01",
            updated_at: "2026-01-02",
            dataset_ids: ["kb1"],
            dataset_names: ["知识库A"],
            item_count: 10,
          },
        ],
      },
    });
    const result = await listDatasets("kw");
    expect(result).toEqual([
      {
        id: "ds1",
        name: "数据集A",
        description: "desc",
        owner_id: "u1",
        owner_name: "王博超",
        group_id: "g1",
        created_at: "2026-01-01",
        updated_at: "2026-01-02",
        knowledge_bases: [{ id: "kb1", name: "知识库A" }],
        sample_count: 10,
      },
    ]);
  });

  it("gets a single dataset by id", async () => {
    apiMocks.evalSetGet.mockResolvedValue({
      data: {
        id: "ds1",
        name: "数据集A",
        created_by: "u1",
        group_id: "g1",
        created_at: "2026-01-01",
        updated_at: "2026-01-02",
      },
    });
    const result = await getDataset("ds1");
    expect(result.id).toBe("ds1");
    expect(apiMocks.evalSetGet).toHaveBeenCalledWith({ evalSetId: "ds1" });
  });
});

describe("createDataset / updateDataset / deleteDataset", () => {
  it("throws when creating without any knowledge base ids", async () => {
    await expect(createDataset({ name: "test" })).rejects.toThrow();
    expect(apiMocks.evalSetsPost).not.toHaveBeenCalled();
  });

  it("creates a dataset with deduped knowledge base ids and resolved group id", async () => {
    apiMocks.evalSetsPost.mockResolvedValue({
      data: {
        id: "ds1",
        name: "test",
        created_by: "u1",
        group_id: "group-1",
        created_at: "2026-01-01",
        updated_at: "2026-01-01",
      },
    });
    await createDataset({ name: "test", knowledge_base_ids: ["kb1", "kb1", "kb2"] });
    expect(apiMocks.evalSetsPost).toHaveBeenCalledWith({
      createEvalSetRequest: {
        name: "test",
        description: "",
        dataset_ids: ["kb1", "kb2"],
        group_id: "group-1",
      },
    });
  });

  it("updates a dataset, preserving current knowledge bases when none are supplied", async () => {
    apiMocks.evalSetGet.mockResolvedValue({
      data: {
        id: "ds1",
        name: "old",
        created_by: "u1",
        group_id: "g1",
        created_at: "2026-01-01",
        updated_at: "2026-01-01",
        dataset_ids: ["kb1"],
        dataset_names: ["知识库A"],
      },
    });
    apiMocks.evalSetsPatch.mockResolvedValue({
      data: {
        id: "ds1",
        name: "new",
        created_by: "u1",
        group_id: "g1",
        created_at: "2026-01-01",
        updated_at: "2026-01-02",
      },
    });
    await updateDataset("ds1", { name: "new" });
    expect(apiMocks.evalSetsPatch).toHaveBeenCalledWith({
      evalSetId: "ds1",
      updateEvalSetRequest: {
        name: "new",
        description: "",
        dataset_ids: ["kb1"],
        group_id: "g1",
      },
    });
  });

  it("clears knowledge base associations when an empty array is explicitly submitted", async () => {
    apiMocks.evalSetGet.mockResolvedValue({
      data: {
        id: "ds1",
        name: "old",
        created_by: "u1",
        group_id: "g1",
        created_at: "2026-01-01",
        updated_at: "2026-01-01",
        dataset_ids: ["kb1"],
        dataset_names: ["知识库A"],
      },
    });
    apiMocks.evalSetsPatch.mockResolvedValue({ data: { id: "ds1", name: "new", created_by: "u1", group_id: "g1", created_at: "x", updated_at: "y" } });
    await updateDataset("ds1", { name: "new", knowledge_base_ids: [] });
    expect(apiMocks.evalSetsPatch).toHaveBeenCalledWith({
      evalSetId: "ds1",
      updateEvalSetRequest: {
        name: "new",
        description: "",
        dataset_ids: [],
        group_id: "g1",
      },
    });
  });

  it("deletes a dataset by id", async () => {
    apiMocks.evalSetsDelete.mockResolvedValue({});
    await deleteDataset("ds1");
    expect(apiMocks.evalSetsDelete).toHaveBeenCalledWith({ evalSetId: "ds1" });
  });
});

describe("listDatasetItems", () => {
  it("maps eval set items to DatasetItem shape and normalizes list fields", async () => {
    apiMocks.itemsGet.mockResolvedValue({
      data: {
        items: [
          {
            id: "item1",
            eval_set_id: "ds1",
            question: "q1",
            question_type: "t1",
            ground_truth: "gt1",
            reference_doc_ids: "doc1, doc2",
            reference_chunk_ids: "chunk1",
            source: "upload",
            created_at: "2026-01-01",
            updated_at: "2026-01-01",
            created_by: "u1",
            created_by_name: "王博超",
          },
        ],
        total: 1,
      },
    });
    const result = await listDatasetItems("ds1", { keyword: "q" });
    expect(result.total).toBe(1);
    expect(result.items[0]).toMatchObject({
      id: "item1",
      dataset_id: "ds1",
      reference_doc_ids: ["doc1", "doc2"],
      reference_chunk_ids: ["chunk1"],
      source: "upload",
      created_by: "王博超",
    });
  });

  it("falls back to manual source when the backend returns an unrecognized source", async () => {
    apiMocks.itemsGet.mockResolvedValue({
      data: {
        items: [
          {
            id: "item1",
            eval_set_id: "ds1",
            question: "q1",
            question_type: "t1",
            ground_truth: "gt1",
            source: "weird_source",
            created_at: "2026-01-01",
            updated_at: "2026-01-01",
            created_by: "u1",
          },
        ],
        total: 1,
      },
    });
    const result = await listDatasetItems("ds1");
    expect(result.items[0].source).toBe("manual");
  });
});
