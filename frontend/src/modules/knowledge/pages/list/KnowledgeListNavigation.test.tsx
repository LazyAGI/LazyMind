import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { deferred, desktopTestTranslation as t, installDesktopTestDOM } from "@/test/desktopResourceFixtures";

const mocks = vi.hoisted(() => ({
  list: vi.fn(), catalog: vi.fn(), scan: vi.fn(), cloudCreate: vi.fn(), scenes: vi.fn(), remove: vi.fn(), get: vi.fn(),
  post: vi.fn(), put: vi.fn(), patch: vi.fn(), delete: vi.fn(), features: vi.fn(), imageEmbedRequired: false, role: "user",
}));
vi.mock("@/modules/learning/api", () => ({ getLearningCatalog: mocks.scenes }));
vi.mock("react-i18next", async (load) => ({ ...await load<typeof import("react-i18next")>(), useTranslation: () => ({ t }) }));
vi.mock("@/modules/knowledge/api/knowledgeMarket", () => ({
  listKnowledgeMarket: mocks.catalog,
  listKnowledgeMarketDomains: vi.fn().mockResolvedValue({ domains: { industry: ["团队"], evaluation: [] } }),
  listKnowledgeMarketInstalls: vi.fn().mockResolvedValue({ items: [{ market_item_id: "official", dataset_id: "official-id", install_state: "done", installed_version: "1", active: false }] }),
  listKnowledgeMarketTasks: vi.fn().mockResolvedValue({ items: [] }),
  getKnowledgeMarketItem: vi.fn(), getKnowledgeMarketTask: vi.fn(),
  installKnowledgeMarketItem: vi.fn(), updateKnowledgeMarketItem: vi.fn(), updateAllKnowledgeMarketItems: vi.fn(),
}));
vi.mock("@/modules/knowledge/components/SyncKnowledgeBaseCreationFlow", () => ({
  default: () => null,
  useSyncKnowledgeBaseCreation: () => ({ cloudConnectionLoading: false, handleCreateFromCloudDocuments: mocks.cloudCreate }),
}));
vi.mock("@/modules/knowledge/components/UpdateModal", async () => ({ default: (await import("react")).forwardRef(() => null) }));
vi.mock("@/modules/knowledge/components/CreateKnowledgeBaseModal", async () => ({ default: (await import("react")).forwardRef(() => null) }));
vi.mock("@/modules/knowledge/components/KnowledgeTag", () => ({ default: () => null }));
vi.mock("@/modules/knowledge/components/KnowledgeBaseSyncNow", () => ({ default: () => null }));
vi.mock("@/components/auth", () => ({ AgentAppsAuth: { getUserInfo: () => ({ role: mocks.role }) } }));
vi.mock("@/components/request", () => ({ BASE_URL: "", localizeErrorCode: (_code: string, fallback: string) => fallback,
  axiosInstance: { get: mocks.get, post: mocks.post, put: mocks.put, patch: mocks.patch, delete: mocks.delete } }));
vi.mock("@/modules/knowledge/utils/request", () => ({ KnowledgeBaseServiceApi: () => ({ datasetServiceListDatasets: mocks.list, datasetServiceDeleteDataset: mocks.remove }) }));
vi.mock("@/modules/dataSource/api/clients", () => ({ dataSourceScanApi: { listSources: mocks.scan } }));
vi.mock("@/hooks/useModelFeatures", () => ({ fetchModelFeatures: mocks.features, isImageEmbedRequired: () => mocks.imageEmbedRequired, MODEL_FEATURES_CHANGED_EVENT: "features-changed" }));
vi.mock("@/runtime/mode", async (load) => ({ ...await load<object>(), isDesktopRuntime: () => false }));
vi.mock("@/components/ui", () => ({
  ListPageTable: (props: any) => <div className="list-page-table-scroll">
    {!props.loading && props.dataSource.length === 0 ? props.locale?.emptyText : null}
    {props.dataSource.map((row: any) => <div key={row.dataset_id || row.id} {...props.onRow?.(row)}>
      {props.columns[0].render(row[props.columns[0].dataIndex], row)}
      {props.columns.find((column: any) => column.key === "action")?.render(row)}
    </div>)}
    {props.pagination && <>
      <span>第 {props.pagination.current} 页</span>
      <button onClick={() => props.onChange({ current: 2, pageSize: props.pagination.pageSize })}>切到第二页</button>
      <button onClick={() => props.onChange({ current: 3, pageSize: 20 })}>每页二十条</button>
    </>}
  </div>,
}));
vi.mock("./KnowledgeLibraryActions", () => ({ default: ({ dataset, onDelete }: any) => <button onClick={(event) => { event.stopPropagation(); onDelete(); }}>delete-{dataset.dataset_id}</button> }));
import KnowledgePage from "./index";

const localResponse = { data: { datasets: [{ dataset_id: "local-id", display_name: "本地手册", tags: ["团队"] }], total_size: 55 } };
const manualCalls = () => mocks.list.mock.calls.filter(([params]) => params.pageSize !== 100);
const lastManualCall = () => manualCalls().slice(-1)[0];
const pageOffset = (params: { pageToken: string }) => params.pageToken ? JSON.parse(atob(params.pageToken)).Start : 0;

