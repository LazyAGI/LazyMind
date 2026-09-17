import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { deferred, desktopTestTranslation as t, installDesktopTestDOM } from "@/test/desktopResourceFixtures";

const mocks = vi.hoisted(() => ({ mode: "desktop", session: vi.fn(), get: vi.fn(), post: vi.fn(), local: vi.fn(), detail: vi.fn(), installs: vi.fn(), install: vi.fn(), update: vi.fn() }));
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t }) }));
vi.mock("@/runtime/mode", async (load) => ({ ...await load<object>(), isDesktopRuntime: () => mocks.mode === "desktop" }));
vi.mock("@/runtime/cloud/session", () => ({ getCloudSession: mocks.session, isCloudBusinessAvailable: (session: any) => session?.state === "signed_in" && session?.configured !== false && session?.reachability !== "unreachable", LAZYMIND_CLOUD_SESSION_CHANGED_EVENT: "lazymind:cloud-session-changed" }));
vi.mock("@/modules/knowledge/api/knowledgeMarket", () => ({
  listKnowledgeMarket: mocks.local, getKnowledgeMarketItem: mocks.detail, listKnowledgeMarketInstalls: mocks.installs,
  listKnowledgeMarketDomains: vi.fn().mockResolvedValue({ domains: { industry: ["本地域"], evaluation: [] } }),
  listKnowledgeMarketTasks: vi.fn().mockResolvedValue({ items: [], total: 0 }), getKnowledgeMarketTask: vi.fn(),
  installKnowledgeMarketItem: mocks.install, updateKnowledgeMarketItem: mocks.update, updateAllKnowledgeMarketItems: vi.fn(),
}));
vi.mock("@/components/request", () => ({ BASE_URL: "", axiosInstance: { defaults: {}, get: mocks.get, post: mocks.post, request: (options: { url: string }) => mocks.get(options.url, options) }, getLocalizedErrorMessage: () => "请求失败", localizeErrorCode: () => "请求失败" }));
vi.mock("@/modules/knowledge/components/SyncKnowledgeBaseCreationFlow", () => ({ default: () => null, useSyncKnowledgeBaseCreation: () => ({}) }));
vi.mock("@/components/ui/TypedConfirmModal", async () => ({ default: (await import("react")).forwardRef(() => null) }));
vi.mock("@/modules/knowledge/components/UpdateModal", async () => ({ default: (await import("react")).forwardRef(() => null) }));
vi.mock("@/modules/knowledge/components/CreateKnowledgeBaseModal", async () => ({ default: (await import("react")).forwardRef(() => null) }));
vi.mock("@/components/ui", () => ({ ListPageTable: () => null }));
vi.mock("@/modules/knowledge/components/KnowledgeTag", () => ({ default: () => null }));
vi.mock("@/components/auth", () => ({ AgentAppsAuth: { getUserInfo: () => ({}) } }));
vi.mock("@/modules/knowledge/utils/request", () => ({ KnowledgeBaseServiceApi: () => ({ datasetServiceListDatasets: vi.fn().mockResolvedValue({ data: { datasets: [] } }) }) }));
vi.mock("@/modules/dataSource/api/clients", () => ({ dataSourceScanApi: {} }));
vi.mock("@/hooks/useModelFeatures", () => ({ fetchModelFeatures: vi.fn().mockResolvedValue({}), isImageEmbedRequired: () => false, MODEL_FEATURES_CHANGED_EVENT: "features-changed" }));
import KnowledgePage from "./index";

const localItem = { id: "same-key", name: "本地知识", category: "industry", domain: "本地域", description: "本地目录说明", tags: [], icon: "", data_source: "本地数据来源", online_access_url: "", sort_order: 0, version: "local-v3", created_at: "2026-09-01", updated_at: "2026-09-03" };
const cloudItem = { catalog_key: "same-key", name: "云端知识", category: "industry", domain: "云端领域", description: "云端目录说明", tags: ["云端标签"], icon: "", data_source: "云端数据来源", online_access_url: "", version: 7, published_at: "2026-09-04T00:00:00Z", updated_at: "2026-09-04T00:00:00Z" };
const envelope = (data: unknown) => ({ data: { data } });
async function mount() {
  await act(async () => { render(<ConfigProvider button={{ autoInsertSpace: false }} theme={{ token: { motion: false } }}><MemoryRouter initialEntries={["/lib/knowledge/list"]}><KnowledgePage /></MemoryRouter></ConfigProvider>); });
  fireEvent.click(screen.getByRole("tab", { name: /知识广场/ }));
}
const cloudRequests = () => mocks.get.mock.calls.filter(([url]) => String(url).includes("/cloud/knowledge-market"));

