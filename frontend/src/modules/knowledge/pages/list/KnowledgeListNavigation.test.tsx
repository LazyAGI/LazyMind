import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { deferred, desktopTestTranslation as t, installDesktopTestDOM } from "@/test/desktopResourceFixtures";

const mocks = vi.hoisted(() => ({ list: vi.fn(), catalog: vi.fn(), scan: vi.fn(), cloudCreate: vi.fn(), scenes: vi.fn() }));
vi.mock("@/modules/learning/api", () => ({ getLearningCatalog: mocks.scenes }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t }) }));
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
vi.mock("@/components/auth", () => ({ AgentAppsAuth: { getUserInfo: () => ({}) } }));
vi.mock("@/components/request", () => ({ BASE_URL: "", axiosInstance: { get: vi.fn().mockResolvedValue({ data: { ready: true } }) } }));
vi.mock("@/modules/knowledge/utils/request", () => ({ KnowledgeBaseServiceApi: () => ({ datasetServiceListDatasets: mocks.list }) }));
vi.mock("@/modules/dataSource/api/clients", () => ({ dataSourceScanApi: { listSources: mocks.scan } }));
vi.mock("@/hooks/useModelFeatures", () => ({ fetchModelFeatures: vi.fn().mockResolvedValue({}), isImageEmbedRequired: () => false, MODEL_FEATURES_CHANGED_EVENT: "features-changed" }));
vi.mock("@/runtime/mode", async (load) => ({ ...await load<object>(), isDesktopRuntime: () => false }));
vi.mock("@/components/ui", () => ({
  ListPageTable: (props: any) => <div className="list-page-table-scroll">
    {!props.loading && props.dataSource.length === 0 ? props.locale?.emptyText : null}
    {props.dataSource.map((row: any) => <div key={row.dataset_id || row.id} {...props.onRow?.(row)}>
      {props.columns[0].render(row[props.columns[0].dataIndex], row)}
    </div>)}
    {props.pagination && <>
      <span>第 {props.pagination.current} 页</span>
      <button onClick={() => props.onChange({ current: 2, pageSize: props.pagination.pageSize })}>切到第二页</button>
      <button onClick={() => props.onChange({ current: 3, pageSize: 20 })}>每页二十条</button>
    </>}
  </div>,
}));
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
  ], { initialEntries: [`/lib/knowledge/list${search}`] });
  await act(async () => { render(<ConfigProvider button={{ autoInsertSpace: false }} theme={{ token: { motion: false } }}><RouterProvider router={router} /></ConfigProvider>); });
  return router;
}

beforeAll(installDesktopTestDOM);
beforeEach(() => {
  vi.clearAllMocks();
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