async function mount(search: string) {
  const router = createMemoryRouter([
    { path: "/lib/knowledge/list", element: <KnowledgePage /> },
    { path: "/lib/knowledge/detail/:id", element: <span>知识库详情</span> },
    { path: "/agent/chat/home", element: <span>聊天首页</span> },
    { path: "/settings", element: <span>默认模型设置</span> },
  ], { initialEntries: [`/lib/knowledge/list${search}`] });
  await act(async () => { render(<ConfigProvider button={{ autoInsertSpace: false }} theme={{ token: { motion: false } }}><RouterProvider router={router} /></ConfigProvider>); });
  return router;
}

beforeAll(installDesktopTestDOM);
beforeEach(() => {
  vi.clearAllMocks();
  mocks.role = "user";
  mocks.imageEmbedRequired = false;
  mocks.features.mockResolvedValue({});
  mocks.get.mockImplementation((url: string) => Promise.resolve({ data: url.endsWith("/deletion")
    ? { dataset_id: "local-id", display_name: "本地手册", status: "cleaning", total: 5, completed: 2, failed: 0 }
    : { ready: true } }));
  mocks.list.mockResolvedValue(localResponse);
  mocks.scenes.mockResolvedValue({ profiles: [
    { key: "general", name_i18n_key: "learning.profile.general.name" },
    { key: "academic_papers", name_i18n_key: "learning.profile.academicPapers.name" },
  ] });
  mocks.catalog.mockResolvedValue([{
    id: "official", name: "官方手册", category: "industry", domain: "团队", description: "官方说明",
    tags: ["团队"], icon: "", data_source: "官方", version: "1", online_access_url: "",
    created_at: "2026-09-01", updated_at: "2026-09-01",
  }]);
  mocks.scan.mockResolvedValue({ data: { items: [], total: 0 } });
});
afterEach(cleanup);