beforeAll(installDesktopTestDOM);
beforeEach(() => {
  vi.clearAllMocks(); mocks.mode = "desktop";
  mocks.session.mockResolvedValue({ configured: true, reachability: "reachable", state: "signed_in", account_id: "account-a" });
  mocks.local.mockResolvedValue([localItem]); mocks.installs.mockResolvedValue({ total: 0, items: [] });
  mocks.detail.mockResolvedValue({ ...localItem, sample_questions: ["本地示例问题"] });
  mocks.get.mockImplementation(async (url: string) => {
    if (url.includes("/cloud/knowledge-market/items/")) return envelope({ ...cloudItem, package_url: "https://packages.example.test/fixture.zip", package_revision: "fixture-revision", source_adapter: "generic_markdown", adapter_options: {}, sample_questions: ["云端独有示例问题"] });
    if (url.includes("/cloud/knowledge-market")) return envelope({ items: [cloudItem], catalog_revision: 9 });
    return { data: { ready: true } };
  });
});
afterEach(cleanup);

describe("Desktop 知识广场 combined sources without deduplication", () => {
  it("keeps same-key and same-name catalogs as separate cards", async () => {
    mocks.local.mockResolvedValue([{ ...localItem, name: "重复知识" }]);
    mocks.get.mockImplementation(async (url: string) => url.includes("/cloud/knowledge-market") ? envelope({ items: [{ ...cloudItem, name: "重复知识" }], catalog_revision: 9 }) : { data: { ready: true } });
    await mount(); await waitFor(() => expect(screen.getAllByRole("button", { name: /重复知识/ })).toHaveLength(2));
    expect(screen.getByText("本地目录说明")).toBeVisible(); expect(screen.getByText("云端目录说明")).toBeVisible();
  });

  it("fetches cloud detail using catalog_key and does not call local detail or installation", async () => {
    await mount(); fireEvent.click(await screen.findByRole("button", { name: /云端知识/ }));
    const dialog = await screen.findByRole("dialog");
    await waitFor(() => expect(within(dialog).getByText("云端独有示例问题")).toBeVisible());
    expect(mocks.get.mock.calls.some(([url]) => String(url).endsWith("/cloud/knowledge-market/items/same-key"))).toBe(true);
    expect(mocks.detail).not.toHaveBeenCalled(); expect(mocks.install).not.toHaveBeenCalled(); expect(mocks.post).not.toHaveBeenCalled();
    expect(within(dialog).queryByRole("button", { name: "安装" })).not.toBeInTheDocument();
    expect(within(dialog).getByText("7", { exact: true })).toBeVisible();
  });

  it("does not inherit the local installation state for a Cloud item with the same key", async () => {
    mocks.installs.mockResolvedValue({ total: 1, items: [{ market_item_id: "same-key", dataset_id: "local-dataset", install_state: "done", installed_version: "local-v3", active: false }] });
    await mount(); fireEvent.click(await screen.findByRole("button", { name: /云端知识/ }));
    const dialog = await screen.findByRole("dialog");
    await waitFor(() => expect(within(dialog).getByText("云端独有示例问题")).toBeVisible());
    expect(within(dialog).queryByText("local-v3")).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("button", { name: "打开" })).not.toBeInTheDocument();
  });

  it("keeps the original local detail and install target", async () => {
    await mount(); fireEvent.click(await screen.findByRole("button", { name: /本地知识/ }));
    await waitFor(() => expect(screen.getByText("本地示例问题")).toBeVisible());
    expect(mocks.detail).toHaveBeenCalledWith("same-key");
  });

  it("includes Cloud-only domains and searches both catalogs", async () => {
    await mount(); fireEvent.click(await screen.findByRole("button", { name: "云端领域" }));
    expect(await screen.findByRole("button", { name: /云端知识/ })).toBeVisible();
    expect(screen.queryByRole("button", { name: /本地知识/ })).not.toBeInTheDocument();
  });

  it("loads a Cloud catalog beyond the first 100 items", async () => {
    mocks.get.mockImplementation(async (url: string) => {
      if (!url.includes("/cloud/knowledge-market")) return { data: { ready: true } };
      if (new URL(url, "http://localhost").searchParams.get("cursor") === "next") return envelope({ items: [{ ...cloudItem, catalog_key: "last", name: "最后一项云端知识" }], catalog_revision: 9 });
      return envelope({ items: Array.from({ length: 100 }, (_, i) => ({ ...cloudItem, catalog_key: `item-${i}`, name: `云端项-${i}` })), next_cursor: "next", catalog_revision: 9 });
    });
    await mount(); expect(await screen.findByRole("button", { name: /最后一项云端知识/ })).toBeVisible();
  });

  it("preserves Cloud results when the local source fails", async () => {
    mocks.local.mockRejectedValue(new Error("fixture local unavailable")); await mount();
    expect(await screen.findByRole("button", { name: /云端知识/ })).toBeVisible();
    expect(await screen.findByRole("alert")).toBeVisible();
  });

  it("preserves local results when Cloud fails", async () => {
    mocks.get.mockImplementation(async (url: string) => { if (url.includes("/cloud/knowledge-market")) throw new Error("fixture Cloud unavailable"); return { data: { ready: true } }; });
    await mount(); expect(await screen.findByRole("button", { name: /本地知识/ })).toBeVisible();
    expect(await screen.findByRole("alert")).toBeVisible();
  });

  it("reports a withdrawn Cloud item without falling back to same-key local detail", async () => {
    const normal = mocks.get.getMockImplementation()!;
    mocks.get.mockImplementation(async (url: string, ...args: unknown[]) => { if (url.includes("/cloud/knowledge-market/items/")) throw { response: { status: 404, data: { code: 3000004 } } }; return normal(url, ...args); });
    await mount(); fireEvent.click(await screen.findByRole("button", { name: /云端知识/ }));
    await waitFor(() => expect(screen.getByRole("alert")).toBeVisible()); expect(mocks.detail).not.toHaveBeenCalled();
  });

  it("ignores old Cloud data returned after logout", async () => {
    const pending = deferred<ReturnType<typeof envelope>>();
    mocks.get.mockImplementation((url: string) => url.includes("/cloud/knowledge-market") ? pending.promise : Promise.resolve({ data: { ready: true } }));
    await mount(); await waitFor(() => expect(cloudRequests().length).toBeGreaterThan(0));
    mocks.session.mockResolvedValue({ state: "signed_out" });
    await act(async () => { window.dispatchEvent(new Event("lazymind:cloud-session-changed")); });
    await act(async () => pending.resolve(envelope({ items: [cloudItem], catalog_revision: 9 })));
    expect(screen.queryByRole("button", { name: /云端知识/ })).not.toBeInTheDocument();
  });

  it.each(["signed_out", "offline", "reauth_required"])("does not fetch Cloud while %s", async (state) => {
    mocks.session.mockResolvedValue({ state }); await mount(); await screen.findByRole("button", { name: /本地知识/ });
    expect(cloudRequests()).toHaveLength(0);
    expect(screen.queryByText(t("admin.memoryCloudLoading"))).not.toBeInTheDocument();
    expect(screen.queryByText(t("admin.memoryCloudLoadFailed"))).not.toBeInTheDocument();
  });

  it("keeps Cloud interaction invisible when the local session snapshot cannot be read", async () => {
    mocks.session.mockRejectedValue(new Error("session unavailable"));
    await mount();
    await screen.findByRole("button", { name: /本地知识/ });
    expect(cloudRequests()).toHaveLength(0);
    expect(screen.queryByText(t("admin.memoryCloudLoading"))).not.toBeInTheDocument();
    expect(screen.queryByText(t("admin.memoryCloudLoadFailed"))).not.toBeInTheDocument();
  });

  it("does not fetch Cloud when the configured address is unreachable", async () => {
    mocks.session.mockResolvedValue({
      configured: true,
      reachability: "unreachable",
      state: "signed_in",
    });
    await mount();
    await screen.findByRole("button", { name: /本地知识/ });
    expect(cloudRequests()).toHaveLength(0);
  });

  it.each(["cloud", "local"])("preserves the %s runtime behavior", async (mode) => {
    mocks.mode = mode; await mount(); await screen.findByRole("button", { name: /本地知识/ }); expect(cloudRequests()).toHaveLength(0);
  });
});