describe("knowledge list URL and data restoration", () => {
  const noticeName = t("knowledge.embeddingIndexStateTitle");
  const indexFixture = {
    model_name: "Qwen3-Embedding-8B", provider_name: "硅基流动",
    indexed_knowledge_bases: 3, pending_knowledge_bases: 2,
  };
  const configureEmbedding = (index: () => object, readiness: { ready: boolean; reason?: string }) => {
    mocks.get.mockImplementation(async (url: string) => ({ data: url.endsWith("/embedding/index-state")
      ? { data: index() } : { data: readiness } }));
  };
  const refreshEmbedding = () => act(async () => { window.dispatchEvent(new Event("features-changed")); });
  const dismissIndexNotice = async () => {
    const notice = await screen.findByRole("status", { name: noticeName });
    fireEvent.click(within(notice).getByRole("button", { name: t("common.close") }));
    expect(screen.queryByRole("status", { name: noticeName })).not.toBeInTheDocument();
  };
  const expectNoOnlineCleanup = () => {
    expect(mocks.post).not.toHaveBeenCalled();
    expect(mocks.put).not.toHaveBeenCalled();
    expect(mocks.patch).not.toHaveBeenCalled();
    expect(mocks.delete).not.toHaveBeenCalled();
    expect(mocks.get.mock.calls.map(([url]) => url)).toEqual(expect.arrayContaining([
      "/api/core/model_providers/embedding/index-state", "/api/core/model_providers/models/ready?model_type=embed_main",
    ]));
    expect(mocks.get.mock.calls.every(([url]) => !/cleanup|\/clear(?:[/?]|$)/.test(url))).toBe(true);
  };

  it.each([["empty", "empty"], ["indexed", "verified"]])("hides the healthy %s idle index banner", async (status, index_identity) => {
    configureEmbedding(() => ({ ...indexFixture, status, index_identity, operation_phase: "idle" }), { ready: true });

    await mount("");

    expect(screen.queryByRole("status", { name: noticeName })).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it.each([
    ["rebuilding", "verified", "rebuilding", undefined, "Rebuilding"],
    ["clearing", "verified", "clearing", "index_cleanup_in_progress", "Clearing"],
    ["clearing", "verified", "cleanup_failed", "index_cleanup_incomplete", "CleanupFailed"],
    ["unknown", "legacy_unknown", "idle", "index_identity_unverified", "Unknown"],
    ["cleared", "empty", "cleared", "index_cleared", "Cleared"],
  ])("renders one %s index notice for %s/%s when readiness is false", async (status, index_identity, operation_phase, reason, message) => {
    configureEmbedding(() => ({ ...indexFixture, status, index_identity, operation_phase }), { ready: false, reason });

    await mount("");

    const notice = await screen.findByRole("status", { name: noticeName });
    expect(notice).toHaveTextContent(t(`knowledge.embeddingIndex${message}`, { count: 2 }));
    expect(screen.getAllByRole("status", { name: noticeName })).toEqual([notice]);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.queryByText(t("knowledge.embeddingNotReadyBanner"))).not.toBeInTheDocument();
  });

  it("keeps the ordinary model-readiness warning for a healthy index with unavailable credentials", async () => {
    configureEmbedding(() => ({ ...indexFixture, status: "indexed", index_identity: "verified", operation_phase: "idle" }),
      { ready: false, reason: "credentials_unavailable" });

    await mount("");

    expect(screen.getByRole("alert")).toHaveTextContent(t("knowledge.embeddingNotReadyBanner"));
    expect(screen.queryByRole("status", { name: noticeName })).not.toBeInTheDocument();
  });

  it("reports an independent credential problem inside the rebuilding notice", async () => {
    configureEmbedding(() => ({ ...indexFixture, status: "rebuilding", index_identity: "verified", operation_phase: "rebuilding" }),
      { ready: false, reason: "credentials_unavailable" });

    await mount("");

    const notice = await screen.findByRole("status", { name: noticeName });
    expect(notice).toHaveTextContent(t("knowledge.embeddingIndexRebuilding", { count: 2 }));
    expect(notice).toHaveTextContent(t("knowledge.embeddingNotReadyBanner"));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("publishes a healthy index and text readiness as one snapshot when index finishes first", async () => {
    const nextIndex = deferred<{ data: { data: object } }>();
    const nextReadiness = deferred<{ data: { ready: boolean } }>();
    let indexCalls = 0;
    let readinessCalls = 0;
    mocks.get.mockImplementation((url: string) => {
      if (url.endsWith("/embedding/index-state")) {
        indexCalls += 1;
        return indexCalls === 1
          ? Promise.resolve({ data: { data: { ...indexFixture, status: "clearing", index_identity: "verified", operation_phase: "cleanup_failed" } } })
          : nextIndex.promise;
      }
      if (url.includes("models/ready?model_type=embed_main")) {
        readinessCalls += 1;
        return readinessCalls === 1
          ? Promise.resolve({ data: { ready: false, reason: "index_cleanup_incomplete" } })
          : nextReadiness.promise;
      }
      return Promise.resolve({ data: { ready: true } });
    });
    await mount("");
    expect(await screen.findByRole("status", { name: noticeName })).toHaveTextContent(t("knowledge.embeddingIndexCleanupFailed"));

    await refreshEmbedding();
    await waitFor(() => expect([indexCalls, readinessCalls]).toEqual([2, 2]));
    await act(async () => {
      nextIndex.resolve({ data: { data: { ...indexFixture, status: "indexed", index_identity: "verified", operation_phase: "idle" } } });
    });

    expect(screen.getByRole("status", { name: noticeName })).toHaveTextContent(t("knowledge.embeddingIndexCleanupFailed"));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();

    await act(async () => { nextReadiness.resolve({ data: { ready: true } }); });
    await waitFor(() => expect(screen.queryByRole("status", { name: noticeName })).not.toBeInTheDocument());
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("publishes a healthy index and text readiness as one snapshot when readiness finishes first", async () => {
    const nextIndex = deferred<{ data: { data: object } }>();
    const nextReadiness = deferred<{ data: { ready: boolean } }>();
    let indexCalls = 0;
    let readinessCalls = 0;
    mocks.get.mockImplementation((url: string) => {
      if (url.endsWith("/embedding/index-state")) {
        indexCalls += 1;
        return indexCalls === 1
          ? Promise.resolve({ data: { data: { ...indexFixture, status: "clearing", index_identity: "verified", operation_phase: "cleanup_failed" } } })
          : nextIndex.promise;
      }
      if (url.includes("models/ready?model_type=embed_main")) {
        readinessCalls += 1;
        return readinessCalls === 1
          ? Promise.resolve({ data: { ready: false, reason: "index_cleanup_incomplete" } })
          : nextReadiness.promise;
      }
      return Promise.resolve({ data: { ready: true } });
    });
    await mount("");
    expect(await screen.findByRole("status", { name: noticeName })).toHaveTextContent(t("knowledge.embeddingIndexCleanupFailed"));

    await refreshEmbedding();
    await waitFor(() => expect([indexCalls, readinessCalls]).toEqual([2, 2]));
    await act(async () => { nextReadiness.resolve({ data: { ready: true } }); });

    expect(screen.getByRole("status", { name: noticeName })).toHaveTextContent(t("knowledge.embeddingIndexCleanupFailed"));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();

    await act(async () => {
      nextIndex.resolve({ data: { data: { ...indexFixture, status: "indexed", index_identity: "verified", operation_phase: "idle" } } });
    });
    await waitFor(() => expect(screen.queryByRole("status", { name: noticeName })).not.toBeInTheDocument());
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it.each(["failed", "invalid"])("retains a dismissed complete snapshot when the next readiness half is %s", async (outcome) => {
    const nextIndex = deferred<{ data: { data: object } }>();
    const nextReadiness = deferred<{ data: object }>();
    let indexCalls = 0;
    let readinessCalls = 0;
    mocks.get.mockImplementation((url: string) => {
      if (url.endsWith("/embedding/index-state")) {
        indexCalls += 1;
        if (indexCalls === 1) return Promise.resolve({ data: { data: { ...indexFixture, status: "clearing", index_identity: "verified", operation_phase: "cleanup_failed" } } });
        if (indexCalls === 2) return nextIndex.promise;
        return Promise.resolve({ data: { data: { ...indexFixture, status: "indexed", index_identity: "verified", operation_phase: "idle" } } });
      }
      if (url.includes("models/ready?model_type=embed_main")) {
        readinessCalls += 1;
        if (readinessCalls === 1) return Promise.resolve({ data: { ready: false, reason: "index_cleanup_incomplete" } });
        if (readinessCalls === 2) return nextReadiness.promise;
        return Promise.resolve({ data: { ready: true } });
      }
      return Promise.resolve({ data: { ready: true } });
    });
    await mount("");
    await dismissIndexNotice();

    await refreshEmbedding();
    await waitFor(() => expect([indexCalls, readinessCalls]).toEqual([2, 2]));
    await act(async () => {
      nextIndex.resolve({ data: { data: { ...indexFixture, status: "indexed", index_identity: "verified", operation_phase: "idle" } } });
      if (outcome === "failed") nextReadiness.reject(new Error("readiness unavailable"));
      else nextReadiness.resolve({ data: { reason: "missing ready" } });
    });

    expect(screen.queryByRole("status", { name: noticeName })).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();

    await refreshEmbedding();
    await waitFor(() => expect([indexCalls, readinessCalls]).toEqual([3, 3]));
    expect(screen.queryByRole("status", { name: noticeName })).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it.each(["failed", "invalid"])("retains a complete model-warning snapshot when the next index half is %s", async (outcome) => {
    let indexCalls = 0;
    let readinessCalls = 0;
    mocks.get.mockImplementation((url: string) => {
      if (url.endsWith("/embedding/index-state")) {
        indexCalls += 1;
        if (indexCalls === 1) return Promise.resolve({ data: { data: { ...indexFixture, status: "indexed", index_identity: "verified", operation_phase: "idle" } } });
        return outcome === "failed"
          ? Promise.reject(new Error("index unavailable"))
          : Promise.resolve({ data: { data: { status: "not-a-real-status" } } });
      }
      if (url.includes("models/ready?model_type=embed_main")) {
        readinessCalls += 1;
        return Promise.resolve({ data: readinessCalls === 1
          ? { ready: false, reason: "credentials_unavailable" }
          : { ready: true } });
      }
      return Promise.resolve({ data: { ready: true } });
    });
    await mount("");
    expect(await screen.findByRole("alert")).toHaveTextContent(t("knowledge.embeddingNotReadyBanner"));

    await refreshEmbedding();
    await waitFor(() => expect([indexCalls, readinessCalls]).toEqual([2, 2]));

    expect(screen.getByRole("alert")).toHaveTextContent(t("knowledge.embeddingNotReadyBanner"));
    expect(screen.queryByRole("status", { name: noticeName })).not.toBeInTheDocument();
  });

  it("waits for multimodal readiness before publishing the next embedding generation", async () => {
    mocks.imageEmbedRequired = true;
    const nextIndex = deferred<{ data: { data: object } }>();
    const nextReadiness = deferred<{ data: { ready: boolean } }>();
    const nextMultimodal = deferred<{ data: { ready: boolean } }>();
    let indexCalls = 0;
    let readinessCalls = 0;
    let multimodalCalls = 0;
    mocks.get.mockImplementation((url: string) => {
      if (url.endsWith("/embedding/index-state")) {
        indexCalls += 1;
        return indexCalls === 1
          ? Promise.resolve({ data: { data: { ...indexFixture, status: "clearing", index_identity: "verified", operation_phase: "cleanup_failed" } } })
          : nextIndex.promise;
      }
      if (url.includes("model_type=embed_main")) {
        readinessCalls += 1;
        return readinessCalls === 1
          ? Promise.resolve({ data: { ready: false, reason: "index_cleanup_incomplete" } })
          : nextReadiness.promise;
      }
      if (url.includes("model_type=embed_image")) {
        multimodalCalls += 1;
        return multimodalCalls === 1
          ? Promise.resolve({ data: { ready: false, reason: "model_unavailable" } })
          : nextMultimodal.promise;
      }
      return Promise.resolve({ data: { ready: true } });
    });
    await mount("");
    expect(await screen.findByRole("status", { name: noticeName })).toHaveTextContent(t("knowledge.embeddingIndexCleanupFailed"));
    expect(screen.getByRole("alert")).toHaveTextContent(t("knowledge.multimodalEmbeddingNotReadyBanner"));

    await refreshEmbedding();
    await waitFor(() => expect([indexCalls, readinessCalls, multimodalCalls]).toEqual([2, 2, 2]));
    await act(async () => {
      nextIndex.resolve({ data: { data: { ...indexFixture, status: "indexed", index_identity: "verified", operation_phase: "idle" } } });
      nextReadiness.resolve({ data: { ready: true } });
    });

    expect(screen.getByRole("status", { name: noticeName })).toHaveTextContent(t("knowledge.embeddingIndexCleanupFailed"));
    expect(screen.getByRole("alert")).toHaveTextContent(t("knowledge.multimodalEmbeddingNotReadyBanner"));

    await act(async () => { nextMultimodal.resolve({ data: { ready: true } }); });
    await waitFor(() => expect(screen.queryByRole("status", { name: noticeName })).not.toBeInTheDocument());
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("ignores an older complete embedding generation after a newer refresh", async () => {
    const staleIndex = deferred<{ data: { data: object } }>();
    const staleReadiness = deferred<{ data: { ready: boolean; reason: string } }>();
    let indexCalls = 0;
    let readinessCalls = 0;
    mocks.get.mockImplementation((url: string) => {
      if (url.endsWith("/embedding/index-state")) {
        indexCalls += 1;
        return indexCalls === 2 ? staleIndex.promise : Promise.resolve({ data: { data: {
          ...indexFixture, status: "indexed", index_identity: "verified", operation_phase: "idle",
        } } });
      }
      if (url.includes("models/ready?model_type=embed_main")) {
        readinessCalls += 1;
        return readinessCalls === 2 ? staleReadiness.promise : Promise.resolve({ data: { ready: true } });
      }
      return Promise.resolve({ data: { ready: true } });
    });
    await mount("");
    expect(screen.queryByRole("status", { name: noticeName })).not.toBeInTheDocument();

    await refreshEmbedding();
    await waitFor(() => expect([indexCalls, readinessCalls]).toEqual([2, 2]));
    await refreshEmbedding();
    await waitFor(() => expect([indexCalls, readinessCalls]).toEqual([3, 3]));

    await act(async () => {
      staleIndex.resolve({ data: { data: { ...indexFixture, status: "clearing", index_identity: "verified", operation_phase: "cleanup_failed" } } });
      staleReadiness.resolve({ data: { ready: false, reason: "index_cleanup_incomplete" } });
    });
    expect(screen.queryByRole("status", { name: noticeName })).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("does not publish an embedding generation that completes after unmount", async () => {
    const staleIndex = deferred<{ data: { data: object } }>();
    const staleReadiness = deferred<{ data: { ready: boolean; reason: string } }>();
    mocks.get.mockImplementation((url: string) => url.endsWith("/embedding/index-state")
      ? staleIndex.promise
      : url.includes("models/ready?model_type=embed_main")
        ? staleReadiness.promise
        : Promise.resolve({ data: { ready: true } }));
    await mount("");
    await waitFor(() => expect(mocks.get.mock.calls.some(([url]) => url.endsWith("/embedding/index-state"))).toBe(true));
    await act(async () => { cleanup(); });

    mocks.get.mockImplementation((url: string) => Promise.resolve({ data: url.endsWith("/embedding/index-state")
      ? { data: { ...indexFixture, status: "indexed", index_identity: "verified", operation_phase: "idle" } }
      : { ready: true } }));
    await mount("");
    await act(async () => {
      staleIndex.resolve({ data: { data: { ...indexFixture, status: "clearing", index_identity: "verified", operation_phase: "cleanup_failed" } } });
      staleReadiness.resolve({ data: { ready: false, reason: "index_cleanup_incomplete" } });
    });

    expect(screen.queryByRole("status", { name: noticeName })).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it.each([["unknown", "Unknown"], ["clearing", "Clearing"], ["cleared", "Cleared"]])(
    "uses the legacy %s status when the server has no identity or phase fields",
    async (status, message) => {
      configureEmbedding(() => ({ ...indexFixture, status }), { ready: false });

      await mount("");

      const notice = await screen.findByRole("status", { name: noticeName });
      expect(notice).toHaveTextContent(t(`knowledge.embeddingIndex${message}`));
      expect(screen.getAllByRole("status", { name: noticeName })).toEqual([notice]);
      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    },
  );

  it("dismisses the index notice locally until a different identity or phase arrives", async () => {
    let index = { ...indexFixture, status: "clearing", index_identity: "verified", operation_phase: "clearing" };
    let unavailable = false;
    configureEmbedding(() => {
      if (unavailable) throw new Error("index status temporarily unavailable");
      return index;
    }, { ready: false, reason: "index_cleanup_in_progress" });
    await mount("");

    const readsBeforeClose = mocks.get.mock.calls.length;
    await dismissIndexNotice();
    expect(mocks.get).toHaveBeenCalledTimes(readsBeforeClose);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    unavailable = true;
    await refreshEmbedding();
    unavailable = false;
    index = { ...index, pending_knowledge_bases: 1, model_name: "Updated display label" };
    await refreshEmbedding();
    expect(screen.queryByRole("status", { name: noticeName })).not.toBeInTheDocument();

    index = { ...index, index_identity: "legacy_unknown" };
    await refreshEmbedding();
    await dismissIndexNotice();
    index = { ...index, operation_phase: "cleanup_failed" };
    await refreshEmbedding();
    expect(await screen.findByRole("status", { name: noticeName })).toHaveTextContent(t("knowledge.embeddingIndexCleanupFailed"));
    expectNoOnlineCleanup();
  });

  it("shows a dismissed abnormal notice again when the knowledge page is reopened", async () => {
    configureEmbedding(() => ({ status: "unknown", index_identity: "legacy_unknown", operation_phase: "idle" }),
      { ready: false, reason: "index_identity_unverified" });
    const router = await mount("");
    await dismissIndexNotice();

    fireEvent.click(screen.getByRole("button", { name: "本地手册" }));
    await screen.findByText("知识库详情");
    await act(async () => { await router.navigate(-1); });

    expect(await screen.findByRole("status", { name: noticeName })).toHaveTextContent(t("knowledge.embeddingIndexUnknown"));
  });

  it("does not reopen a dismissed readiness-derived notice after a failed status check", async () => {
    let readyCheckFails = false;
    mocks.get.mockImplementation(async (url: string) => {
      if (url.endsWith("/embedding/index-state") || readyCheckFails) throw new Error("status temporarily unavailable");
      return { data: { ready: false, reason: "index_identity_unverified" } };
    });
    await mount("");
    await dismissIndexNotice();

    readyCheckFails = true;
    await refreshEmbedding();
    readyCheckFails = false;
    await refreshEmbedding();

    expect(screen.queryByRole("status", { name: noticeName })).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("shows complete offline cleanup instructions and opens default-model settings without a cleanup request", async () => {
    mocks.role = "system-admin";
    configureEmbedding(() => ({ status: "unknown", index_identity: "legacy_unknown", operation_phase: "idle" }),
      { ready: false, reason: "index_identity_unverified" });
    const router = await mount("");

    const notice = await screen.findByRole("status", { name: noticeName });
    expect(notice).toHaveTextContent(/停止所有.*读写向量/);
    expect(notice).toHaveTextContent("LAZYMIND_EMBEDDING_MAINTENANCE_MODE=true");
    expect(notice).toHaveTextContent(/LAZYMIND_CORE_DATABASE_URL.*正确.*Core/);
    expect(notice).toHaveTextContent("python -m lazymind.parsing.service.embedding_maintenance --apply --writers-stopped --confirm 'CLEAR TEXT VECTORS'");
    fireEvent.click(within(notice).getByRole("link", { name: t("knowledge.goToConfig") }));
    expect(await screen.findByText("默认模型设置")).toBeInTheDocument();
    expect(router.state.location.search).toBe("?section=model_defaults");
    expectNoOnlineCleanup();
  });

  it("explains that vector search waits for rebuilding while source documents remain", async () => {
    mocks.get.mockImplementation((url: string) => Promise.resolve({ data: url.includes("/embedding/index-state")
      ? { data: {
        status: "rebuilding", model_name: "新向量模型", provider_name: "测试供应商",
        indexed_knowledge_bases: 0, pending_knowledge_bases: 2,
      } }
      : { ready: true } }));

    await mount("");

    const status = await screen.findByRole("status", { name: t("knowledge.embeddingIndexStateTitle") });
    expect(status).toHaveTextContent("2 个知识库");
    expect(status).toHaveTextContent("原文和切片");
    expect(status).toHaveTextContent("向量检索");
  });

  it("defaults the normal entry to all types and queries the selected existing scene", async () => {
    const router = await mount("");
    fireEvent.click(screen.getByRole("button", { name: t("knowledge.filterAndSort") }));
    const types = await screen.findByRole("group", { name: t("knowledge.typeLabel") });
    await within(types).findByRole("option", { name: t("learning.profile.academicPapers.name") });
    expect(within(types).getByRole("option", { name: t("knowledge.mineAllTypes") })).toHaveAttribute("aria-selected", "true");
    expect(manualCalls()[0][0].scene).toBeUndefined();
    fireEvent.click(within(types).getByRole("option", { name: t("learning.profile.academicPapers.name") }));
    await waitFor(() => expect(lastManualCall()?.[0].scene).toBe("academic_papers"));
    expect(lastManualCall()?.[1].params.source).toBe("manual");
    expect(router.state.location.search).toBe("?scene=academic_papers");
    expect(screen.getByRole("tab", { name: "本地上传" })).toBeInTheDocument();
  });

  it("clears the paper type on reset, keeps the entry context, and restores the cleared state from detail", async () => {
    const router = await mount("?scene=academic_papers&showcase_entry=work&page=2");
    fireEvent.click(screen.getByRole("button", { name: t("knowledge.filterAndSortWithCount", { count: 1 }) }));
    const types = await screen.findByRole("group", { name: t("knowledge.typeLabel") });
    expect(await within(types).findByRole("option", { name: t("learning.profile.academicPapers.name") })).toHaveAttribute("aria-selected", "true");
    fireEvent.click(within(types).getByRole("option", { name: t("learning.profile.general.name") }));
    await waitFor(() => expect(lastManualCall()?.[0].scene).toBe("general"));
    expect(new URLSearchParams(router.state.location.search).get("page")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "重置" }));
    await waitFor(() => expect(lastManualCall()?.[0].scene).toBeUndefined());
    expect(lastManualCall()?.[1].params.source).toBeUndefined();
    expect(router.state.location.search).toBe("?showcase_entry=work");
    expect(screen.getByRole("button", { name: t("showcase.backToHome") })).toBeInTheDocument();
    fireEvent.click(await screen.findByRole("button", { name: "本地手册" }));
    expect(router.state.location.pathname).toBe("/lib/knowledge/detail/local-id");
    await act(async () => { await router.navigate(-1); });
    fireEvent.click(await screen.findByRole("button", { name: t("knowledge.filterAndSort") }));
    expect(await screen.findByRole("option", { name: t("knowledge.mineAllTypes") })).toHaveAttribute("aria-selected", "true");
    expect(lastManualCall()?.[0].scene).toBeUndefined();
  });

  it("shows a type-catalog error and loads options when retried", async () => {
    mocks.scenes.mockRejectedValueOnce(new Error("fixture"));
    await mount("?scene=academic_papers&showcase_entry=chat");
    fireEvent.click(screen.getByRole("button", { name: t("knowledge.filterAndSortWithCount", { count: 1 }) }));
    await screen.findByText(t("knowledge.typeLoadFailed"));
    fireEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByRole("option", { name: t("learning.profile.academicPapers.name") })).toHaveAttribute("aria-selected", "true");
  });

  it("selects an accessible paper collection and preserves the filtered origin", async () => {
    mocks.list.mockResolvedValue({ data: { datasets: [{ dataset_id: "papers", display_name: "已有论文集", scene: "academic_papers" }], total_size: 1 } });
    const router = await mount("?scene=academic_papers&showcase_entry=work");
    fireEvent.click(await screen.findByRole("button", { name: /已有论文集/ }));
    expect(router.state.location.pathname).toBe("/lib/knowledge/detail/papers");
    expect(router.state.location.state.knowledgeListOrigin.url).toContain("scene=academic_papers");
    expect(manualCalls()[0][0].scene).toBe("academic_papers");
    expect(manualCalls()[0][1].params.source).toBeUndefined();
    await act(async () => { await router.navigate(-1); });
    expect(await screen.findByText(t("showcase.paperReadingTitle"))).toBeVisible();
    expect(screen.queryByRole("tab", { name: /本地上传/ })).not.toBeInTheDocument();
  });

  it.each(["chat", "work"])("handles an empty collection list and cancels back to %s", async (mode) => {
    mocks.list.mockResolvedValue({ data: { datasets: [], total_size: 0 } });
    const router = await mount(`?scene=academic_papers&showcase_entry=${mode}`);
    expect(await screen.findByText(t("showcase.noPaperLibraries"))).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: t("showcase.backToHome") }));
    expect(await screen.findByText("聊天首页")).toBeInTheDocument();
    expect(router.state.location.search).toBe(`?showcase_entry=${mode}`);
  });
  it("returns from an installed official knowledge base with the original filters", async () => {
    const router = await mount("?source=official&keyword=手册&officialTag=团队&sort=most_used");
    fireEvent.click(await screen.findByRole("button", { name: "官方手册" }));
    await screen.findByText("知识库详情");
    expect(router.state.location.pathname).toBe("/lib/knowledge/detail/official-id");
    expect(router.state.location.state.knowledgeListOrigin.url).toContain("source=official");
    await act(async () => { await router.navigate(-1); });
    expect(await screen.findByRole("tab", { name: "已安装的官方知识库" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByDisplayValue("手册")).toBeVisible();
    expect(new URLSearchParams(router.state.location.search).get("officialTag")).toBe("团队");
    expect(mocks.list.mock.calls.some(([params, options]) => params.orderBy === "most_used" && options.params.source === "official_installed")).toBe(true);
  });

  it("requests the restored local page and opens exactly one detail entry from the name button", async () => {
    const router = await mount("?keyword=手册&tag=团队&page=3&pageSize=20&sort=recent_used");
    await screen.findByRole("button", { name: "本地手册" });
    expect(manualCalls()).toHaveLength(1);
    expect(manualCalls()[0][0]).toMatchObject({ keyword: "手册", tags: ["团队"], pageSize: 20, orderBy: "recent_used" });
    expect(pageOffset(manualCalls()[0][0])).toBe(40);
    fireEvent.click(screen.getByRole("button", { name: "本地手册" }));
    await screen.findByText("知识库详情");
    await act(async () => { await router.navigate(-1); });
    expect(await screen.findByText("第 3 页")).toBeVisible();
    await waitFor(() => expect(manualCalls()).toHaveLength(2));
    expect(pageOffset(manualCalls()[1][0])).toBe(40);
  });

  it("preserves a restored tag while the tag catalog is still loading", async () => {
    const tags = deferred<typeof localResponse>();
    mocks.list.mockImplementation((params) => params.pageSize === 100 ? tags.promise : Promise.resolve(localResponse));
    const router = await mount("?tag=团队&page=3");
    await screen.findByText("第 3 页");
    expect(new URLSearchParams(router.state.location.search).get("tag")).toBe("团队");
    expect(manualCalls()[0][0].tags).toEqual(["团队"]);
    await act(async () => { tags.resolve(localResponse); });
    expect(new URLSearchParams(router.state.location.search).get("tag")).toBe("团队");
    expect(manualCalls()).toHaveLength(1);
  });

  it("keeps restored tags when their catalog request fails", async () => {
    mocks.list.mockImplementation((params) => params.pageSize === 100 ? Promise.reject(new Error("fixture")) : Promise.resolve(localResponse));
    const router = await mount("?tag=团队&page=3");
    await screen.findByRole("button", { name: "本地手册" });
    expect(new URLSearchParams(router.state.location.search).get("tag")).toBe("团队");
  });

  it("preserves an official tag until its catalog finishes loading", async () => {
    const catalog = deferred<unknown[]>();
    const items = await mocks.catalog();
    mocks.catalog.mockReturnValue(catalog.promise);
    const router = await mount("?source=official&officialTag=团队");
    expect(new URLSearchParams(router.state.location.search).get("officialTag")).toBe("团队");
    await act(async () => { catalog.resolve(items); });
    expect(await screen.findByRole("button", { name: "官方手册" })).toBeVisible();
    expect(new URLSearchParams(router.state.location.search).get("officialTag")).toBe("团队");
  });

  it("ignores an older page response after the user has changed pages", async () => {
    const oldPage = deferred<typeof localResponse>();
    mocks.list.mockImplementation((params) => params.pageSize !== 100 && pageOffset(params) === 20
      ? oldPage.promise : Promise.resolve(localResponse));
    const router = await mount("?page=3");
    fireEvent.click(screen.getByText("切到第二页"));
    await screen.findByRole("button", { name: "本地手册" });
    await act(async () => { oldPage.resolve({ data: { datasets: [], total_size: 0 } }); });
    expect(new URLSearchParams(router.state.location.search).get("page")).toBe("2");
    expect(screen.getByRole("button", { name: "本地手册" })).toBeVisible();
  });

  it("clamps a removed last page and requests the remaining page", async () => {
    mocks.list.mockResolvedValue({ data: { datasets: localResponse.data.datasets, total_size: 11 } });
    const router = await mount("?page=9");
    await waitFor(() => expect(new URLSearchParams(router.state.location.search).get("page")).toBe("2"));
    await waitFor(() => expect(manualCalls()).toHaveLength(2));
    expect(manualCalls().map(([params]) => pageOffset(params))).toEqual([80, 10]);
  });

  it("does not reset pagination on request failure and retries the same conditions", async () => {
    mocks.list.mockImplementation((params) => params.pageSize === 100 ? Promise.resolve(localResponse) : Promise.reject(new Error("fixture")));
    const router = await mount("?keyword=手册&tag=团队&page=3");
    const retry = await screen.findByRole("button", { name: "重试" });
    expect(new URLSearchParams(router.state.location.search).get("page")).toBe("3");
    mocks.list.mockResolvedValue(localResponse);
    fireEvent.click(retry);
    await screen.findByRole("button", { name: "本地手册" });
    expect(manualCalls()).toHaveLength(2);
    expect(manualCalls()[1][0]).toMatchObject({ keyword: "手册", tags: ["团队"] });
    expect(pageOffset(manualCalls()[1][0])).toBe(20);
  });

  it("uses one request per pagination change and resets page only on page-size change", async () => {
    const router = await mount("?page=3");
    fireEvent.click(screen.getByRole("tab", { name: "本地上传" }));
    expect(new URLSearchParams(router.state.location.search).get("page")).toBe("3");
    fireEvent.click(await screen.findByText("切到第二页"));
    await waitFor(() => expect(manualCalls()).toHaveLength(2));
    expect(pageOffset(manualCalls()[1][0])).toBe(10);
    fireEvent.click(screen.getByText("每页二十条"));
    await waitFor(() => expect(manualCalls()).toHaveLength(3));
    expect(pageOffset(manualCalls()[2][0])).toBe(0);
    expect(new URLSearchParams(router.state.location.search).get("pageSize")).toBe("20");
    expect(new URLSearchParams(router.state.location.search).get("page")).toBeNull();
  });

  it("does not reset the local page when an inactive official tag is removed", async () => {
    const router = await mount("?page=3&officialTag=已删除标签");
    await waitFor(() => expect(new URLSearchParams(router.state.location.search).get("officialTag")).toBeNull());
    expect(new URLSearchParams(router.state.location.search).get("page")).toBe("3");
    expect(manualCalls()).toHaveLength(1);
  });

  it("keeps settings entry parameters when consuming a cloud-create request", async () => {
    const router = await mount("?from=settings-knowledge&createSource=cloud-documents&provider=feishu");
    await waitFor(() => expect(mocks.cloudCreate).toHaveBeenCalledWith("feishu"));
    const params = new URLSearchParams(router.state.location.search);
    expect(params.get("from")).toBe("settings-knowledge");
    expect(params.get("source")).toBe("cloudArchive");
    expect(params.has("createSource")).toBe(false);
    expect(screen.getByRole("tab", { name: "云文档同步" })).toHaveAttribute("aria-selected", "true");
  });

  it("restores square filters independently of the mine view", async () => {
    await mount("?view=square&keyword=不影响广场&squareKeyword=手册&squareDomain=团队&squareStatus=installed");
    expect(await screen.findByRole("button", { name: /查看知识库.*官方手册|查看.*官方手册.*详情/ })).toBeVisible();
    expect(screen.getByDisplayValue("手册")).toBeVisible();
  });
});

it("reports cleanup acceptance after confirming a knowledge base deletion", async () => {
  localStorage.clear();
  mocks.remove.mockResolvedValue({ data: { items: [{ cleanup_status: "submitted" }] } });
  const { message } = await import("antd");
  const success = vi.spyOn(message, "success");
  const info = vi.spyOn(message, "info");
  await mount("");
  fireEvent.click(await screen.findByRole("button", { name: "delete-local-id" }));
  const dialog = await screen.findByRole("dialog");
  fireEvent.change(within(dialog).getByRole("textbox"), { target: { value: t("knowledge.deleteConfirmText", { name: "本地手册" }) } });
  fireEvent.click(within(dialog).getByRole("button", { name: t("common.confirm") }));
  await waitFor(() => expect(mocks.remove).toHaveBeenCalledWith({ dataset: "local-id" }));
  await waitFor(() => expect(info).toHaveBeenCalledWith(t("knowledge.cleanupAccepted")));
  expect(await screen.findByText(t("knowledge.cleanupInProgress"))).toBeVisible();
  expect(screen.getByText(t("knowledge.cleanupProgress", { completed: 2, total: 5 }))).toBeVisible();
  expect(success).not.toHaveBeenCalledWith(t("knowledge.deleteSuccess"));
  success.mockRestore(); info.mockRestore(); localStorage.clear();
});
